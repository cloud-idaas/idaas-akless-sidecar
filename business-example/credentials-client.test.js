'use strict';
const { test, before, after } = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const { configuredRoleArn, configuredCredentialsURI, parseTimeoutMs, parseAuthEnabled, fetchJson: authenticatedFetch, safeTokenPermissions } = require('./credentials-client');

test('credential URI uses the default when the optional shared port is absent or empty', () => {
  for (const env of [{}, { IDAAS_AKLESS_SIDECAR_LISTEN_PORT: '' },
    { IDAAS_AKLESS_CREDENTIALS_URI: '' }]) {
    assert.equal(configuredCredentialsURI(env), 'http://127.0.0.1:61765/credentials');
  }
});

test('shared port overrides the URI default while an explicit URI takes precedence', () => {
  for (const port of ['61865', '8080', '1', '65535', '+61865', '061865']) {
    assert.equal(new URL(configuredCredentialsURI({ IDAAS_AKLESS_SIDECAR_LISTEN_PORT: port })).port,
      String(Number(port)));
  }
  assert.equal(configuredCredentialsURI({ IDAAS_AKLESS_CREDENTIALS_URI: 'http://127.0.0.1:8080/credentials',
    IDAAS_AKLESS_SIDECAR_LISTEN_PORT: 'invalid' }), 'http://127.0.0.1:8080/credentials');
});

test('invalid shared ports fail instead of silently selecting the default', () => {
  for (const port of ['0', '-1', '65536', 'invalid', '127.0.0.1:61865', ' 61865', '1.5', '6e4', '６１８６５']) {
    assert.throws(() => configuredCredentialsURI({ IDAAS_AKLESS_SIDECAR_LISTEN_PORT: port }), /must be an integer/);
  }
});

test('root-owned fsGroup projections are readable only by non-root; unsafe write permissions fail', () => {
  for (const [mode, owner, reader, expected] of [
    [0o440, 1000, 1000, true], [0o440, 0, 0, true],
    [0o640, 0, 1000, true], [0o640, 0, 10001, true],
    [0o640, 1000, 1000, false], [0o640, 1000, 10001, false],
    [0o640, 0, 0, false], [0o660, 0, 1000, false], [0o642, 0, 1000, false]
  ]) assert.equal(safeTokenPermissions(mode, owner, reader), expected);
});

let directory;
let defaultTokenFile;
before(async () => {
  directory = await fs.mkdtemp(path.join(os.tmpdir(), 'akless-client-'));
  defaultTokenFile = path.join(directory, 'token');
  await fs.writeFile(defaultTokenFile, 'a.b.c', { mode: 0o440 });
});
after(async () => fs.rm(directory, { recursive: true, force: true }));
const fetchJson = (url, timeout, file = defaultTokenFile) => authenticatedFetch(url, timeout, file, true);

async function publish(file, token) {
  await fs.writeFile(file + '.new', token, { mode: 0o440 });
  await fs.rename(file + '.new', file);
}

test('timeout configuration defaults, units, and invalid values', () => {
  assert.equal(parseTimeoutMs(), 15000);
  assert.equal(parseTimeoutMs('25000'), 25000);
  for (const value of ['', '10s', '0', '-1', '1.5', 'Infinity', '2147483648']) {
    assert.throws(() => parseTimeoutMs(value), /positive integer in milliseconds/);
  }
});

test('authentication defaults to enabled and accepts only explicit true or false', () => {
  assert.equal(parseAuthEnabled(), true);
  assert.equal(parseAuthEnabled(''), true);
  assert.equal(parseAuthEnabled('true'), true);
  assert.equal(parseAuthEnabled('false'), false);
  for (const value of ['0', '1', 'FALSE', 'invalid']) {
    assert.throws(() => parseAuthEnabled(value), /must be true or false/);
  }
});

test('explicit anonymous mode skips token IO and sends no Authorization', async t => {
  const url = await endpoint(t, (req, res) => {
    assert.equal(req.headers.authorization, undefined);
    res.end('{"ok":true}');
  });
  assert.deepEqual(await authenticatedFetch(url, 2000, '', false), { ok: true });
  await assert.rejects(authenticatedFetch(url, 2000, '', 'false'), /must be a boolean/);
});

test('anonymous mode never retries 401 or falls back from enabled mode', async t => {
  let calls = 0;
  const url = await endpoint(t, (req, res) => {
    calls++;
    assert.equal(req.headers.authorization, undefined);
    res.writeHead(401); res.end();
  });
  await assert.rejects(authenticatedFetch(url, 2000, '', false), /HTTP 401/);
  assert.equal(calls, 1);
  await assert.rejects(authenticatedFetch(url, 2000, '', true), /token file path/);
  assert.equal(calls, 1);
});

test('client reads the authentication environment switch and rejects invalid values', async t => {
  const previous = process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED;
  t.after(() => {
    if (previous === undefined) delete process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED;
    else process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED = previous;
  });
  let calls = 0;
  const url = await endpoint(t, (req, res) => {
    calls++;
    assert.equal(req.headers.authorization, undefined);
    res.end('{"ok":true}');
  });
  // Defaults are read on each call; testing them needs no child process.
  process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED = 'false';
  assert.deepEqual(await authenticatedFetch(url, 2000, ''), { ok: true });
  assert.equal(calls, 1);
  process.env.IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED = 'invalid';
  await assert.rejects(authenticatedFetch(url, 2000, ''), /must be true or false/);
  assert.equal(calls, 1);
});

async function endpoint(t, handler) {
  const server = http.createServer(handler);
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  return `http://127.0.0.1:${server.address().port}/credentials`;
}

test('unset and empty token file environment use the default; custom paths are honored', async t => {
  const url = await endpoint(t, (req, res) => {
    assert.equal(req.headers.authorization, 'a.b.c');
    res.end('{"ok":true}');
  });
  const script = `
    const fs = require('node:fs/promises');
    const open = fs.open;
    const fixture = process.argv[3];
    const expected = process.env.REVIEW_EXPECTED_TOKEN_PATH;
    fs.open = (file, ...args) => {
      require('node:assert/strict').equal(file, expected);
      return open(fixture, ...args);
    };
    require(process.argv[1]).fetchJson(process.argv[2], 2000)
      .then(c => { if (!c.ok) process.exitCode = 1; })
      .catch(e => { console.error(e.message); process.exitCode = 1; });
  `;
  for (const value of [undefined, '', '/run/custom/token']) {
    const env = { ...process.env, IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED: 'true',
      REVIEW_EXPECTED_TOKEN_PATH: value || '/run/secrets/idaas-akless/credential-auth/token' };
    if (value === undefined) delete env.IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE;
    else env.IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE = value;
    await promisify(execFile)(process.execPath,
      ['-e', script, require.resolve('./credentials-client'), url, defaultTokenFile], { env, timeout: 5000 });
  }
});

test('fetch parses JSON without altering the credential contract', async t => {
  const raw = { Code: 'Success', AccessKeyId: 'fake-id', AccessKeySecret: 'fake-secret', SecurityToken: 'fake-token', Expiration: 'future' };
  const url = await endpoint(t, (req, res) => {
    assert.equal(req.headers.authorization, 'a.b.c');
    res.end(JSON.stringify(raw));
  });
  assert.deepEqual(await fetchJson(url, 2000), raw);
});

test('HTTP failures omit upstream response bodies from errors', async t => {
  const url = await endpoint(t, (_, res) => { res.writeHead(502); res.end('sensitive test detail'); });
  await assert.rejects(fetchJson(url, 2000), { message: 'sidecar returned HTTP 502' });
});

test('invalid JSON rejects the request', async t => {
  const url = await endpoint(t, (_, res) => res.end('invalid'));
  await assert.rejects(fetchJson(url, 2000), /invalid sidecar JSON/);
});

test('total deadline aborts a response even while body data keeps arriving', async t => {
  const url = await endpoint(t, (_, res) => {
    res.writeHead(200);
    res.write('{');
    const timer = setInterval(() => res.write(' '), 5);
    res.on('close', () => clearInterval(timer));
  });
  await assert.rejects(fetchJson(url, 100), /sidecar request deadline exceeded/);
});

test('connection errors reject the request', async () => {
  await assert.rejects(fetchJson('http://127.0.0.1:0/credentials', 2000), /sidecar request failed/);
});

test('untrusted destinations are rejected before reading or sending a token', async () => {
  for (const url of ['http://example.com/credentials', 'http://localhost/credentials',
    'https://127.0.0.1/credentials', 'http://127.0.0.1/download',
    'http://user:pass@127.0.0.1/credentials', 'http://127.0.0.1/credentials#fragment']) {
    await assert.rejects(fetchJson(url, 2000), /numeric loopback/);
  }
});

test('missing, writable, oversized, malformed and non-file tokens never send anonymous HTTP', async t => {
  let requests = 0;
  const url = await endpoint(t, (_, res) => { requests++; res.end('{}'); });
  for (const [name, value, mode] of [
    ['empty', '', 0o440], ['newline', 'a.b.c\n', 0o440], ['Bearer', 'Bearer a.b.c', 0o440],
    ['large', 'a'.repeat(8193), 0o440], ['writable', 'a.b.c', 0o640], ['malformed', 'a..c', 0o440]
  ]) {
    const file = path.join(directory, name);
    await fs.writeFile(file, value, { mode });
    await assert.rejects(fetchJson(url, 2000, file), /token file unavailable or invalid/);
  }
  await assert.rejects(fetchJson(url, 2000, path.join(directory, 'missing')), /token file unavailable/);
  await assert.rejects(fetchJson(url, 2000, directory), /token file unavailable/);
  await assert.rejects(fetchJson(url, 2000, ''), /token file path/);
  assert.equal(requests, 0);
});

test('each fetch rereads rotated token; 401 rereads once within the request', async t => {
  const file = path.join(directory, 'rotation');
  await publish(file, 'old.token.signature');
  const seen = [];
  const url = await endpoint(t, async (req, res) => {
    seen.push(req.headers.authorization);
    if (seen.length === 1) {
      await publish(file, 'new.token.signature');
      res.writeHead(401); res.end();
    } else res.end('{}');
  });
  assert.deepEqual(await fetchJson(url, 2000, file), {});
  await publish(file, 'third.token.signature');
  await fetchJson(url, 2000, file);
  assert.deepEqual(seen, ['old.token.signature', 'new.token.signature', 'third.token.signature']);
});

test('401 retries once; 403 and 503 never retry or fall back', async t => {
  for (const status of [401, 403, 503]) {
    let requests = 0;
    const url = await endpoint(t, (_, res) => { requests++; res.writeHead(status); res.end('secret'); });
    await assert.rejects(fetchJson(url, 2000), { message: `sidecar returned HTTP ${status}` });
    assert.equal(requests, status === 401 ? 2 : 1);
  }
});

test('redirects do not forward token; environment proxy is not used', async t => {
  let forwarded = 0;
  const target = await endpoint(t, (_, res) => { forwarded++; res.end('{}'); });
  const url = await endpoint(t, (_, res) => { res.writeHead(302, { Location: target }); res.end(); });
  const previous = process.env.HTTP_PROXY;
  process.env.HTTP_PROXY = target;
  try { await assert.rejects(fetchJson(url, 2000), { message: 'sidecar returned HTTP 302' }); }
  finally {
    if (previous === undefined) delete process.env.HTTP_PROXY;
    else process.env.HTTP_PROXY = previous;
  }
  assert.equal(forwarded, 0);
});

test('response size is bounded', async t => {
  const url = await endpoint(t, (_, res) => res.end('x'.repeat(1024 * 1024 + 1)));
  await assert.rejects(fetchJson(url, 2000), /response too large/);
});

test('dedicated agent bypasses environment proxy even with Node proxy opt-in', async t => {
  let proxied = 0;
  const proxy = await endpoint(t, (_, res) => { proxied++; res.writeHead(500); res.end(); });
  const target = await endpoint(t, (req, res) => {
    assert.equal(req.headers.authorization, 'a.b.c');
    res.end('{"ok":true}');
  });
  const script = 'require(process.argv[1]).fetchJson(process.argv[2],2000,process.argv[3]).then(c=>{if(!c.ok)process.exitCode=1}).catch(()=>{process.exitCode=1})';
  const proxyOrigin = new URL(proxy).origin;
  await promisify(execFile)(process.execPath, ['-e', script, require.resolve('./credentials-client'), target, defaultTokenFile], {
    env: { ...process.env, IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED: 'true', NODE_USE_ENV_PROXY: '1', HTTP_PROXY: proxyOrigin, http_proxy: proxyOrigin,
      HTTPS_PROXY: proxyOrigin, https_proxy: proxyOrigin, NO_PROXY: '', no_proxy: '' },
    timeout: 5000
  });
  assert.equal(proxied, 0);
});

test('401 attempts share one deadline', async t => {
  let calls = 0;
  const url = await endpoint(t, (_, res) => {
    calls++;
    if (calls === 1) setTimeout(() => { res.writeHead(401); res.end(); }, 20);
  });
  await assert.rejects(fetchJson(url, 150), /deadline exceeded/);
  assert.equal(calls, 2);
});

test('cloud role reads only the project variable', () => {
  assert.equal(configuredRoleArn({}), '');
  assert.equal(configuredRoleArn({ IDAAS_AKLESS_CLOUD_ROLE_ARN: 'project-role' }), 'project-role');
  assert.equal(configuredRoleArn({ IDAAS_ROLE_ARN: 'old', IDAAS_CLOUD_ROLE_ARN: 'old' }), '');
  assert.equal(configuredRoleArn({ IDAAS_AKLESS_CLOUD_ROLE_ARN: '', IDAAS_ROLE_ARN: 'old' }), '');
});
