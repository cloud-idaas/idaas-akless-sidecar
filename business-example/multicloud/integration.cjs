'use strict';
const { isAbsolute } = require('node:path');
const { ROLE_ENV_VARS, credentialsURI, validateCredential, fromCredentialsURI } = require('./providers.cjs');

async function resources(cloud, provider, load = require) {
  if (cloud === 'aws') {
    const { S3Client, ListBucketsCommand } = load('@aws-sdk/client-s3');
    const client = new S3Client({ region: 'cn-north-1', credentials: provider, maxAttempts: 1 });
    try {
      const r = await client.send(new ListBucketsCommand({ MaxBuckets: 5 }), { abortSignal: AbortSignal.timeout(15000) });
      return (r.Buckets || []).map(b => ({ name: b.Name, created: b.CreationDate }));
    } finally { client.destroy(); }
  }
  const c = await provider();
  if (cloud === 'alibaba') {
    const OSS = load('ali-oss');
    const client = new OSS({ region: 'oss-cn-hangzhou', secure: true, timeout: 15000,
      accessKeyId: c.accessKeyId, accessKeySecret: c.secretAccessKey, stsToken: c.sessionToken });
    const r = await client.listBuckets({ 'max-keys': 5 });
    return (r.buckets || []).map(b => ({ name: b.name, region: b.region, created: b.creationDate }));
  }
  if (cloud !== 'tencent') throw new Error('unsupported cloud');
  const COS = load('cos-nodejs-sdk-v5');
  const client = new COS({ SecretId: c.accessKeyId, SecretKey: c.secretAccessKey,
    SecurityToken: c.sessionToken, Protocol: 'https:', Timeout: 15000 });
  const r = await new Promise((resolve, reject) => client.getService({}, (err, data) => err ? reject(err) : resolve(data)));
  return (r.Buckets || []).slice(0, 5).map(b => ({ name: b.Name, region: b.Location, created: b.CreationDate }));
}

function awsCredentialsURI() {
  const { fromHttp } = require('@aws-sdk/credential-providers');
  const tokenFile = process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE || '/run/secrets/idaas-akless/credential-auth/token';
  if (!isAbsolute(tokenFile)) throw new Error('credential authentication token file must be absolute');
  const provider = fromHttp({ awsContainerCredentialsFullUri: credentialsURI('aws').href,
    // Explicit empty values suppress SDK environment fallbacks and metadata precedence.
    awsContainerCredentialsRelativeUri: '', awsContainerAuthorizationToken: '',
    awsContainerAuthorizationTokenFile: tokenFile, timeout: 15000, maxRetries: 0 });
  return async () => validateCredential(await provider());
}

function alibabaCredentialsURI() {
  const Credential = require('@alicloud/credentials').default;
  const client = new Credential({ type: 'credentials_uri', credentialsURI: credentialsURI('alibaba').href });
  return async () => {
    const c = await client.getCredential();
    if (![c.accessKeyId, c.accessKeySecret, c.securityToken].every(v => typeof v === 'string' && v.trim())) {
      throw new Error('incomplete native credential');
    }
    return { accessKeyId: c.accessKeyId, secretAccessKey: c.accessKeySecret, sessionToken: c.securityToken };
  };
}

async function main({ fromURI = fromCredentialsURI, query = resources,
  nativeAWS = awsCredentialsURI, nativeAlibaba = alibabaCredentialsURI,
  log = console.log, nativeAlibabaOnly = process.argv.includes('--native-alibaba-uri') } = {}) {
  const results = [];
  async function check(cloud, mode, action) {
    try {
      const detail = await action();
      results.push({ cloud, mode, ok: true, ...detail });
    } catch (error) {
      // SDK exceptions may retain request headers or credential triples. Never serialize them.
      const raw = String(error?.code || error?.name || 'Error');
      const code = /^[A-Za-z0-9_.-]{1,80}$/.test(raw) ? raw : 'Error';
      const status = error?.statusCode ?? error?.$metadata?.httpStatusCode;
      results.push({ cloud, mode, ok: false, code,
        ...(Number.isInteger(status) && status >= 100 && status <= 599 ? { status } : {}) });
    }
    log(JSON.stringify(results.at(-1)));
  }
  for (const cloud of nativeAlibabaOnly ? [] : Object.keys(ROLE_ENV_VARS)) {
    await check(cloud, 'authenticated-http-sts', async () => {
      const c = await fromURI(cloud)();
      return { expiresAt: c.expiration.toISOString() };
    });
    await check(cloud, 'authenticated-uri-adapter-resources', async () => ({ bucketCount: (await query(cloud, fromURI(cloud))).length }));
  }
  if (!nativeAlibabaOnly) {
    await check('aws', 'native-credentials-uri-sts', async () => {
      const c = await nativeAWS()();
      if (!c.sessionToken) throw new Error('missing native session token');
      return { expiresAt: c.expiration.toISOString() };
    });
    await check('aws', 'native-credentials-uri-resources', async () => ({ bucketCount: (await query('aws', nativeAWS())).length }));
  }
  await check('alibaba', nativeAlibabaOnly ? 'native-credentials-uri-sts' : 'native-uri-authentication-boundary', async () => {
    if (nativeAlibabaOnly) { await nativeAlibaba()(); return {}; }
    try { await nativeAlibaba()(); }
    // Match the pinned SDK's status prefix, never status-like text inside the upstream body.
    catch (e) { if (/^get sts token failed, httpStatus: 401(?:,|$)/.test(e?.message)) return { expectedRejection: 401 }; throw e; }
    throw new Error('native provider unexpectedly bypassed local authentication');
  });
  if (nativeAlibabaOnly) {
    await check('alibaba', 'native-credentials-uri-resources', async () => ({ bucketCount: (await query('alibaba', nativeAlibaba())).length }));
  }
  log(JSON.stringify({ summary: true, passed: results.filter(r => r.ok).length, total: results.length }));
  return results;
}
if (require.main === module) main().then(results => { process.exitCode = results.some(r => !r.ok) ? 1 : 0; })
  .catch(() => { console.error('integration failed; details omitted'); process.exitCode = 1; });
module.exports = { resources, main, awsCredentialsURI, alibabaCredentialsURI };
