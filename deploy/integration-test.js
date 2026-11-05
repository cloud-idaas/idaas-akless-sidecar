'use strict';
// Run inside the business container using kubectl exec -i ... -- node < this-file.
// Credentials and tokens stay in memory and are never included in test output.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const { fetchJson, configuredCredentialsURI } = require('/app/credentials-client');

const tokenFile = '/run/secrets/idaas-akless/credential-auth/token';
const url = new URL(configuredCredentialsURI(process.env));
const token = fs.readFileSync(tokenFile, 'utf8');
let passed = 0;

function request(suffix = '', authorization, method = 'GET') {
  const target = new URL(url);
  target.search = suffix;
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

async function rejected(name, suffix, auth, status, method) {
  await check(name, async () => {
    const r = await request(suffix, auth, method);
    assert.equal(r.status, status);
    const body = JSON.parse(r.body);
    assert.equal(typeof body.code, 'string');
    assert.equal(body.AccessKeySecret, undefined);
    assert.equal(body.SecurityToken, undefined);
    assert.equal(body.cloudAccountRoleAccessCredential, undefined);
    assert.equal(r.body.includes(token), false);
    assert.equal(r.headers['cache-control'], 'no-store');
  });
}

async function main() {
  await check('business cannot write projected token', () => {
    let fd;
    try { fd = fs.openSync(tokenFile, 'r+'); }
    catch (e) { assert.ok(['EACCES', 'EROFS', 'EPERM'].includes(e.code)); return; }
    finally { if (fd !== undefined) fs.closeSync(fd); }
    throw new Error('token writable');
  });
  await check('IDaaS federation token is not mounted in business', () => {
    assert.throws(() => fs.accessSync('/run/secrets/oidc-federated/token'));
  });
  await rejected('anonymous credentials rejected', '', undefined, 401);
  await rejected('anonymous raw rejected', '?format=raw', undefined, 401);
  await rejected('unknown token rejected', '', 'a.b.c', 401);
  await rejected('Bearer prefix rejected', '', `Bearer ${token}`, 400);
  await rejected('duplicate Authorization rejected', '', [token, token], 400);
  await rejected('unsupported method rejected', '', undefined, 405, 'POST');
  await check('authenticated business client retrieves real STS', async () => {
    const c = await fetchJson(url, 15000);
    assert.equal(c.Code, 'Success');
    for (const key of ['AccessKeyId', 'AccessKeySecret', 'SecurityToken']) assert.ok(typeof c[key] === 'string' && c[key].length > 0);
    assert.ok(Date.parse(c.Expiration) > Date.now());
  });
  await check('authenticated cached credentials remain available', async () => {
    const r = await request('', token);
    assert.equal(r.status, 200);
    assert.equal(r.headers['cache-control'], 'no-store');
    assert.ok(JSON.parse(r.body).AccessKeySecret);
  });
  await rejected('warm cache does not bypass authentication', '', undefined, 401);
  await rejected('warm raw cache does not bypass authentication', '?format=raw', undefined, 401);
  await check('authenticated raw response retains credential contract', async () => {
    const r = await request('?format=raw', token);
    assert.equal(r.status, 200);
    const c = JSON.parse(r.body).cloudAccountRoleAccessCredential;
    assert.ok(c.alibabaCloudStsToken.accessKeySecret);
    assert.ok(c.accessCredentialExpiresAt * 1000 > Date.now());
  });
  await rejected('invalid format rejected', '?format=invalid', token, 400);
  await rejected('duplicate role rejected', '?roleArn=a&roleArn=b', token, 400);
  console.log(`RESULT ${passed} integration checks passed; secrets omitted`);
}

main().catch(error => { console.error(error.message); process.exitCode = 1; });
