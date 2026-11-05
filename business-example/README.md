# Node.js HTTP business example

**English** | [简体中文](README.zh-CN.md)

Use Node.js built-in modules to obtain Alibaba Cloud temporary credentials from the Sidecar in the same Pod. No dependency installation or IDaaS SDK integration is required.

## Run

Requires Node.js 24+. Start the Sidecar on the same host, then run from this directory:

```bash
export IDAAS_AKLESS_CREDENTIALS_URI=http://127.0.0.1:61765/credentials
# Optional; otherwise use the URI role or Sidecar default.
export IDAAS_AKLESS_CLOUD_ROLE_ARN='<authorized-alibaba-cloud-role>'
npm start
```

Authentication defaults to enabled. Mount the local token read-only, default `/run/secrets/idaas-akless/credential-auth/token`, overridable by `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE`. Do not mount the IDaaS bootstrap token. The example acquires once at startup; on 401 it rereads and retries only once. Success logs only expiry; failure logs only an error message. The home page and `/healthz` are independent of acquisition.

If server authentication is explicitly disabled, also set `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false` in the business container. Only `true/false` is accepted, default `true`. Disabled mode neither reads the file, sends Authorization nor retries 401. No file or HTTP error changes enabled mode to anonymous. Keep authentication enabled in production; disabling removes this SSRF protection while retaining IDaaS bootstrap authentication. Templates share the ConfigMap switch, string `"true"`, between both containers. Recreate Pods after changes; see [configuration](../deploy/CONFIGURATION.md#explicitly-disabling-local-authentication).

Example success log:

```text
[ok] got STS credentials from sidecar, expires at <future-time>
```

In another local terminal, run `curl -fsS http://127.0.0.1:3000/healthz` and expect `ok`. If logs show only `[err]`, check Sidecar startup and default/request roles, then restart the example as needed to retry. HTTP liveness is not proof of credential acquisition.

## Use credentials in an application

Reuse the exported helpers in [credentials-client.js](credentials-client.js), following the complete function example in [SDK integration](../docs/SDK-INTEGRATION.md). [app.js](app.js) is the executable demo entry point: it constructs the URI/role and starts an HTTP server when loaded; its internal `fetchCredential()` is not a library export.

Long-running applications must refresh before expiry and handle failures/retries. One startup acquisition does not provide indefinite validity. This example reads Alibaba Cloud fields; see [SDK integration](../docs/SDK-INTEGRATION.md) for AWS/Tencent fields and providers.

| Configuration | Default |
| --- | --- |
| `IDAAS_AKLESS_CREDENTIALS_URI` | Explicit URI wins; otherwise constructed from the shared port, or `http://127.0.0.1:61765/credentials` if absent; HTTP/default format only |
| `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | Optional shared port; absent/empty is `61765`; used only without a full URI |
| `IDAAS_AKLESS_CLOUD_ROLE_ARN` | Empty; nonempty cloud-role external ID overrides URI `roleArn`; otherwise use the URI role or Sidecar default |
| `IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS` | `15000` milliseconds |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | `true`; align with Sidecar mode; only `true/false` |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | `/run/secrets/idaas-akless/credential-auth/token`; reread for each acquisition; bare JWT |
| `PORT` | `3000` |

See the complete [configuration rules](../deploy/CONFIGURATION.md#parameter-names-and-cross-layer-mapping). `app.js` uses `configuredCredentialsURI()` for full URI/shared-port/default precedence, then supplies role and timeout to `fetchJson`. The helper itself automatically reads only authentication variables. `IDAAS_AKLESS_CLOUD_ROLE_ARN` chooses this request's role; Sidecar `IDAAS_AKLESS_DEFAULT_ROLE_ARN` is the fallback for unspecified roles. Keep temporary AK, SK and tokens out of logs.

An unset/empty token-file environment variable uses the default, matching the Sidecar. An explicitly empty path argument passed directly to `fetchJson` remains an error.

## Build and verify

From the repository root, build an amd64 image:

```bash
docker buildx build --platform linux/amd64 --load \
  -f business-example/Dockerfile -t idaas-akless-business-example:local business-example/
npm test --prefix business-example
```

Pod containers share loopback; two independent Docker containers do not. Mount only the separate-audience local authentication token read-only in the business container, never IDaaS configuration/bootstrap identity. Tokens go only to numeric-loopback `/credentials`; proxies/redirects are disabled and bodies bounded. See [deployment](../deploy/README.md) for live cloud identity checks.

For your application, reuse the HTTP client and field mapping and implement required refresh/retry behavior. You do not need the demo home page or `/healthz`. If using a native SDK provider, initialize it according to [SDK integration](../docs/SDK-INTEGRATION.md). Setting an SDK URI variable for this example does not make it use that provider.
