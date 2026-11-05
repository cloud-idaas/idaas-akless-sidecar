'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { pathToFileURL } = require('node:url');
const { EventEmitter } = require('node:events');

const scriptPath = path.join(__dirname, 'rotation-integration-test.js');
const script = new vm.Script(fs.readFileSync(scriptPath, 'utf8'), { filename: pathToFileURL(scriptPath).href });
const start = 1800000000000;

function token(iat, exp) {
  return 'header.' + Buffer.from(JSON.stringify({ iat: iat / 1000, exp: exp / 1000 })).toString('base64url') + '.signature';
}

async function runScenario(options = {}) {
  let now = start;
  let reads = 0;
  const old = token(options.oldIssuedAt ?? start - 300000, options.oldExpiresAt ?? start + 300000);
  const current = token(options.newIssuedAt ?? start, start + 600000);
  const codes = options.codes ?? [200, 200, 200, 401, 200];
  const requests = [];
  const logs = [];
  const processStub = { env: options.env ?? { IDAAS_AKLESS_CREDENTIALS_URI: 'http://127.0.0.1:61765/credentials' }, exitCode: 0 };
  const http = {
    get(url, settings, callback) {
      const request = new EventEmitter();
      request.setTimeout = () => request;
      const index = requests.length;
      requests.push({ url, token: settings.headers.Authorization, at: now });
      queueMicrotask(() => {
        if (index === 2) now += options.oldRequestDelay ?? 0;
        const response = new EventEmitter();
        response.statusCode = codes[index];
        response.resume = () => queueMicrotask(() => response.emit('end'));
        callback(response);
      });
      return request;
    }
  };
  await script.runInNewContext({
    require(name) {
      if (name === '/app/credentials-client') return require('../business-example/credentials-client');
      if (name === 'node:fs') return { readFileSync: () => reads++ === 0 || options.noRotation ? old : current };
      if (name === 'node:http') return http;
      if (name === 'node:timers/promises') return { setTimeout: async ms => { now += ms; } };
      return require(name);
    },
    Buffer, Date: { now: () => now }, process: processStub,
    console: { log: message => logs.push(message), error: message => logs.push(message) }
  });
  assert.equal(logs.some(message => message.includes(old) || message.includes(current)), false);
  return { exitCode: processStub.exitCode, requests, old, current, logs };
}

test('rotation checks resolve an omitted or custom shared port without a URI variable', async () => {
  for (const [env, port] of [[{}, '61765'], [{ IDAAS_AKLESS_SIDECAR_LISTEN_PORT: '61865' }, '61865']]) {
    const result = await runScenario({ env });
    assert.equal(result.exitCode, 0);
    assert.ok(result.requests.every(request => request.url === 'http://127.0.0.1:' + port + '/credentials'));
  }
});

for (const scenario of [
  { name: 'fresh rotation retains a positive grace assertion' },
  { name: 'delayed projection correctly rejects the previous token', newIssuedAt: start - 120000, codes: [200, 200, 401, 401, 200] },
  { name: 'old expiry ends grace before issuance deadline', oldExpiresAt: start + 1000, codes: [200, 200, 401, 401, 200] },
  { name: 'old maximum age caps an extended expiry', oldIssuedAt: start - 3599000, oldExpiresAt: start + 86400000, codes: [200, 200, 401, 401, 200] },
  { name: 'future issuance skew does not break positive grace', newIssuedAt: start + 30000 },
  { name: 'uncertain server observation permits acceptance', newIssuedAt: start - 40000 },
  { name: 'uncertain server observation permits rejection', newIssuedAt: start - 40000, codes: [200, 200, 401, 401, 200] },
  { name: 'request crossing expiry may return acceptance', newIssuedAt: start - 55000, oldRequestDelay: 4000 },
  { name: 'request crossing expiry may return rejection', newIssuedAt: start - 55000, oldRequestDelay: 4000, codes: [200, 200, 401, 401, 200] }
]) {
  test(scenario.name, async () => {
    const result = await runScenario(scenario);
    assert.equal(result.exitCode, 0, result.logs.join('\n'));
    assert.deepEqual(result.requests.map(request => request.token), [result.old, result.current, result.old, result.old, result.current]);
    const lastOldRequest = result.requests[3];
    const latestGraceEnd = Math.min(
      (scenario.newIssuedAt ?? start) + 60000,
      scenario.oldExpiresAt ?? start + 300000,
      (scenario.oldIssuedAt ?? start - 300000) + 3600000
    );
    assert.ok(lastOldRequest.at > latestGraceEnd, 'final rejection must be checked past the latest possible grace');
  });
}

for (const scenario of [
  { name: 'early rejection fails when grace is guaranteed', codes: [200, 200, 401] },
  { name: 'acceptance beyond the grace upper bound fails', newIssuedAt: start - 120000, codes: [200, 200, 200] },
  { name: 'unexpected transition status fails', newIssuedAt: start - 40000, codes: [200, 200, 503] },
  { name: 'old token still accepted after grace fails', codes: [200, 200, 200, 200] },
  { name: 'new token rejection fails', codes: [200, 401] },
  { name: 'missing rotation fails instead of sending an undefined token', noRotation: true }
]) {
  test(scenario.name, async () => {
    const result = await runScenario(scenario);
    assert.equal(result.exitCode, 1);
    assert.ok(result.logs.includes('FAIL real kubelet rotation integration'));
    if (scenario.noRotation) assert.equal(result.requests.length, 1);
  });
}
