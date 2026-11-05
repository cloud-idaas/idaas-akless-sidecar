'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { fromCredentialsURI } = require('./providers.cjs');
const { resources } = require('./integration.cjs');

const config = {
  IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN: 'acs:ram::1234567890123456:role/example',
  IDAAS_AKLESS_TEST_AWS_ROLE_ARN: 'arn:aws-cn:iam::123456789012:role/example',
  IDAAS_AKLESS_TEST_TENCENT_ROLE_ARN: 'qcs::cam::uin/123456789012:roleName/example',
};
const roleKey = { alibaba: 'IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN', aws: 'IDAAS_AKLESS_TEST_AWS_ROLE_ARN',
  tencent: 'IDAAS_AKLESS_TEST_TENCENT_ROLE_ARN' };

function payload(cloud, generation) {
  const expiration = new Date(Date.now() + 3600000).toISOString();
  const triple = [`fake-${generation}-id`, `fake-${generation}-secret`, `fake-${generation}-token`];
  if (cloud === 'alibaba') return { Code: 'Success', AccessKeyId: triple[0], AccessKeySecret: triple[1], SecurityToken: triple[2], Expiration: expiration };
  if (cloud === 'aws') return { AccessKeyId: triple[0], SecretAccessKey: triple[1], Token: triple[2], Expiration: expiration };
  return { tmpSecretId: triple[0], tmpSecretKey: triple[1], token: triple[2], expiration, expiredTime: Math.floor(Date.parse(expiration) / 1000) };
}

// This is a local component fixture, not a real IDaaS/Sidecar or cloud endpoint.
async function sidecarFixture(t, cloud) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'akless-multicloud-'));
  t.after(() => fs.rm(directory, { force: true, recursive: true }));
  const file = path.join(directory, 'local-token');
  async function publish(token) {
    await fs.writeFile(file + '.next', token, { mode: 0o440 });
    await fs.rename(file + '.next', file);
  }
  await publish('old.local.signature');
  const saved = { enabled: process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED,
    file: process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE };
  process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED = 'true';
  process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE = file;
  t.after(() => {
    for (const [key, value] of [['IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED', saved.enabled],
      ['IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE', saved.file]]) {
      if (value === undefined) delete process.env[key]; else process.env[key] = value;
    }
  });
  const requests = [];
  const state = { generation: 1, status: 200, rotate: false };
  const server = http.createServer(async (req, res) => {
    requests.push({ token: req.headers.authorization, uri: new URL(req.url, 'http://127.0.0.1') });
    try {
      if (state.rotate) {
        state.rotate = false;
        await publish('new.local.signature');
        res.writeHead(401); res.end(); return;
      }
      res.writeHead(state.status, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(state.status === 200 ? payload(cloud, state.generation) : { message: 'fake-upstream-secret' }));
    } catch { res.writeHead(500); res.end(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); });
  return { state, requests, publish, provider: fromCredentialsURI(cloud, { env: { ...config,
    IDAAS_AKLESS_CREDENTIALS_URI: `http://127.0.0.1:${server.address().port}/credentials` } }) };
}

for (const cloud of ['alibaba', 'aws', 'tencent']) {
  test(`${cloud}: HTTP adapter rereads rotated auth and passes each complete STS generation to the product SDK boundary`, async t => {
    const f = await sidecarFixture(t, cloud);
    f.state.rotate = true;
    const received = [];
    class S3Client {
      constructor(options) { this.provider = options.credentials; }
      async send() { const c = await this.provider(); received.push([c.accessKeyId, c.secretAccessKey, c.sessionToken]); return { Buckets: [] }; }
      destroy() {}
    }
    class OSS {
      constructor(c) { received.push([c.accessKeyId, c.accessKeySecret, c.stsToken]); }
      async listBuckets() { return { buckets: [] }; }
    }
    class COS {
      constructor(c) { received.push([c.SecretId, c.SecretKey, c.SecurityToken]); }
      getService(_, callback) { callback(null, { Buckets: [] }); }
    }
    const load = name => ({ '@aws-sdk/client-s3': { S3Client, ListBucketsCommand: class {} }, 'ali-oss': OSS, 'cos-nodejs-sdk-v5': COS })[name];
    assert.deepEqual(await resources(cloud, f.provider, load), []);
    f.state.generation = 2;
    await f.publish('third.local.signature');
    assert.deepEqual(await resources(cloud, f.provider, load), []);
    assert.deepEqual(received, [['fake-1-id', 'fake-1-secret', 'fake-1-token'], ['fake-2-id', 'fake-2-secret', 'fake-2-token']]);
    assert.deepEqual(f.requests.map(r => r.token), ['old.local.signature', 'new.local.signature', 'third.local.signature']);
    assert.ok(f.requests.every(r => r.uri.pathname === '/credentials' && r.uri.searchParams.get('roleArn') === config[roleKey[cloud]]));
  });

  test(`${cloud}: actual HTTP failures remain failures and expose no upstream body`, async t => {
    const f = await sidecarFixture(t, cloud);
    for (const status of [401, 403, 502]) {
      f.state.status = status;
      const before = f.requests.length;
      await assert.rejects(f.provider(), error => {
        assert.equal(error.message, `sidecar returned HTTP ${status}`);
        assert.ok(!error.message.includes('fake-upstream-secret'));
        return true;
      });
      assert.equal(f.requests.length - before, status === 401 ? 2 : 1);
    }
    assert.ok(f.requests.every(r => r.token === 'old.local.signature'));
  });
}
