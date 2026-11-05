# Business SDK integration

**English** | [简体中文](SDK-INTEGRATION.zh-CN.md)

Local authentication is enabled by default. Both default and raw responses require a file token. Mount the local authentication token projection read-only in the business container, with audience `idaas.akless.sidecar` and default path `/run/secrets/idaas-akless/credential-auth/token`. Never mount or repurpose the IDaaS bootstrap token.

Compatibility or controlled tests can explicitly disable local authentication; see [steps and risks](../deploy/CONFIGURATION.md#explicitly-disabling-local-authentication). Use Sidecar `--credential-auth-enabled=false` or `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false`. The included Node.js client reads that variable and skips file reads and authentication headers when false. Native cloud SDKs do not read this project-specific variable; configure their providers separately. Disabling does not change IDaaS bootstrap identity or role authorization, nor demonstrate sustained SDK refresh.

## Included authenticated HTTP client

The [Node.js client](../business-example/credentials-client.js) uses built-in modules with no extra dependencies:

```js
const { fetchJson, configuredRoleArn, configuredCredentialsURI, parseTimeoutMs } = require('./credentials-client');
async function callCloudApi() {
  const url = new URL(configuredCredentialsURI());
  const roleArn = configuredRoleArn(process.env);
  if (roleArn) url.searchParams.set('roleArn', roleArn);
  const timeoutMs = parseTimeoutMs(process.env.IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS);
  const c = await fetchJson(url, timeoutMs);
  // Inject the returned credential triple into the product SDK.
}
```

`configuredCredentialsURI()` prefers a nonempty full URI, then the shared port. When both are absent or empty it uses `http://127.0.0.1:61765/credentials`. Shared ports range from `1–65535`. Native vendor SDKs still need an explicitly configured full URI using their own standard variables.

Authentication defaults to enabled. Each actual acquisition rereads the token and sends a bare JWT in Authorization. The client accepts only numeric-loopback `/credentials` endpoints. Since the Sidecar listens only on `127.0.0.1`, use IPv4. Proxies and redirects are disabled; bodies are limited to 1MiB. Enabled mode rereads the file and retries at most once on 401, sharing one HTTP deadline across both attempts. It checks the deadline after a file read but cannot forcibly interrupt an already-started filesystem operation. File failure never sends an anonymous request; disabled mode does not retry 401. Override the path with `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` using a trusted absolute path and read-only projected mount. A non-root reader can read root-owned `0640`; group/other-writable files and writable files owned by the service user are rejected. Do not use this client for user-supplied URLs, downloads or webhooks.

Select a role using `IDAAS_AKLESS_CLOUD_ROLE_ARN` or `URL.searchParams` for URI `roleArn`. The example matches the business client: a nonempty environment role overrides the URI role; without a nonempty URI role the Sidecar default applies. `fetchJson` itself reads only local-auth switch/token-file environment variables; the caller supplies URI, role and timeout. Copying only the helper does not automatically load those three business settings. See the [configuration map](../deploy/CONFIGURATION.md#parameter-names-and-cross-layer-mapping). The business example acquires once at startup. Long-running applications must refresh by Expiration and atomically replace AK, SK and token from the same generation. Static Credential objects do not refresh themselves.

## AWS Token File mode

Configure the business container:

```text
AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:61765/credentials
AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE=/run/secrets/idaas-akless/credential-auth/token
```

Mount the same local-auth projected file read-only. Do not also set static credentials, `AWS_CONTAINER_CREDENTIALS_RELATIVE_URI` or other token variables that override this source. The file contains a bare JWT; the SDK must not prepend Bearer. Offline response checks do not prove file rereading, rotation, proxy or redirect behavior for every SDK version. Use only versions that pass these acceptance checks, or use a controlled adapter.

## Alibaba Cloud and Tencent Cloud

Alibaba Cloud's Node.js native URI provider has separate [live integration tests](../verification/native-uri/README.md) for explicit/default chains, authentication modes, real STS and cloud identity. Optional long tests verify near-expiry refresh but stop before waiting for the first STS to actually expire. Continued operation across expiry needs separate acceptance. See [testing](TESTING.md) for all entry points and prerequisites.

In default authenticated mode, setting only `ALIBABA_CLOUD_CREDENTIALS_URI` does not satisfy the endpoint's file authentication. Production integrations should use a file-authenticated adapter and inject returned credentials into product SDKs. Explicit anonymous mode for URI compatibility removes local SSRF protection and requires separate field/network/refresh verification of the chosen SDK. Refreshing providers for every language are not supplied; existing field-mapping probes do not establish completed authenticated integration.

| Cloud | Default response fields | Business integration |
| --- | --- | --- |
| Alibaba Cloud | AccessKeyId, AccessKeySecret, SecurityToken, Expiration | Inject into the product SDK and refresh before expiry |
| AWS | AccessKeyId, SecretAccessKey, Token, Expiration | Accepted Token File provider or controlled adapter |
| Tencent Cloud | tmpSecretId, tmpSecretKey, token, expiration, expiredTime | Map temporary credentials and implement refresh |

## Authentication and environment acceptance

The Sidecar can return sufficiently valid cached credentials immediately while refreshing in the background. The default lifetime margin is `5m`, configured in the Sidecar through `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY` or `--credential-refresh-min-validity`. Align it with the SDK's early-refresh window and application headroom, avoiding repeated retrieval of the same near-expiry credential. It controls waiting only, not the lifetime rules for new upstream results or unexpired fallback after failure. Applications must still check actual Expiration. Refresh is request-triggered; SDKs contacting the Sidecar only near expiry may wait for upstream access. This is not periodic prefetch.

The authentication rejection/rotation checks below apply to enabled mode. Disabled mode needs separate anonymous acquisition, role rejection, invalid-query and expiry checks; it cannot claim the same SSRF-protection acceptance.

- Missing/wrong tokens and other Pods' tokens cannot obtain credentials, including cache hits and raw output.
- Missing, unsafe or expired files deny acquisition without anonymous requests or higher-privilege credential-source fallback.
- Observe at least two real kubelet rotations in one business process; verify file rereading, STS replacement and final expiry behavior.
- Ensure redirects/proxies cannot obtain the token, and general-purpose business HTTP clients never attach it automatically.
- Ensure the local token cannot obtain IDaaS identity or authenticate to the API server as a ServiceAccount. An RBAC 403 after successful authentication is not proof of audience isolation.
- Check the expected cloud account/role and call a required business API. Do not print tokens, credential bodies or secret fields.

Local authentication checks possession of a shared secret, not process identity or immediate revocation. It does not block file-read vulnerabilities, RCE or use of already-issued STS. The platform must isolate node metadata and other credential proxies. See [configuration](../deploy/CONFIGURATION.md), [response contracts](CREDENTIALS-RESPONSE.md) and [offline verification scope](../verification/sdk-contracts/README.md).
