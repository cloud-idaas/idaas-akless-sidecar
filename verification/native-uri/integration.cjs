'use strict';
const assert = require('node:assert/strict');
const http = require('node:http');
const { setTimeout: delay } = require('node:timers/promises');

// The live CLI run uses real SDKs, transport and time; unit tests inject dependencies.
async function run({ env, Credential, Sts, Config, snapshot, wait = delay, now = Date.now, log = console.log }) {
  const uri = new URL(env.ALIBABA_CLOUD_CREDENTIALS_URI);
  assert.equal(uri.protocol, 'http:');
  assert.equal(uri.hostname, '127.0.0.1');
  assert.equal(uri.pathname, '/credentials');
  assert.ok(!uri.username && !uri.password && !uri.hash);
  assert.ok(['explicit', 'default'].includes(env.SDK_URI_MODE));
  assert.ok(['enabled', 'disabled'].includes(env.SDK_AUTH_MODE));
  const role = /^acs:ram::([0-9]+):role\/(.+)$/.exec(env.EXPECTED_ROLE_ARN);
  assert.ok(role);
  const native = env.SDK_URI_MODE === 'explicit'
    ? new Credential({ type: 'credentials_uri', credentialsURI: uri.href }) : new Credential();
  if (env.SDK_AUTH_MODE === 'enabled') {
    // The SDK does not attach this project's local authorization token.
    await assert.rejects(() => native.getCredential(), error => {
      if (typeof error?.message !== 'string') return false;
      // The pinned default chain prepends errors from unavailable credential sources.
      let message = error.message;
      if (env.SDK_URI_MODE === 'default' && message.startsWith('unable to get credentials from any of the providers in the chain: ')) {
        const uriFailure = message.indexOf('get sts token failed, ');
        if (uriFailure < 0) return false;
        // Inspect the first URI failure, never a later status-like string in its body.
        message = message.slice(uriFailure);
      }
      return /^get sts token failed, httpStatus: 401(?:,|$)/.test(message);
    });
    log(`PASS ${env.SDK_URI_MODE}: native URI rejected by authentication-enabled Sidecar (401)`);
    return;
  }
  const first = await native.getCredential();
  for (const key of ['accessKeyId', 'accessKeySecret', 'securityToken']) assert.ok(first[key]);
  const initial = await snapshot(uri);
  assert.equal(initial.AccessKeyId, first.accessKeyId);
  assert.ok(Date.parse(initial.Expiration) > now());
  log(`PASS ${env.SDK_URI_MODE}: real STS retrieved through native URI provider`);
  const config = new Config({ credential: native, endpoint: 'sts.cn-hangzhou.aliyuncs.com', protocol: 'https' });
  const sts = new Sts(config);
  async function identity() {
    const { body } = await sts.getCallerIdentity();
    assert.equal(body.accountId, role[1]);
    assert.equal(body.identityType, 'AssumedRoleUser');
    assert.ok(body.arn.startsWith(`acs:ram::${role[1]}:assumed-role/${role[2]}/`));
  }
  await identity();
  log(`PASS ${env.SDK_URI_MODE}: product SDK GetCallerIdentity matches expected account and role`);
  const cached = await native.getCredential();
  assert.equal(cached.accessKeyId, first.accessKeyId);
  log(`PASS ${env.SDK_URI_MODE}: same provider instance reuses valid credentials`);
  if (env.SDK_WAIT_FOR_REFRESH === 'true') {
    const deadline = Date.parse(initial.Expiration) + 120000;
    assert.ok(deadline - now() <= 7200000);
    let changed = false;
    while (now() < deadline) {
      await wait(5000);
      const current = await native.getCredential();
      if (current.accessKeyId !== first.accessKeyId || current.securityToken !== first.securityToken) {
        const refreshed = await snapshot(uri);
        assert.equal(refreshed.AccessKeyId, current.accessKeyId);
        assert.ok(Date.parse(refreshed.Expiration) > Date.parse(initial.Expiration));
        changed = true;
        break;
      }
    }
    assert.ok(changed, 'native provider did not refresh within the deadline');
    await identity();
    log(`PASS ${env.SDK_URI_MODE}: native provider automatically refreshed real STS; cloud identity verified again`);
  }
}

function snapshot(uri) {
  return new Promise((resolve, reject) => {
    const req = http.get(uri, { agent: false }, res => {
      if (res.statusCode !== 200) { res.resume(); reject(new Error('snapshot request failed')); return; }
      const chunks = [];
      let size = 0;
      res.on('data', chunk => {
        size += chunk.length;
        if (size > 1024 * 1024) res.destroy(new Error('snapshot too large'));
        else chunks.push(chunk);
      });
      res.on('error', reject);
      res.on('end', () => {
        try { resolve(JSON.parse(Buffer.concat(chunks))); } catch { reject(new Error('invalid snapshot')); }
      });
    });
    req.setTimeout(15000, () => req.destroy(new Error('snapshot timeout')));
    req.on('error', reject);
  });
}

if (require.main === module) {
  run({ env: process.env, Credential: require('@alicloud/credentials').default,
    Sts: require('@alicloud/sts20150401').default, Config: require('@alicloud/openapi-core').$OpenApiUtil.Config, snapshot })
    .then(() => console.log('RESULT native Credentials URI integration passed; secrets omitted'))
    .catch(() => { console.error('FAIL native Credentials URI integration; secrets omitted'); process.exitCode = 1; });
}
module.exports = { run, snapshot };
