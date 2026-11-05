'use strict';
// Run only in an isolated test Pod with a 600s credential-auth projection.
// Keeps old/current tokens in memory; never logs tokens or response bodies.
const fs = require('node:fs');
const http = require('node:http');
const assert = require('node:assert/strict');
const { setTimeout: delay } = require('node:timers/promises');
const { configuredCredentialsURI } = require('/app/credentials-client');
const file = '/run/secrets/idaas-akless/credential-auth/token';
const endpoint = configuredCredentialsURI(process.env);

function status(token) {
  return new Promise((resolve, reject) => {
    const req = http.get(endpoint, { headers: { Authorization: token }, agent: false }, res => {
      res.resume();
      res.on('error', reject);
      res.on('end', () => resolve(res.statusCode));
    });
    req.setTimeout(15000, () => req.destroy(new Error('request timed out')));
    req.on('error', reject);
  });
}

// Decode only to schedule test assertions; authentication remains the Sidecar's job.
function tokenTimes(token) {
  const claims = JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString('utf8'));
  assert.ok(Number.isFinite(claims.iat) && Number.isFinite(claims.exp), 'token timestamps missing');
  return { issuedAt: claims.iat * 1000, expiresAt: claims.exp * 1000 };
}

async function main() {
  const previous = fs.readFileSync(file, 'utf8');
  assert.equal(await status(previous), 200);
  console.log('PASS initial projected token retrieves STS; waiting for kubelet rotation');
  const deadline = Date.now() + 11 * 60 * 1000;
  let current;
  while (Date.now() < deadline) {
    await delay(2000);
    current = fs.readFileSync(file, 'utf8');
    if (current !== previous) break;
  }
  assert.ok(current && current !== previous, 'kubelet rotation not observed within deadline');
  assert.equal(await status(current), 200);
  console.log('PASS kubelet replaced projected token; new token accepted without restart');
  const oldTimes = tokenTimes(previous);
  const newTimes = tokenTimes(current);
  const oldUntil = Math.min(oldTimes.expiresAt, oldTimes.issuedAt + 3600000);
  // Match the server's 60s grace, 1h maximum age and allowed 30s issuance skew.
  // The exact first observation is private to the server, so use conservative bounds.
  const earliestGraceEnd = Math.min(newTimes.issuedAt + 30000, oldUntil);
  const latestGraceEnd = Math.min(newTimes.issuedAt + 60000, oldUntil);
  const requestStarted = Date.now();
  const oldStatus = await status(previous);
  if (requestStarted >= latestGraceEnd) {
    assert.equal(oldStatus, 401);
    console.log('PASS previous token already outside its grace period');
  } else if (Date.now() < earliestGraceEnd) {
    assert.equal(oldStatus, 200);
    console.log('PASS previous token accepted within its guaranteed grace period');
  } else {
    // Observation time and requests crossing the cutoff may legitimately yield either.
    assert.ok(oldStatus === 200 || oldStatus === 401, 'unexpected previous token status');
    console.log('PASS previous token follows the bounded grace transition');
  }
  await delay(Math.max(0, latestGraceEnd - Date.now()) + 5000);
  assert.equal(await status(previous), 401);
  assert.equal(await status(current), 200);
  console.log('PASS previous token rejected after grace; current token still accepted');
  console.log('RESULT real kubelet rotation integration passed; secrets omitted');
}

main().catch(() => { console.error('FAIL real kubelet rotation integration'); process.exitCode = 1; });
