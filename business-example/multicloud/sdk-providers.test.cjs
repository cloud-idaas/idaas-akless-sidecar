'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { Readable } = require('node:stream');
const { NodeHttpHandler } = require('@smithy/node-http-handler');
const { awsCredentialsURI, alibabaCredentialsURI, main } = require('./integration.cjs');
const { normalize } = require('./providers.cjs');

const roles = {
  IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN: 'acs:ram::1234567890123456:role/example',
  IDAAS_AKLESS_TEST_AWS_ROLE_ARN: 'arn:aws-cn:iam::123456789012:role/example',
  IDAAS_AKLESS_TEST_TENCENT_ROLE_ARN: 'qcs::cam::uin/123456789012:roleName/example',
};
const tokenFile = '/run/secrets/idaas-akless/credential-auth/token';
const expiration = () => new Date(Date.now() + 3600000).toISOString();
const awsResponse = () => ({ AccessKeyId: 'FAKE_ID', SecretAccessKey: 'FAKE_SECRET', Token: 'FAKE_TOKEN', Expiration: expiration() });
const alibabaResponse = () => ({ Code: 'Success', AccessKeyId: 'FAKE_ID', AccessKeySecret: 'FAKE_SECRET',
  SecurityToken: 'FAKE_TOKEN', Expiration: expiration() });

function environment(t, overrides = {}) {
  const values = { ...roles, IDAAS_AKLESS_CREDENTIALS_URI: undefined,
    IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE: undefined,
    AWS_CONTAINER_CREDENTIALS_RELATIVE_URI: undefined, AWS_CONTAINER_CREDENTIALS_FULL_URI: undefined,
    AWS_CONTAINER_AUTHORIZATION_TOKEN: undefined, AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE: undefined, ...overrides };
  const original = Object.fromEntries(Object.keys(values).map(key => [key, process.env[key]]));
  function set(entries) {
    for (const [key, value] of Object.entries(entries)) {
      if (value === undefined) delete process.env[key]; else process.env[key] = value;
    }
  }
  set(values);
  t.after(() => set(original));
}

// Keep the pinned AWS provider real; replace only filesystem and HTTP boundaries.
function awsBoundary(t, payload = awsResponse()) {
  const requests = [];
  const reads = [];
  let destroyed = 0;
  const state = { payload, status: 200, token: 'fake.local.signature', readError: undefined, requestError: undefined };
  t.mock.method(fs, 'readFile', async file => {
    reads.push(file);
    if (state.readError) throw state.readError;
    return Buffer.from(state.token);
  });
  t.mock.method(NodeHttpHandler, 'create', options => {
    assert.equal(options.connectionTimeout, 15000);
    return {
      async handle(request, options) {
        assert.equal(options.requestTimeout, 15000);
        requests.push(request);
        if (state.requestError) throw state.requestError;
        const body = typeof state.payload === 'string' ? state.payload : JSON.stringify(state.payload);
        return { response: { statusCode: state.status, headers: {}, body: Readable.from([body]) } };
      },
      destroy() { destroyed++; },
    };
  });
  return { requests, reads, state, destroyed: () => destroyed };
}

test('regression: AWS native URI cannot be displaced by inherited metadata configuration', async t => {
  environment(t, { AWS_CONTAINER_CREDENTIALS_RELATIVE_URI: '/unexpected-metadata-source' });
  const boundary = awsBoundary(t);
  await awsCredentialsURI()();
  assert.equal(boundary.requests[0].hostname, '127.0.0.1');
  assert.equal(boundary.requests[0].path, '/credentials');
  assert.equal(boundary.requests[0].query.roleArn, roles.IDAAS_AKLESS_TEST_AWS_ROLE_ARN);
});

test('AWS pinned provider rereads the token and replaces the whole STS triple on each resolution', async t => {
  environment(t, { IDAAS_AKLESS_CREDENTIALS_URI: 'http://127.0.0.1:8081/credentials',
    IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE: '/run/custom/local-token',
    AWS_CONTAINER_CREDENTIALS_FULL_URI: 'http://169.254.170.2/ignored',
    AWS_CONTAINER_AUTHORIZATION_TOKEN: 'ignored.static.token',
    AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE: '/ignored/aws-token' });
  const b = awsBoundary(t);
  const provider = awsCredentialsURI();
  const first = await provider();
  b.state.token = 'rotated.local.signature';
  b.state.payload = { AccessKeyId: 'NEXT_ID', SecretAccessKey: 'NEXT_SECRET', Token: 'NEXT_TOKEN', Expiration: expiration() };
  const second = await provider();
  assert.equal(first.accessKeyId, 'FAKE_ID');
  assert.deepEqual([second.accessKeyId, second.secretAccessKey, second.sessionToken], ['NEXT_ID', 'NEXT_SECRET', 'NEXT_TOKEN']);
  assert.ok(second.expiration instanceof Date);
  assert.deepEqual(b.reads, ['/run/custom/local-token', '/run/custom/local-token']);
  assert.deepEqual(b.requests.map(r => r.headers.Authorization), ['fake.local.signature', 'rotated.local.signature']);
  assert.ok(b.requests.every(r => r.hostname === '127.0.0.1' && r.port === 8081 && r.path === '/credentials'
    && r.query.roleArn === roles.IDAAS_AKLESS_TEST_AWS_ROLE_ARN));
  assert.equal(b.destroyed(), 2);
});

test('AWS file failures stop before HTTP and never use a static token fallback', async t => {
  environment(t, { AWS_CONTAINER_AUTHORIZATION_TOKEN: 'ignored.static.token' });
  const b = awsBoundary(t);
  b.state.readError = Object.assign(new Error('missing fixture file'), { code: 'ENOENT' });
  await assert.rejects(awsCredentialsURI()(), { code: 'ENOENT' });
  assert.deepEqual(b.reads, [tokenFile]);
  assert.equal(b.requests.length, 0);
  assert.equal(b.destroyed(), 1);
});

test('AWS pinned provider rejects HTTP, parsing and network failures without retry', async t => {
  environment(t);
  const b = awsBoundary(t);
  for (const status of [301, 401, 403, 502]) {
    b.state.status = status;
    const before = b.requests.length;
    await assert.rejects(awsCredentialsURI()());
    assert.equal(b.requests.length, before + 1);
  }
  b.state.status = 200;
  for (const payload of ['not-json', { ...awsResponse(), Token: undefined }, { ...awsResponse(), Expiration: 'not-a-date' }]) {
    b.state.payload = payload;
    await assert.rejects(awsCredentialsURI()());
  }
  b.state.requestError = Object.assign(new Error('simulated network timeout'), { name: 'TimeoutError' });
  const before = b.requests.length;
  await assert.rejects(awsCredentialsURI()());
  assert.equal(b.requests.length, before + 1);
  assert.equal(b.destroyed(), b.requests.length);
});

test('AWS rejects untrusted URI and relative token paths before constructing a transport', t => {
  environment(t, { IDAAS_AKLESS_CREDENTIALS_URI: 'http://example.invalid/credentials' });
  const create = t.mock.method(NodeHttpHandler, 'create', () => { throw new Error('transport must not be constructed'); });
  assert.throws(() => awsCredentialsURI(), /untrusted/);
  process.env.IDAAS_AKLESS_CREDENTIALS_URI = 'http://127.0.0.1:61765/credentials';
  process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE = 'relative/token';
  assert.throws(() => awsCredentialsURI(), /must be absolute/);
  assert.equal(create.mock.callCount(), 0);
});

// Keep the pinned Alibaba URI provider and its cache real, replacing its HTTP boundary.
function alibabaBoundary(t) {
  const http = require('@alicloud/credentials/dist/src/providers/http');
  const requests = [];
  const state = { status: 200, payload: alibabaResponse() };
  t.mock.method(http, 'doRequest', async request => {
    requests.push(request);
    const body = typeof state.payload === 'string' ? state.payload : JSON.stringify(state.payload);
    return { statusCode: state.status, body: Buffer.from(body) };
  });
  return { state, requests };
}

test('Alibaba pinned native provider uses the configured role URI, caches, and refreshes at a simulated stale point', async t => {
  environment(t, { IDAAS_AKLESS_CREDENTIALS_URI: 'http://127.0.0.1:8081/credentials',
    ALIBABA_CLOUD_ACCESS_KEY_ID: 'IGNORED_ID', ALIBABA_CLOUD_ACCESS_KEY_SECRET: 'IGNORED_SECRET' });
  const now = Date.parse('2030-01-01T00:00:00Z');
  t.mock.timers.enable({ apis: ['Date'], now });
  const b = alibabaBoundary(t);
  const provider = alibabaCredentialsURI();
  const first = await provider();
  assert.deepEqual(first, { accessKeyId: 'FAKE_ID', secretAccessKey: 'FAKE_SECRET', sessionToken: 'FAKE_TOKEN' });
  assert.deepEqual(await provider(), first);
  assert.equal(b.requests.length, 1);
  t.mock.timers.tick(46 * 60 * 1000);
  b.state.payload = { ...alibabaResponse(), AccessKeyId: 'NEXT_ID', AccessKeySecret: 'NEXT_SECRET', SecurityToken: 'NEXT_TOKEN' };
  assert.deepEqual(await provider(), { accessKeyId: 'NEXT_ID', secretAccessKey: 'NEXT_SECRET', sessionToken: 'NEXT_TOKEN' });
  assert.equal(b.requests.length, 2);
  for (const request of b.requests) {
    const uri = new URL(request.toRequestURL());
    assert.equal(uri.origin, 'http://127.0.0.1:8081');
    assert.equal(uri.searchParams.get('roleArn'), roles.IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN);
    assert.equal(request.method, 'GET');
    assert.equal(request.headers.Authorization, undefined);
  }
});

test('Alibaba pinned native provider rejects missing or blank credentials and malformed responses', async t => {
  environment(t);
  const b = alibabaBoundary(t);
  for (const payload of ['not-json', { ...alibabaResponse(), AccessKeyId: undefined },
    { ...alibabaResponse(), AccessKeySecret: ' ' }, { ...alibabaResponse(), SecurityToken: ' ' }]) {
    b.state.payload = payload;
    await assert.rejects(alibabaCredentialsURI()());
  }
  assert.equal(b.requests.length, 4);
});

function matrixOptions(overrides = {}) {
  const provider = async () => ({ accessKeyId: 'FAKE_ID', secretAccessKey: 'FAKE_SECRET', sessionToken: 'FAKE_TOKEN',
    expiration: new Date(expiration()) });
  return { fromURI: () => provider, nativeAWS: () => provider, query: async (_, getCredential) => { await getCredential(); return []; },
    nativeAlibabaOnly: false, log: () => {}, ...overrides };
}

test('Alibaba authentication-enabled matrix accepts only the actual native 401, never a 403 or 502', async t => {
  environment(t);
  const b = alibabaBoundary(t);
  b.state.status = 401;
  let result = await main(matrixOptions());
  assert.equal(result.length, 9);
  assert.deepEqual(result.at(-1), { cloud: 'alibaba', mode: 'native-uri-authentication-boundary', ok: true, expectedRejection: 401 });
  for (const status of [403, 502]) {
    b.state.status = status;
    result = await main(matrixOptions());
    assert.equal(result.at(-1).ok, false);
  }
  b.state.status = 401;
  result = await main(matrixOptions({ nativeAlibabaOnly: true }));
  assert.equal(result.length, 2);
  assert.ok(result.every(r => !r.ok));
});

test('regression: an upstream body mentioning 401 cannot disguise a native 502 as expected rejection', async t => {
  environment(t);
  const b = alibabaBoundary(t);
  b.state.status = 502;
  b.state.payload = 'untrusted detail: httpStatus: 401';
  const result = await main(matrixOptions());
  assert.equal(result.at(-1).ok, false);
});

test('regression: AWS native provider rejects blank secrets and already expired STS', async t => {
  environment(t);
  const boundary = awsBoundary(t);
  for (const patch of [{ AccessKeyId: '' }, { SecretAccessKey: ' ' }, { Token: ' ' },
    { Expiration: '2000-01-01T00:00:00Z' }]) {
    boundary.state.payload = { ...awsResponse(), ...patch };
    await assert.rejects(awsCredentialsURI()(), /invalid or near-expiry credential/);
  }
});

test('regression: Alibaba HTTP adapter rejects a non-Success response with credential-shaped fields', () => {
  assert.throws(() => normalize('alibaba', { Code: 'Failed', AccessKeyId: 'FAKE_ID',
    AccessKeySecret: 'FAKE_SECRET', SecurityToken: 'FAKE_TOKEN', Expiration: expiration() }), /invalid/);
});

test('regression: Tencent HTTP adapter rejects inconsistent absolute expiry', () => {
  assert.throws(() => normalize('tencent', { tmpSecretId: 'FAKE_ID', tmpSecretKey: 'FAKE_SECRET',
    token: 'FAKE_TOKEN', expiration: expiration(), expiredTime: 1 }), /invalid/);
});
