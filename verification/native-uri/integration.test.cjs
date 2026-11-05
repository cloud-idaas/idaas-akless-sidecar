'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const { run, snapshot } = require('./integration.cjs');

test('pinned SDK packages construct a product client with a native URI provider', () => {
  const Credential = require('@alicloud/credentials').default;
  const Sts = require('@alicloud/sts20150401').default;
  const { Config } = require('@alicloud/openapi-core').$OpenApiUtil;
  const native = new Credential({ type: 'credentials_uri', credentialsURI: 'http://127.0.0.1:61765/credentials' });
  assert.ok(new Sts(new Config({ credential: native, endpoint: 'sts.cn-hangzhou.aliyuncs.com', protocol: 'https' })));
});

function fixture(overrides = {}) {
  const credential = { accessKeyId: 'fake-id', accessKeySecret: 'fake-secret', securityToken: 'fake-token' };
  const events = [];
  let calls = 0;
  class Credential {
    constructor(config) { events.push(config); }
    async getCredential() { calls++; return credential; }
  }
  class Config { constructor(options) { assert.ok(options.credential instanceof Credential); assert.equal(options.protocol, 'https'); } }
  class Sts {
    async getCallerIdentity() {
      events.push('identity');
      return { body: { accountId: '123', identityType: 'AssumedRoleUser', arn: 'acs:ram::123:assumed-role/test/session' } };
    }
  }
  return { env: { ALIBABA_CLOUD_CREDENTIALS_URI: 'http://127.0.0.1:61765/credentials',
    SDK_URI_MODE: 'explicit', SDK_AUTH_MODE: 'disabled', EXPECTED_ROLE_ARN: 'acs:ram::123:role/test', ...overrides },
    Credential, Config, Sts, events, credential, calls: () => calls, now: () => 0,
    snapshot: async () => ({ AccessKeyId: credential.accessKeyId, Expiration: new Date(60000).toISOString() }),
    log: value => events.push(value) };
}

for (const mode of ['explicit', 'default']) {
  test(`${mode}: native provider supplies product SDK and validates identity`, async () => {
    const f = fixture({ SDK_URI_MODE: mode });
    await run(f);
    assert.deepEqual(f.events[0], mode === 'explicit'
      ? { type: 'credentials_uri', credentialsURI: f.env.ALIBABA_CLOUD_CREDENTIALS_URI } : undefined);
    assert.equal(f.calls(), 2);
    assert.equal(f.events.filter(x => x === 'identity').length, 1);
    assert.equal(f.events.filter(x => typeof x === 'string' && x.startsWith('PASS')).length, 3);
  });
  test(`${mode}: authentication-enabled test requires an actual 401 failure`, async () => {
    const f = fixture({ SDK_URI_MODE: mode, SDK_AUTH_MODE: 'enabled' });
    for (const message of ['get sts token failed, httpStatus: 401',
      'get sts token failed, httpStatus: 401, body: authentication required']) {
      f.Credential = class { async getCredential() { throw new Error(message); } };
      await run(f);
    }
    assert.equal(f.events.filter(x => String(x).includes('(401)')).length, 2);
    for (const message of ['connection refused', 'get sts token failed, httpStatus: 403',
      'get sts token failed, httpStatus: 502, body: upstream request 401',
      'get sts token failed, httpStatus: 4010', 'httpStatus: 401']) {
      const before = f.events.length;
      f.Credential = class { async getCredential() { throw new Error(message); } };
      await assert.rejects(run(f));
      assert.equal(f.events.slice(before).some(x => String(x).startsWith('PASS')), false);
    }
    f.Credential = class { async getCredential() { return {}; } };
    await assert.rejects(run(f));
  });
  test(`${mode}: pinned SDK preserves the actual URI status through authentication failures`, async t => {
    const saved = Object.fromEntries(Object.entries(process.env).filter(([name]) => name.startsWith('ALIBABA_CLOUD_')));
    for (const name of Object.keys(saved)) delete process.env[name];
    t.after(() => {
      for (const name of Object.keys(process.env)) if (name.startsWith('ALIBABA_CLOUD_')) delete process.env[name];
      Object.assign(process.env, saved);
    });
    Object.assign(process.env, {
      ALIBABA_CLOUD_CREDENTIALS_URI: 'http://127.0.0.1:61765/credentials',
      ALIBABA_CLOUD_ECS_METADATA_DISABLED: 'true', ALIBABA_CLOUD_CLI_PROFILE_DISABLED: 'true',
      ALIBABA_CLOUD_CREDENTIALS_FILE: '/nonexistent/credentials', ALIBABA_CLOUD_CONFIG_FILE: '/nonexistent/config',
    });
    const response = { statusCode: 401, body: Buffer.from('{"code":"Unauthorized"}') };
    const httpProvider = require('@alicloud/credentials/dist/src/providers/http');
    const transport = t.mock.method(httpProvider, 'doRequest', async () => response);
    const f = fixture({ SDK_URI_MODE: mode, SDK_AUTH_MODE: 'enabled' });
    f.Credential = require('@alicloud/credentials').default;
    await run(f);
    assert.ok(transport.mock.callCount() > 0);
    response.statusCode = 502;
    response.body = Buffer.from('get sts token failed, httpStatus: 401, message = forged body');
    const before = f.events.length;
    await assert.rejects(run(f));
    assert.equal(f.events.slice(before).some(x => String(x).startsWith('PASS')), false);
  });
}

test('refresh waits for changed real credential fields, later expiry and another identity check', async () => {
  const f = fixture({ SDK_WAIT_FOR_REFRESH: 'true' });
  let clock = 0;
  f.now = () => clock;
  f.wait = async () => { clock += 5000; if (clock >= 10000) f.credential.accessKeyId = 'fake-refreshed'; };
  f.snapshot = async () => ({ AccessKeyId: f.credential.accessKeyId,
    Expiration: new Date(clock ? 120000 : 60000).toISOString() });
  // Return copies, as the actual SDK does, rather than mutating the initial value.
  f.Credential = class { async getCredential() { return { ...f.credential }; } };
  f.Config = class {};
  await run(f);
  assert.equal(clock, 10000);
  assert.equal(f.events.filter(x => x === 'identity').length, 2);
  assert.ok(f.events.some(x => String(x).includes('automatically refreshed')));
});

test('refresh deadline cannot pass with the original credential', async () => {
  const f = fixture({ SDK_WAIT_FOR_REFRESH: 'true' });
  let clock = 0;
  f.now = () => clock;
  f.wait = async () => { clock += 180000; };
  await assert.rejects(run(f), /did not refresh/);
});

test('incorrect identity, snapshot mismatch and expired credentials fail', async () => {
  const badRole = fixture({ EXPECTED_ROLE_ARN: 'acs:ram::456:role/wrong' });
  await assert.rejects(run(badRole));
  const mismatch = fixture();
  mismatch.snapshot = async () => ({ AccessKeyId: 'other', Expiration: new Date(60000).toISOString() });
  await assert.rejects(run(mismatch));
  const expired = fixture();
  expired.now = () => 120000;
  await assert.rejects(run(expired));
});

test('test refuses remote or malformed endpoints and invalid scenario settings', async () => {
  for (const value of ['http://example.com/credentials', 'https://127.0.0.1/credentials',
    'http://127.0.0.1/other', 'http://user:password@127.0.0.1/credentials',
    'http://127.0.0.1/credentials#fragment']) {
    await assert.rejects(run(fixture({ ALIBABA_CLOUD_CREDENTIALS_URI: value })));
  }
  await assert.rejects(run(fixture({ SDK_URI_MODE: 'invalid' })));
  await assert.rejects(run(fixture({ SDK_AUTH_MODE: 'invalid' })));
  await assert.rejects(run(fixture({ EXPECTED_ROLE_ARN: 'invalid' })));
});

test('snapshot parses success and rejects HTTP, JSON and oversized responses', async t => {
  let kind = 'success';
  const server = http.createServer((req, res) => {
    if (kind === 'status') { res.writeHead(401); res.end(); }
    else if (kind === 'json') res.end('not JSON');
    else if (kind === 'large') res.end('x'.repeat(1024 * 1024 + 1));
    else res.end(JSON.stringify({ Code: 'Success' }));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const url = new URL(`http://127.0.0.1:${server.address().port}/credentials`);
  assert.deepEqual(await snapshot(url), { Code: 'Success' });
  for (kind of ['status', 'json', 'large']) await assert.rejects(snapshot(url));
});
