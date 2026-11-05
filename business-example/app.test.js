'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const client = require('./credentials-client');
const runtime = require('./runtime');

async function start(env, credential, failure) {
  const observed = {};
  let finish;
  const completed = new Promise(resolve => { finish = resolve; });
  const modules = {
    'node:http': { createServer(handler) {
      assert.equal(handler, runtime.handleRequest);
      return { listen(...args) { observed.listener = args; } };
    } },
    './credentials-client': { ...client, configuredCredentialsURI: () => client.configuredCredentialsURI(env),
      async fetchJson(url, timeout) {
      observed.uri = url;
      observed.timeout = timeout;
      if (failure) throw failure;
      return credential;
    } },
    './runtime': runtime,
  };
  const filename = require.resolve('./app');
  vm.runInNewContext(fs.readFileSync(filename, 'utf8'), {
    require: name => { assert.ok(Object.hasOwn(modules, name)); return modules[name]; },
    URL, process: { env }, console: {
      log: (...args) => { observed.success = args; finish(); },
      error: (...args) => { observed.error = args; finish(); },
    },
  }, { filename });
  await completed;
  return observed;
}

const credential = { AccessKeyId: 'example-id', AccessKeySecret: 'example-secret',
  SecurityToken: 'example-token', Expiration: '2099-01-01T00:00:00Z' };

test('business can use port 8080 while fetching credentials from the new default', async () => {
  const result = await start({ PORT: '8080' }, credential);
  assert.deepEqual(result.listener, ['8080', '0.0.0.0']);
  assert.equal(result.uri.href, 'http://127.0.0.1:61765/credentials');
  assert.equal(result.timeout, 15000);
  assert.equal(result.success[1], credential.Expiration);
  assert.equal(result.error, undefined);
});

test('explicit legacy credential URI and role remain supported', async () => {
  const result = await start({ IDAAS_AKLESS_CREDENTIALS_URI: 'http://127.0.0.1:8080/credentials',
    IDAAS_AKLESS_CLOUD_ROLE_ARN: 'example-role', IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS: '20000' }, credential);
  assert.deepEqual(result.listener, [3000, '0.0.0.0']);
  assert.equal(result.uri.origin, 'http://127.0.0.1:8080');
  assert.equal(result.uri.searchParams.get('roleArn'), 'example-role');
  assert.equal(result.timeout, 20000);
  assert.equal(result.error, undefined);
});

test('optional shared port configures the business URI without a separate URI variable', async () => {
  for (const [port, expected] of [['61865', '61865'], ['', '61765']]) {
    const result = await start({ IDAAS_AKLESS_SIDECAR_LISTEN_PORT: port }, credential);
    assert.equal(result.uri.origin, 'http://127.0.0.1:' + expected);
    assert.equal(result.error, undefined);
  }
});

test('startup reports incomplete credentials without logging the returned secrets', async () => {
  const result = await start({}, { AccessKeyId: 'example-id' });
  assert.deepEqual(result.error, ['[err]', 'missing Alibaba Cloud STS credential fields']);
  assert.equal(result.success, undefined);
});

test('startup preserves credential retrieval failures', async () => {
  const result = await start({}, undefined, new Error('credential request failed'));
  assert.deepEqual(result.error, ['[err]', 'credential request failed']);
  assert.equal(result.success, undefined);
});
