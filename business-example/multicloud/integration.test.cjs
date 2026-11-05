'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { ROLE_ENV_VARS, credentialsURI, normalize, validateCredential, fromCredentialsURI } = require('./providers.cjs');
const { resources, main } = require('./integration.cjs');
const env = {
  IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN: 'acs:ram::1234567890123456:role/example',
  IDAAS_AKLESS_TEST_AWS_ROLE_ARN: 'arn:aws-cn:iam::123456789012:role/example',
  IDAAS_AKLESS_TEST_TENCENT_ROLE_ARN: 'qcs::cam::uin/123456789012:roleName/example',
};
const expiration = new Date(Date.now() + 3600000).toISOString();
const fixtures = {
  alibaba: { Code: 'Success', AccessKeyId: 'test-id', AccessKeySecret: 'test-secret', SecurityToken: 'test-token', Expiration: expiration },
  aws: { AccessKeyId: 'test-id', SecretAccessKey: 'test-secret', Token: 'test-token', Expiration: expiration },
  tencent: { tmpSecretId: 'test-id', tmpSecretKey: 'test-secret', token: 'test-token', expiration,
    expiredTime: Math.floor(Date.parse(expiration) / 1000) },
};

test('URI encodes each configured role and refuses remote credential endpoints', () => {
  for (const cloud of Object.keys(ROLE_ENV_VARS)) {
    assert.equal(credentialsURI(cloud, undefined, env).origin, 'http://127.0.0.1:61765');
    assert.equal(credentialsURI(cloud, undefined, env).searchParams.get('roleArn'), env[ROLE_ENV_VARS[cloud]]);
    for (const role of [undefined, '', '<role-arn>', 'invalid role', 'role\n']) {
      assert.throws(() => credentialsURI(cloud, undefined, { [ROLE_ENV_VARS[cloud]]: role }), /authorized role identifier/);
    }
  }
  for (const base of ['https://127.0.0.1/credentials', 'http://example.com/credentials',
    'http://u:p@127.0.0.1/credentials', 'http://127.0.0.1/other', 'http://127.0.0.1/credentials#fragment']) {
    assert.throws(() => credentialsURI('aws', base), /untrusted/);
  }
  assert.throws(() => credentialsURI('other'), /unsupported/);
});

test('each cloud follows the optional shared port with an explicit URI override', () => {
  for (const cloud of Object.keys(ROLE_ENV_VARS)) {
    const shared = { ...env, IDAAS_AKLESS_SIDECAR_LISTEN_PORT: '61865' };
    assert.equal(credentialsURI(cloud, undefined, shared).port, '61865');
    assert.equal(credentialsURI(cloud, undefined, { ...shared,
      IDAAS_AKLESS_CREDENTIALS_URI: 'http://127.0.0.1:8080/credentials' }).port, '8080');
    assert.equal(credentialsURI(cloud, undefined, { ...shared, IDAAS_AKLESS_SIDECAR_LISTEN_PORT: '' }).port, '61765');
  }
});

test('all vendor response contracts retain the session token and reject expiry or missing secrets', () => {
  for (const [cloud, fixture] of Object.entries(fixtures)) {
    const c = normalize(cloud, fixture);
    assert.deepEqual(c, { accessKeyId: 'test-id', secretAccessKey: 'test-secret', sessionToken: 'test-token', expiration: new Date(expiration) });
    assert.throws(() => normalize(cloud, fixture, new Date(expiration).getTime() - 60000), /near-expiry/);
    for (const key of Object.keys(fixture)) assert.throws(() => normalize(cloud, { ...fixture, [key]: ' ' }), /invalid/);
  }
  assert.throws(() => normalize('other', {}), /unsupported/);
  assert.throws(() => normalize('aws', null), /invalid/);
});

test('URI adapter fetches again for each credential resolution and propagates local authentication failure', async () => {
  let requests = 0;
  const provider = fromCredentialsURI('aws', { env, fetch: async (uri, timeout) => {
    assert.equal(uri.hostname, '127.0.0.1'); assert.equal(timeout, 15000);
    requests++; return { ...fixtures.aws, AccessKeyId: `id-${requests}` };
  } });
  assert.equal((await provider()).accessKeyId, 'id-1');
  assert.equal((await provider()).accessKeyId, 'id-2');
  await assert.rejects(fromCredentialsURI('aws', { env, fetch: async () => { throw new Error('HTTP 401'); } })(), /401/);
});

test('resource requests use STS triples, secure endpoints and the AWS China region', async () => {
  const provider = async () => normalize('aws', fixtures.aws);
  let destroyed = 0;
  class S3Client {
    constructor(options) { assert.equal(options.region, 'cn-north-1'); assert.equal(options.credentials, provider); }
    async send(command, options) { assert.equal(command.MaxBuckets, 5); assert.ok(options.abortSignal); return { Buckets: [{ Name: 's3-bucket' }] }; }
    destroy() { destroyed++; }
  }
  class ListBucketsCommand { constructor(input) { Object.assign(this, input); } }
  class OSS {
    constructor(options) { assert.equal(options.stsToken, 'test-token'); assert.equal(options.accessKeySecret, 'test-secret'); assert.equal(options.secure, true); }
    async listBuckets(options) { assert.equal(options['max-keys'], 5); return { buckets: [{ name: 'oss-bucket', region: 'cn-hangzhou' }] }; }
  }
  class COS {
    constructor(options) { assert.equal(options.SecurityToken, 'test-token'); assert.equal(options.SecretKey, 'test-secret'); assert.equal(options.Protocol, 'https:'); }
    getService(options, callback) { callback(null, { Buckets: [{ Name: 'cos-bucket', Location: 'ap-guangzhou' }] }); }
  }
  const load = name => ({ '@aws-sdk/client-s3': { S3Client, ListBucketsCommand }, 'ali-oss': OSS, 'cos-nodejs-sdk-v5': COS })[name];
  for (const [cloud, name] of [['aws', 's3-bucket'], ['alibaba', 'oss-bucket'], ['tencent', 'cos-bucket']]) {
    assert.equal((await resources(cloud, provider, load))[0].name, name);
  }
  assert.equal(destroyed, 1);
  S3Client.prototype.send = async () => { throw new Error('AccessDenied'); };
  await assert.rejects(resources('aws', provider, load), /AccessDenied/);
  assert.equal(destroyed, 2);
  COS.prototype.getService = (options, callback) => callback(new Error('Forbidden'));
  await assert.rejects(resources('tencent', provider, load), /Forbidden/);
  await assert.rejects(resources('other', provider, load), /unsupported/);
});

test('matrix separates credential acquisition from resource permission failure and omits SDK error details', async () => {
  const logs = [];
  const provider = async () => normalize('aws', fixtures.aws);
  const result = await main({ fromURI: () => provider, nativeAWS: () => provider,
    nativeAlibaba: () => async () => { throw new Error('get sts token failed, httpStatus: 401, message = omitted.'); },
    query: async cloud => { if (cloud === 'tencent') throw Object.assign(new Error('test-secret'), { code: 'AccessDenied', statusCode: 403, request: fixtures }); return [{ name: 'private-bucket-name' }]; },
    log: line => logs.push(line), nativeAlibabaOnly: false });
  assert.equal(result.length, 9);
  assert.equal(result.filter(r => r.ok).length, 8);
  assert.deepEqual(result.find(r => !r.ok), { cloud: 'tencent', mode: 'authenticated-uri-adapter-resources', ok: false, code: 'AccessDenied', status: 403 });
  assert.ok(!logs.join('').includes('test-secret'));
  assert.ok(!logs.join('').includes('private-bucket-name'));
  assert.equal(result.find(r => r.mode === 'authenticated-uri-adapter-resources' && r.ok).bucketCount, 1);
  assert.equal(result.at(-1).expectedRejection, 401);
});

test('native Alibaba positive matrix and unexpected authentication bypass are distinct outcomes', async () => {
  const provider = async () => normalize('aws', fixtures.aws);
  const options = { fromURI: () => provider, nativeAWS: () => provider, nativeAlibaba: () => provider,
    query: async () => [], log: () => {}, nativeAlibabaOnly: true };
  const positive = await main(options);
  assert.equal(positive.length, 2); assert.ok(positive.every(r => r.ok));
  const unexpected = await main({ ...options, nativeAlibabaOnly: false });
  assert.equal(unexpected.at(-1).ok, false);
  const upstreamFailure = await main({ ...options, nativeAlibabaOnly: false,
    nativeAlibaba: () => async () => { throw Object.assign(new Error('test-secret'), { code: 'untrusted secret' }); } });
  assert.equal(upstreamFailure.at(-1).code, 'Error');
});

test('all clouds reject absent fields, wrong types, malformed dates and the exact remaining-lifetime boundary', () => {
  const now = Date.parse('2030-01-01T00:00:00Z');
  for (const cloud of Object.keys(fixtures)) {
    const field = cloud === 'tencent' ? 'expiration' : 'Expiration';
    const withExpiry = milliseconds => ({ ...fixtures[cloud], [field]: new Date(milliseconds).toISOString(),
      ...(cloud === 'tencent' ? { expiredTime: Math.floor(milliseconds / 1000) } : {}) });
    for (const payload of [null, undefined, 'not-an-object', {}, { ...fixtures[cloud], [field]: 'not-a-date' },
      { ...fixtures[cloud], [field]: now + 3600000 }, withExpiry(now - 1000), withExpiry(now + 60000)]) {
      assert.throws(() => normalize(cloud, payload, now), /invalid/);
    }
    assert.equal(normalize(cloud, withExpiry(now + 60001), now).expiration.getTime(), now + 60001);
    for (const field of cloud === 'alibaba' ? ['AccessKeyId', 'AccessKeySecret', 'SecurityToken']
      : cloud === 'aws' ? ['AccessKeyId', 'SecretAccessKey', 'Token'] : ['tmpSecretId', 'tmpSecretKey', 'token']) {
      for (const value of [undefined, null, '', ' ', 123]) {
        assert.throws(() => normalize(cloud, { ...withExpiry(now + 3600000), [field]: value }, now), /invalid/);
      }
    }
  }
});

test('Tencent absolute expiry is required, numeric and consistent at Unix-second precision', () => {
  const now = Date.parse('2030-01-01T00:00:00Z');
  const expiration = '2030-01-01T09:00:00.123+08:00';
  const seconds = Math.floor(Date.parse(expiration) / 1000);
  for (const expiredTime of [undefined, null, 0, String(seconds), seconds + 1, seconds + 0.5]) {
    assert.throws(() => normalize('tencent', { ...fixtures.tencent, expiration, expiredTime }, now), /invalid/);
  }
  assert.equal(normalize('tencent', { ...fixtures.tencent, expiration, expiredTime: seconds }, now)
    .expiration.toISOString(), '2030-01-01T01:00:00.123Z');
  for (const Code of [undefined, null, '', 'Failed']) assert.throws(() => normalize('alibaba', { ...fixtures.alibaba, Code }), /invalid/);
});

test('native credential validation refuses missing triples and non-Date or invalid expiration', () => {
  const value = { accessKeyId: 'FAKE_ID', secretAccessKey: 'FAKE_SECRET', sessionToken: 'FAKE_TOKEN', expiration: new Date(expiration) };
  assert.equal(validateCredential(value), value);
  for (const c of [null, {}, { ...value, expiration: expiration }, { ...value, expiration: new Date('invalid') },
    { ...value, sessionToken: undefined }, { ...value, secretAccessKey: ' ' }]) assert.throws(() => validateCredential(c), /invalid/);
});

test('role query encoding replaces preexisting parameters and invalid roles fail before fetching', () => {
  for (const cloud of Object.keys(ROLE_ENV_VARS)) {
    const role = 'external/id+符号?x=1&y=2#part';
    const config = { ...env, [ROLE_ENV_VARS[cloud]]: role, IDAAS_AKLESS_CREDENTIALS_URI: 'http://127.0.0.1:8081/credentials?format=raw&roleArn=old' };
    const uri = credentialsURI(cloud, undefined, config);
    assert.equal(uri.port, '8081');
    assert.deepEqual([...uri.searchParams], [['roleArn', role]]);
    assert.equal(credentialsURI(cloud, 'http://127.0.0.1:8082/credentials', config).port, '8082');
    let requests = 0;
    assert.throws(() => fromCredentialsURI(cloud, { env: {}, fetch: async () => { requests++; } }), /authorized role/);
    assert.equal(requests, 0);
  }
});

test('each cloud adapter replaces all credential fields and propagates failures on later resolutions', async () => {
  for (const cloud of Object.keys(fixtures)) {
    let next = fixtures[cloud];
    const seen = [];
    const provider = fromCredentialsURI(cloud, { env, fetch: async uri => {
      seen.push(uri.searchParams.get('roleArn'));
      if (next instanceof Error) throw next;
      return next;
    } });
    const initial = await provider();
    next = JSON.parse(JSON.stringify(fixtures[cloud]).replaceAll('test-id', 'next-id')
      .replaceAll('test-secret', 'next-secret').replaceAll('test-token', 'next-token'));
    assert.deepEqual(await provider(), { accessKeyId: 'next-id', secretAccessKey: 'next-secret', sessionToken: 'next-token', expiration: new Date(expiration) });
    assert.equal(initial.accessKeyId, 'test-id');
    next = new Error('HTTP 503');
    await assert.rejects(provider(), /HTTP 503/);
    assert.deepEqual(seen, Array(3).fill(env[ROLE_ENV_VARS[cloud]]));
  }
});

test('each cloud resource query accepts empty lists and credential failures prevent product calls', async () => {
  const credential = normalize('aws', fixtures.aws);
  let destroyed = 0;
  class S3Client {
    constructor(options) { this.provider = options.credentials; assert.equal(options.maxAttempts, 1); }
    async send() { assert.equal(await this.provider(), credential); return {}; }
    destroy() { destroyed++; }
  }
  class OSS { async listBuckets() { return {}; } }
  class COS { getService(_, callback) { callback(null, {}); } }
  const load = name => ({ '@aws-sdk/client-s3': { S3Client, ListBucketsCommand: class {} }, 'ali-oss': OSS, 'cos-nodejs-sdk-v5': COS })[name];
  for (const cloud of ['aws', 'alibaba', 'tencent']) {
    assert.deepEqual(await resources(cloud, async () => credential, load), []);
    let productCalls = 0;
    const blockedLoad = name => {
      assert.equal(name, '@aws-sdk/client-s3');
      return { S3Client: class {
        constructor(options) { this.provider = options.credentials; }
        async send() { await this.provider(); productCalls++; }
        destroy() { destroyed++; }
      }, ListBucketsCommand: class {} };
    };
    await assert.rejects(resources(cloud, async () => { throw new Error('HTTP 401'); }, blockedLoad), /401/);
    assert.equal(productCalls, 0);
  }
  assert.equal(destroyed, 2);
  const denied = new Error('OSS forbidden');
  await assert.rejects(resources('alibaba', async () => credential, () => class {
    async listBuckets() { throw denied; }
  }), error => error === denied);
});

test('matrix records every cloud failure, continues other cases, and never serializes arbitrary error fields', async () => {
  const logs = [];
  const provider = async () => normalize('aws', fixtures.aws);
  const result = await main({ fromURI: cloud => async () => {
    if (cloud === 'alibaba') throw null;
    if (cloud === 'aws') throw Object.assign(new Error('FAKE_SECRET'), { name: 'untrusted error', statusCode: { secret: 'FAKE_TOKEN' } });
    throw Object.assign(new Error('FAKE_SECRET'), { code: 'AccessDenied', $metadata: { httpStatusCode: 403 } });
  }, query: async (_, getCredential) => { await getCredential(); return []; }, nativeAWS: () => provider,
  nativeAlibaba: () => async () => { throw new Error('get sts token failed, httpStatus: 401, message = omitted.'); },
  nativeAlibabaOnly: false, log: line => logs.push(JSON.parse(line)) });
  assert.equal(result.length, 9);
  assert.equal(result.filter(r => !r.ok).length, 6);
  assert.deepEqual(logs.at(-1), { summary: true, passed: 3, total: 9 });
  assert.equal(result.find(r => r.cloud === 'alibaba').code, 'Error');
  assert.equal(result.find(r => r.cloud === 'aws' && !r.ok).status, undefined);
  assert.equal(result.find(r => r.cloud === 'tencent').status, 403);
  assert.ok(!JSON.stringify(logs).includes('FAKE_SECRET'));
  assert.ok(!JSON.stringify(logs).includes('FAKE_TOKEN'));
});

test('CLI missing configuration exits nonzero with complete failure summaries in both modes', () => {
  const { spawnSync } = require('node:child_process');
  const isolatedEnv = { ...process.env };
  for (const name of Object.values(ROLE_ENV_VARS)) delete isolatedEnv[name];
  for (const [args, total] of [[[], 9], [['--native-alibaba-uri'], 2]]) {
    const result = spawnSync(process.execPath, [require.resolve('./integration.cjs'), ...args], {
      env: isolatedEnv, encoding: 'utf8', timeout: 10000,
    });
    assert.equal(result.error, undefined);
    assert.equal(result.status, 1);
    const lines = result.stdout.trim().split('\n').map(line => JSON.parse(line));
    assert.equal(lines.length, total + 1);
    assert.ok(lines.slice(0, -1).every(r => r.ok === false));
    assert.deepEqual(lines.at(-1), { summary: true, passed: 0, total });
  }
});
