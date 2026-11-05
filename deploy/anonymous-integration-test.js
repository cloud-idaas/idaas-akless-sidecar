'use strict';
// Run only in an isolated auth-disabled test Pod with no local authentication token mount:
// kubectl exec -i <pod> -c business -- node < deploy/anonymous-integration-test.js
// Real STS values stay in memory; output contains check names only.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const { fetchJson, configuredCredentialsURI } = require('/app/credentials-client');
const url = new URL(configuredCredentialsURI(process.env));
let passed = 0;

function request(query = '', method = 'GET', authorization) {
  const target = new URL(url);
  target.search = query;
  const headers = authorization === undefined ? {} : { Authorization: authorization };
  return new Promise((resolve, reject) => {
    const req = http.request(target, { method, headers, agent: false }, res => {
      const chunks = [];
      let size = 0;
      res.on('data', chunk => {
        size += chunk.length;
        if (size > 1024 * 1024) res.destroy(new Error('response too large'));
        else chunks.push(chunk);
      });
      res.on('error', reject);
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks).toString() }));
    });
    req.setTimeout(15000, () => req.destroy(new Error('request timed out')));
    req.on('error', reject);
    req.end();
  });
}

async function check(name, fn) {
  try { await fn(); passed++; console.log(`PASS ${name}`); }
  catch { throw new Error(`FAIL ${name}`); }
}

async function rejected(name, query, method, status) {
  await check(name, async () => {
    const r = await request(query, method);
    assert.equal(r.status, status);
    assert.equal(r.headers['cache-control'], 'no-store');
    const error = JSON.parse(r.body);
    assert.equal(typeof error.code, 'string');
    assert.equal(error.AccessKeySecret, undefined);
    assert.equal(error.cloudAccountRoleAccessCredential, undefined);
  });
}

async function main() {
  await check('business explicitly disables local authentication', () => {
    assert.equal(process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED, 'false');
  });
  await check('local authentication token is not mounted', () => {
    assert.throws(() => fs.accessSync('/run/secrets/idaas-akless/credential-auth/token'));
  });
  await check('IDaaS bootstrap token is not mounted in business', () => {
    assert.throws(() => fs.accessSync('/run/secrets/oidc-federated/token'));
  });
  let credential;
  await check('business client retrieves real STS without a token file', async () => {
    credential = await fetchJson(url, 15000);
    assert.equal(credential.Code, 'Success');
    for (const key of ['AccessKeyId', 'AccessKeySecret', 'SecurityToken']) {
      assert.ok(typeof credential[key] === 'string' && credential[key].length > 0);
    }
    assert.ok(Date.parse(credential.Expiration) > Date.now());
  });
  await check('anonymous HTTP receives the cached credential', async () => {
    const r = await request();
    assert.equal(r.status, 200);
    assert.equal(r.headers['cache-control'], 'no-store');
    assert.deepEqual(JSON.parse(r.body), credential);
  });
  await check('anonymous raw output retains credential and expiry validation', async () => {
    const r = await request('?format=raw');
    assert.equal(r.status, 200);
    const body = JSON.parse(r.body);
    assert.equal(body.cloudAccountVendorType, 'alibaba_cloud');
    const c = body.cloudAccountRoleAccessCredential;
    assert.equal(c.alibabaCloudStsToken.accessKeySecret, credential.AccessKeySecret);
    assert.ok(c.accessCredentialExpiresAt * 1000 > Date.now());
  });
  await check('disabled mode does not validate supplied Authorization', async () => {
    assert.equal((await request('', 'GET', 'not-a-token')).status, 200);
  });
  await rejected('POST remains rejected', '', 'POST', 405);
  await rejected('invalid format remains rejected', '?format=invalid', 'GET', 400);
  await rejected('duplicate role remains rejected', '?roleArn=a&roleArn=b', 'GET', 400);
  await rejected('invalid role remains rejected', '?roleArn=invalid%20role', 'GET', 400);
  await rejected('role allowlist still rejects another role', '?roleArn=unapproved-integration-role', 'GET', 403);
  await rejected('raw mode cannot bypass the role allowlist', '?roleArn=unapproved-integration-role&format=raw', 'GET', 403);
  console.log(`RESULT ${passed} anonymous-mode integration checks passed; secrets omitted`);
}

main().catch(error => { console.error(error.message); process.exitCode = 1; });
