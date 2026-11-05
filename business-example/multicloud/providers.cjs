'use strict';
const { fetchJson, configuredCredentialsURI } = require('../credentials-client');
// Role identities belong to the operator's environment, never the test source.
const ROLE_ENV_VARS = Object.freeze({
  alibaba: 'IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN',
  tencent: 'IDAAS_AKLESS_TEST_TENCENT_ROLE_ARN',
  aws: 'IDAAS_AKLESS_TEST_AWS_ROLE_ARN',
});

function credentialsURI(cloud, base, env = process.env) {
  if (!Object.hasOwn(ROLE_ENV_VARS, cloud)) throw new Error('unsupported cloud');
  const uri = new URL(base ?? configuredCredentialsURI(env));
  if (uri.protocol !== 'http:' || uri.hostname !== '127.0.0.1' || uri.username || uri.password || uri.hash
      || uri.pathname !== '/credentials') throw new Error('untrusted credentials URI');
  const role = env[ROLE_ENV_VARS[cloud]];
  if (typeof role !== 'string' || !role || /[<>\s\x00-\x1f\x7f]/.test(role)) {
    throw new Error(`set ${ROLE_ENV_VARS[cloud]} to an authorized role identifier`);
  }
  uri.search = '';
  uri.searchParams.set('roleArn', role);
  return uri;
}

function normalize(cloud, value, now = Date.now()) {
  if (!value || typeof value !== 'object') throw new Error('invalid credential response');
  if (cloud === 'alibaba' && value.Code !== 'Success') throw new Error('invalid Alibaba credential response code');
  const expiry = cloud === 'tencent' ? value.expiration : value.Expiration;
  let result;
  if (cloud === 'alibaba') result = { accessKeyId: value.AccessKeyId, secretAccessKey: value.AccessKeySecret,
    sessionToken: value.SecurityToken, expiration: new Date(value.Expiration) };
  else if (cloud === 'aws') result = { accessKeyId: value.AccessKeyId, secretAccessKey: value.SecretAccessKey,
    sessionToken: value.Token, expiration: new Date(value.Expiration) };
  else if (cloud === 'tencent') result = { accessKeyId: value.tmpSecretId, secretAccessKey: value.tmpSecretKey,
    sessionToken: value.token, expiration: new Date(value.expiration) };
  else throw new Error('unsupported cloud');
  if (typeof expiry !== 'string') throw new Error('invalid credential expiration');
  if (cloud === 'tencent' && (!Number.isSafeInteger(value.expiredTime)
      || value.expiredTime !== Math.floor(result.expiration.getTime() / 1000))) {
    throw new Error('invalid or inconsistent Tencent credential expiry');
  }
  return validateCredential(result, now);
}

// Apply the same complete-triple and remaining-lifetime gate to adapters and AWS's SDK provider.
function validateCredential(value, now = Date.now()) {
  if (!value || ![value.accessKeyId, value.secretAccessKey, value.sessionToken]
    .every(v => typeof v === 'string' && v.trim()) || !(value.expiration instanceof Date)
      || !(value.expiration.getTime() > now + 60000)) throw new Error('invalid or near-expiry credential');
  return value;
}

function fromCredentialsURI(cloud, { base, fetch = fetchJson, env } = {}) {
  const uri = credentialsURI(cloud, base, env);
  // Reread the local authentication file on every fetch; never cache a static STS forever.
  return async () => normalize(cloud, await fetch(uri, 15000));
}

module.exports = { ROLE_ENV_VARS, credentialsURI, normalize, validateCredential, fromCredentialsURI };
