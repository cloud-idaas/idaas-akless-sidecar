'use strict';
const http = require('node:http');
const fs = require('node:fs/promises');
const { constants } = require('node:fs');
const path = require('node:path');

const DEFAULT_TOKEN_FILE = '/run/secrets/idaas-akless/credential-auth/token';
const MAX_TOKEN_BYTES = 8192;
const MAX_RESPONSE_BYTES = 1024 * 1024;
// A dedicated agent avoids the global agent's opt-in environment proxy support.
const directAgent = new http.Agent({ keepAlive: false, proxyEnv: {} });

// Read the optional role override from the business example's environment.
function configuredRoleArn(env) {
  return env.IDAAS_AKLESS_CLOUD_ROLE_ARN || '';
}

// Explicit URI > shared port override > the Sidecar's default loopback port.
function configuredCredentialsURI(env = process.env) {
  if (env.IDAAS_AKLESS_CREDENTIALS_URI) return env.IDAAS_AKLESS_CREDENTIALS_URI;
  const value = env.IDAAS_AKLESS_SIDECAR_LISTEN_PORT || '61765';
  const port = Number(value);
  if (!/^\+?[0-9]+$/.test(value) || !Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error('IDAAS_AKLESS_SIDECAR_LISTEN_PORT must be an integer from 1 to 65535');
  }
  return `http://127.0.0.1:${port}/credentials`;
}

function parseTimeoutMs(value = '15000') {
  const timeout = Number(value);
  if (!Number.isSafeInteger(timeout) || timeout <= 0 || timeout > 2147483647) {
    throw new Error('IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS must be a positive integer in milliseconds (max 2147483647)');
  }
  return timeout;
}

function parseAuthEnabled(value = 'true') {
  if (value === '') return true; // Empty environment values follow the server's default.
  if (value !== 'true' && value !== 'false') {
    throw new Error('IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED must be true or false');
  }
  return value === 'true';
}

function credentialURL(value) {
  const url = new URL(value);
  if (url.protocol !== 'http:' || !['127.0.0.1', '[::1]'].includes(url.hostname)
      || url.pathname !== '/credentials' || url.username || url.password || url.hash) {
    throw new Error('credential URI must be a numeric loopback /credentials endpoint');
  }
  return url;
}

// fsGroup projections can be root-owned 0640 despite defaultMode 0440.
// Accept root's owner-write only for a non-root reader; mounts remain read-only.
function safeTokenPermissions(mode, owner, euid) {
  const writes = mode & 0o222;
  return writes === 0 || (writes === 0o200 && owner === 0 && euid !== 0);
}

// Bound file type, size, write permissions and token shape before sending it.
// The Sidecar checks its own projected reference, root ownership and Pod binding;
// this client does not independently verify those server-side trust conditions.
async function readToken(file) {
  if (typeof file !== 'string' || !path.isAbsolute(file)) throw new Error('invalid credential token file path');
  let handle;
  try {
    handle = await fs.open(file, constants.O_RDONLY | constants.O_NONBLOCK);
    const stat = await handle.stat();
    if (!stat.isFile() || !safeTokenPermissions(stat.mode, stat.uid, process.geteuid()) || stat.size > MAX_TOKEN_BYTES) throw new Error();
    const buffer = Buffer.alloc(MAX_TOKEN_BYTES + 1);
    let size = 0;
    while (size < buffer.length) {
      const { bytesRead } = await handle.read(buffer, size, buffer.length - size, size);
      if (!bytesRead) break;
      size += bytesRead;
    }
    const token = buffer.subarray(0, size).toString('utf8');
    if (size > MAX_TOKEN_BYTES || !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(token)) throw new Error();
    return token;
  } catch {
    throw new Error('credential token file unavailable or invalid');
  } finally {
    if (handle) await handle.close();
  }
}

function requestJson(url, token, signal) {
  return new Promise((resolve, reject) => {
    const headers = { Accept: 'application/json' };
    if (token !== undefined) headers.Authorization = token;
    const req = http.get(url, {
      agent: directAgent, signal, headers
    }, res => {
      // node:http does not follow redirects. Never forward the token to Location.
      if (res.statusCode !== 200) {
        const error = new Error(`sidecar returned HTTP ${res.statusCode}`);
        error.statusCode = res.statusCode;
        reject(error);
        res.destroy();
        return;
      }
      let size = 0;
      const chunks = [];
      res.on('data', chunk => {
        size += chunk.length;
        if (size > MAX_RESPONSE_BYTES) {
          reject(new Error('sidecar response too large'));
          res.destroy();
          return;
        }
        chunks.push(chunk);
      });
      res.on('error', () => reject(new Error('sidecar response interrupted')));
      res.on('end', () => {
        try { resolve(JSON.parse(Buffer.concat(chunks).toString('utf8'))); }
        catch { reject(new Error('invalid sidecar JSON response')); }
      });
    });
    req.on('error', () => reject(new Error('sidecar request failed')));
  });
}

// HTTP attempts share one deadline, checked again after each token read.
// An in-progress filesystem operation cannot be interrupted by this signal.
async function fetchJson(value, timeoutMs,
  tokenFile = process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE || DEFAULT_TOKEN_FILE,
  authEnabled = parseAuthEnabled(process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED)) {
  if (typeof authEnabled !== 'boolean') throw new Error('credential auth enabled must be a boolean');
  const url = credentialURL(value);
  const signal = AbortSignal.timeout(parseTimeoutMs(String(timeoutMs)));
  try {
    for (let attempt = 0; attempt < 2; attempt++) {
      const token = authEnabled ? await readToken(tokenFile) : undefined;
      signal.throwIfAborted();
      try { return await requestJson(url, token, signal); }
      catch (error) {
        if (!authEnabled || error.statusCode !== 401 || attempt === 1) throw error;
      }
    }
  } catch (error) {
    if (signal.aborted) throw new Error('sidecar request deadline exceeded');
    throw error;
  }
}

module.exports = { configuredRoleArn, configuredCredentialsURI, parseTimeoutMs, parseAuthEnabled, fetchJson, safeTokenPermissions };
