# Configuration contract

**English** | [简体中文](CONFIGURATION.zh-CN.md)

## Parameter names and cross-layer mapping

This page is the common reference for current parameter names, defaults and precedence. The executable's `--help` is authoritative for CLI options. The English and Chinese integration guides, ConfigMaps and business examples use the names below; unlisted legacy parameters are not aliases.

Local authentication consistently uses `credential-auth`: the projected volume is `credential-auth-token`, the default directory is `/run/secrets/idaas-akless/credential-auth` and its file is `token`. The documentation calls this the “local authentication token.” It authenticates business requests to the Sidecar credential endpoint. It is distinct from IDaaS federation authentication and cloud-role authorization, and has a different purpose from the OIDC bootstrap token and IDaaS Access Token.

Both deployment templates explicitly set `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE=/run/secrets/idaas-akless/credential-auth/token` in the Sidecar and business containers. This keeps mount and read paths aligned, including for images that support this variable but still have an older default path.

| Meaning | CLI / HTTP | Environment / configuration | Contract |
| --- | --- | --- | --- |
| Sidecar default role | `--default-role-arn` | `IDAAS_AKLESS_DEFAULT_ROLE_ARN`; same ConfigMap key | Used when the request has no nonempty role |
| Role for this request | HTTP `roleArn` | Node.js example `IDAAS_AKLESS_CLOUD_ROLE_ARN`; upstream `cloudAccountRoleExternalId` | Cross-protocol names for a cloud-role external ID; the request role and default role have different purposes |
| Local role allowlist | `--allowed-role-arns` | `IDAAS_AKLESS_ALLOWED_ROLE_ARNS`; same ConfigMap key | Comma-separated cloud-role external IDs |
| Local authentication switch | `--credential-auth-enabled` | `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED`; same ConfigMap key | Same variable in the Sidecar and Node.js business client |
| Local authentication token file | `--credential-auth-token-file` | `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | Both ends use the same variable; default mounts point to the same projected token |
| OIDC bootstrap token file | SDK `oidcTokenFilePath` | SDK `oidcTokenFilePathEnvVarName` names the variable; templates use `OIDC_TOKEN_FILE_PATH` | Sidecar only; never reuse the local authentication token |
| SDK configuration file | `--config` | `CLOUD_IDAAS_CONFIG_PATH` | Existing SDK name is retained; JSON fields follow the SDK contract |
| Sidecar listening port | `--listen-port` | `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | Integer port; the listener IP is fixed |
| Business credentials URI | None | `IDAAS_AKLESS_CREDENTIALS_URI` | Full HTTP URI matching the Sidecar port |
| Total upstream refresh timeout | `--credential-refresh-timeout` | `IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT` | Duration with a unit, such as `10s`; covers IDaaS token acquisition and the Developer API call |
| HTTP graceful shutdown budget | `--shutdown-timeout` | `IDAAS_AKLESS_SHUTDOWN_TIMEOUT` | Defaults to `auto`: refresh timeout + `5s`; an explicit duration cannot be shorter |
| Cached lifetime margin during refresh | `--credential-refresh-min-validity` | `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY` | Positive duration, default `5m`; cached lifetime must strictly exceed it to return immediately during refresh |
| Total business credential request timeout | None | Node.js example `IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS` | Integer milliseconds, such as `15000`; covers local file reads, HTTP and one 401 retry |

Role values are called “cloud-role external IDs.” `ARN` / `Arn` is retained in existing names for compatibility; values do not require an ARN prefix. Do not supply an internal IDaaS cloud-role ID. The Node.js example uses this precedence: nonempty `IDAAS_AKLESS_CLOUD_ROLE_ARN` > nonempty URI `roleArn` > Sidecar default role.

`CREDENTIAL_AUTH_*` refers to local authentication, `CREDENTIAL_REFRESH_TIMEOUT` to the Sidecar's upstream refresh budget, and `CREDENTIALS_URI` / `CREDENTIALS_TIMEOUT_MS` to business credential requests. These are not interchangeable aliases. Do not invent abbreviations or drop the `ARN` suffix in other guides. New project parameters use `IDAAS_AKLESS_`; external SDK fields and standard environment variables retain their original names.

SDK file mapping is fixed: source example `cloud_idaas.json.example` → ConfigMap key `idaas-akless-client-config.json` → volume file `client-config.json` → container path `/etc/idaas-akless/client-config.json`. These are names at different locations, not four configuration options. The CLI and environment variable take the final container path.

Native cloud SDK variables have independent contracts: AWS uses `AWS_CONTAINER_CREDENTIALS_FULL_URI` and `AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`; Alibaba Cloud's URI provider uses `ALIBABA_CLOUD_CREDENTIALS_URI`. Their capabilities differ from this project's variables. Follow [SDK integration](../docs/SDK-INTEGRATION.md).

## Sidecar startup options

Precedence is CLI > nonempty environment variable > default. An explicit empty CLI value overrides the environment: for example, `--allowed-role-arns=` disables the local allowlist; `--config=` is invalid.

| CLI | Environment variable | Default | Purpose |
| --- | --- | --- | --- |
| `--config` | `CLOUD_IDAAS_CONFIG_PATH` | `/etc/idaas-akless/client-config.json` | IDaaS Core SDK configuration |
| `--credential-auth-enabled` | `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | `true` | Only `true` or `false`; explicit `false` disables local authentication of business requests |
| `--credential-auth-token-file` | `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | `/run/secrets/idaas-akless/credential-auth/token` | Read-only projected token for this Pod; a nonempty absolute path is required when authentication is enabled; unread when disabled |
| `--listen-port` | `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | `61765` | Optional port, `1–65535`; IP fixed to `127.0.0.1`; self-check uses the same port |
| `--default-role-arn` | `IDAAS_AKLESS_DEFAULT_ROLE_ARN` | Empty | Default when the request has no nonempty `roleArn` |
| `--allowed-role-arns` | `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` | Empty | Optional comma-separated allowlist; empty relies on IDaaS authorization |
| `--credential-refresh-timeout` | `IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT` | `10s` | Total budget for IDaaS token acquisition and Developer API access; positive duration with a unit |
| `--credential-refresh-min-validity` | `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY` | `5m` | Return immediately during refresh only if cached lifetime strictly exceeds this margin; otherwise wait; positive duration with a unit |
| `--healthcheck` | None | `false` | Check local `/healthz` without initializing the SDK |
| `--shutdown-timeout` | `IDAAS_AKLESS_SHUTDOWN_TIMEOUT` | `auto` | Finite HTTP drain budget; defaults to refresh timeout + `5s`; an explicit value cannot be shorter |
| `--version` | None | `false` | Print build version/revision and exit without reading SDK or local authentication configuration or validating runtime port/timeouts |

Same-role callers share one refresh, with its budget starting when that task starts. Client cancellation ends only that caller's wait. It does not cancel a shared refresh already in progress. Applications must set their own total request deadline. Graceful shutdown defaults to “refresh timeout + 5 seconds,” or 15 seconds with default settings. Use `--shutdown-timeout` / `IDAAS_AKLESS_SHUTDOWN_TIMEOUT` for a longer finite duration, or `auto` to restore automatic calculation. Startup rejects an explicit budget shorter than the response write budget.

On SIGTERM/SIGINT the server stops accepting requests and waits for active HTTP handlers within the shutdown budget. At the deadline it closes connections, cancels request contexts and exits nonzero. This does not promise to preserve background refreshes with no remaining HTTP waiter; process termination discards the memory cache. Kubernetes `terminationGracePeriodSeconds` must cover business-container shutdown, Sidecar draining and a margin. Native Sidecars terminate after business containers, so the Sidecar does not own the whole Pod grace period. Coordinate increases with Pod termination grace and business request deadlines.

Refresh is request-triggered; no periodic task is created. When refresh is due, a cached credential above the margin returns immediately while one background refresh starts. Callers can also reuse sufficiently valid cached credentials during an existing refresh. Empty, expired or at/below-margin cache entries make callers wait. The default `5m` margin is not a universal SDK compatibility guarantee. Align it with the SDK's early-refresh window and application needs; for example, set `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY=15m` in the Sidecar. This does not change SDK refresh settings.

For remaining lifetime L at fetch completion, the initial refresh point is after 4/5 of L. If L can accommodate both the margin and one refresh budget, the point can move earlier to `expiry - margin - refresh budget`. Short-lived credentials retain proportional refreshing to avoid continuous fetching immediately after caching. An SDK that only contacts the Sidecar near expiry may still wait for upstream access; this is not scheduled prefetch.

The margin controls waiting during refresh, not the minimum lifetime of every successful response. Complete, unexpired new upstream credentials remain acceptable. Refresh failures and backoff can still return an unexpired old credential below the margin. Responses retain their actual expiry, undergo validation and local authentication recheck, and never use background refresh to return expired credentials.

## IDaaS Core SDK configuration

Use the complete console-exported JSON; see [cloud_idaas.json.example](../cloud_idaas.json.example).

The Sidecar always passes its resolved, nonempty configuration path to the Core SDK. A missing or invalid file fails startup; it does not fall back to `~/.cloud_idaas/client-config.json` or individual SDK environment fields. `--version` and `--healthcheck` do not load this file.

- `clientId` identifies the M2M application. Use your tenant's instance, issuer, tokenEndpoint, scope, Developer API and federated credential name.
- A nonempty `oidcTokenFilePath` takes precedence over the environment variable. Templates leave it empty and use `oidcTokenFilePathEnvVarName=OIDC_TOKEN_FILE_PATH`. Do not retain the literal placeholder `${oidcTokenFilePath}`.
- SDK `httpConfiguration.connectTimeout/readTimeout` is in milliseconds and applies only to the Core SDK. The Sidecar refresh budget is separate; context bounds token acquisition and the Developer API request.
- The Sidecar's Developer API client accepts HTTPS only, rejects embedded user credentials and fragments, disables redirects and environment proxies, and limits successful bodies to 1MiB. It does not read non-success bodies. These restrictions add no switches and do not modify the Core SDK's HTTP configuration.

## Kubernetes configuration mapping

Default templates require Kubernetes 1.33+. `idaas-akless-sidecar` is in `initContainers` with container-level `restartPolicy: Always`; `business` is in `containers`. Its `startupProbe` runs `--healthcheck`, so the business starts after the local listener becomes available. That probe does not call IDaaS or guarantee upstream credential acquisition. Business containers exit before the Sidecar on Pod termination. Sidecar restarts during operation can still briefly interrupt credentials; applications must handle failures. Runtime state is under `status.initContainerStatuses`.

| ConfigMap key | Purpose |
| --- | --- |
| `idaas-akless-client-config.json` | Mapped by `items` to `client-config.json` under `/etc/idaas-akless/client-config.json` |
| `IDAAS_AKLESS_DEFAULT_ROLE_ARN` | Sidecar default role |
| `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` | Comma-separated allowlist including the default role in the example; empty disables local restrictions, not IDaaS authorization |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | Local authentication switch shared by the Sidecar and Node.js client; template string `"true"` |
| `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | Explicit template string `"61765"`; optional shared override; absent/empty uses `61765` in both ends and the exec self-check |

The default ConfigMap explicitly contains five keys: SDK JSON, default role, allowlist, local authentication and port. The customer template's `akless-settings` has the same keys. Removing the optional port key or setting it empty keeps the default `61765`. Environment keys use uppercase underscores and match the consumed variables; file keys use filenames, while JSON fields retain SDK names. Authentication must be a string `"true"` or `"false"`. The IP remains `127.0.0.1`; both ends reference the port with `optional: true`, so an absent key injects no environment value and program defaults apply. Upstream timeout remains `10s` and the Node.js request deadline `15000` milliseconds; no duplicate timeout entries are needed.

Deployment `OIDC_TOKEN_FILE_PATH` must equal the token mount directory plus projected filename, such as `/run/secrets/oidc-federated/token`. Business `configuredCredentialsURI()` chooses: nonempty `IDAAS_AKLESS_CREDENTIALS_URI` > loopback URI from a nonempty shared port > `http://127.0.0.1:61765/credentials`. Templates no longer inject a URI that relies on variable expansion. Explicit business URIs must match the final Sidecar port.

The `oidc-federated-token` volume contains the Kubernetes-issued, rotated OIDC token used by the Sidecar for IDaaS federation authentication. Cluster issuer and audience must match the console trust provider, and the ServiceAccount subject must match the application's federated credential. SDK `applicationFederatedCredentialName` selects that credential. Volume names and local paths are not console values, and no token is downloaded from the console.

The mount directory is `/run/secrets/oidc-federated`, with projected relative path `token`. The SDK reads the full path via the environment variable, without relying on an old directory name. Mount the entire separate volume read-only without `subPath` so kubelet rotation remains visible.

Local credential authentication is enabled by default for normal responses, raw output and cache hits. A separate Deployment token, audience `idaas.akless.sidecar`, is projected to `/run/secrets/idaas-akless/credential-auth/token` and mounted read-only only in the Sidecar and designated business containers. The bootstrap token stays Sidecar-only. Use distinct volumes and no subPath. The Downward API supplies `POD_UID`, `POD_NAMESPACE` and `POD_SERVICE_ACCOUNT` to the Sidecar; do not fill them manually.

Default containers run as non-root. With authentication enabled, the reference directory must be root-owned on an actually read-only filesystem mount. The token must also be root-owned and not group/other-writable. Both declared `0440` and kubelet's fsGroup-adjusted `0640` are supported. Projected roots can have mode `3777`; mode bits do not establish container writability. The program checks filesystem flags on a pinned directory handle and does not chmod, write or rotate the file. Tokens are at most 8KiB; kubelet symlinks inside the volume are allowed. The Linux entry point checks the pinned directory and file owner; this does not prove volume type, so deployments must still use standard projected volumes.

**Container UID constraint:** if every container, including the native Sidecar in `initContainers`, explicitly uses the same `runAsUser` or inherits a common Pod-level value, kubelet gives projected ServiceAccount token files to that UID. Current local authentication requires root ownership and rejects this combination. Default Sidecar/business UIDs are `10001` / `1000`. Retain different non-root UIDs for your own application and inspect the final admitted configuration. `fsGroup` changes group read access but cannot restore root ownership. Do not bypass this with chmod, copied tokens or disabled authentication. See [Kubernetes projected-volume security-context interactions](https://kubernetes.io/docs/concepts/storage/projected-volumes/#securitycontext-interactions).

The local `credential-auth-token` projection supports `expirationSeconds` of **600–3600 seconds**, default 3600. For both current and previous tokens, the cutoff is `min(exp, iat+3600 seconds)`; a longer JWT exp does not extend local validity. Do not change this projection to 7200 seconds or more. If kubelet has not rotated it, acquisition returns `503` after one hour from issue until a valid new token appears, even before JWT exp. This cap does not change kubelet's rotation schedule. The separate `oidc-federated-token` bootstrap volume is not subject to this local cap; set it for the IDaaS federation requirements.

Invalid startup files, mounts or Pod context abort startup. An invalid runtime reference makes credential acquisition return 503; `/healthz` still checks only local liveness. The file is reread on acquisition and before output. Previous-token grace ends at the earliest of: first observed rotation + 60 seconds; new token iat + 60 seconds; old token `min(exp, iat+3600 seconds)`. Delayed projection/observation can therefore invalidate the old token immediately. Clients must reread the file. Arbitrary other SA JWTs are not accepted, and authentication errors never enable anonymous fallback.

`deploy/rotation-integration-test.js` uses old/new `iat/exp`, the server's 30-second issue-time skew tolerance and request time to evaluate grace. It requires old-token 200 when definitely within grace and 401 when definitely beyond the latest cutoff. Unknown observation time or a request crossing the cutoff can allow either. It finally requires old-token 401 and current-token 200 after the latest cutoff; it does not assume a full 60 seconds after projection. Its offline regression is `node --test deploy/rotation-integration-test.test.js`; substitutes do not verify real kubelet rotation.

### Default port and upgrades

The default changed from `8080` to `61765`; IP remains `127.0.0.1`. `61765` is in the [RFC 6335 dynamic/private range](https://www.rfc-editor.org/rfc/rfc6335.html#section-8.1.2) and outside the [Linux default ephemeral range `32768–60999`](https://docs.kernel.org/networking/ip-sysctl.html#ip-variables). It is an overridable local default, not an IANA-assigned dedicated port or a guarantee of availability. Check nondefault Linux ranges and other operating systems separately.

When upgrading, first explicitly retain `IDAAS_AKLESS_SIDECAR_LISTEN_PORT=8080` and old business/self-check URIs; the new ConfigMap key can be the string `"8080"`. To migrate, change listener, business URI and probes together in one Pod template. The port key is optional, but templates and business code must use matching versions. Do not upgrade only the image while leaving clients pointed at 8080. Configuration is read at startup; recreate Pods after ConfigMap changes.

An occupied port causes exit. The service does not increment ports, choose a random one or listen on both ports.

### Advanced configuration: port, timeouts and multiple roles

Keep template defaults for initial integration. If changes are needed, update ConfigMap keys/container environment, recreate the Pod and revalidate.

- **Port conflict:** set the template ConfigMap `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` to an available port, such as string `"61865"`, and recreate the Pod. Both ends and self-check follow it; no separate business URI is needed unless one is explicitly configured. For custom integrations, update both Sidecar port and full business URI, including vendor SDK URI variables such as AWS's. The exec probe reads the Sidecar environment; no Sidecar `containerPort` is required. The IP cannot be `0.0.0.0`, a Pod IP, hostname or IPv6 address. If CLI `--listen-port=61865` overrides the environment, add the same option to startup/readiness self-check commands.
- **Slow upstream:** set `IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT`, for example `20s`, in the Sidecar. This is a per-refresh total timeout, not a refresh period or a replacement for SDK JSON HTTP timeouts. Coordinate business deadlines and shutdown budgets; Node.js `IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS` can be `30000`, for example.
- **Multiple roles:** set `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` to a comma-separated list including the default and required business roles, then recreate the Pod. Requests use `roleArn` and still need IDaaS authorization. Recheck the allowlist when changing the default; an empty list disables local role restrictions.

Keep default SDK/local-auth file paths for normal integration. Overrides are listed above.

Full listener-address configuration has been removed: `--listen` is unknown, and `IDAAS_AKLESS_SIDECAR_LISTEN_ADDR` is unread. Service and self-check both use fixed `127.0.0.1` and the validated port. Port 0, out-of-range ports and full addresses are rejected.

### Explicitly disabling local authentication

The default ConfigMap has string `"true"` for `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED`, read by both ends. For SDK compatibility or controlled tests, first ensure both images support disabled mode, change it to string `"false"`, apply the ConfigMap and recreate the Pod. Retaining the local token volume does not affect disabled mode. Keep the IDaaS bootstrap volume.

```yaml
data:
  IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED: "false"
```

This is a partial ConfigMap: retain other keys. To restore authentication, change it back to string `"true"`, apply and recreate the Pod; production should keep it enabled. Check final modes on both ends if CLI overrides are used.

Standalone clients can set `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false`; `--credential-auth-enabled=false` affects only the server, so configure clients explicitly too. Disabled clients do not read local token files, send Authorization or retry 401. Invalid values fail startup/acquisition. Explicit CLI wins over environment; empty environment values retain the enabled default.

Disabling skips only the local token check. IDaaS bootstrap authentication, cloud-role authorization, allowlist, loopback binding, output format and cache validation remain. Startup logs one WARN. Any process reaching loopback can obtain an authorized role's STS, so business SSRF may expose credentials. An allowlist reduces impact but cannot replace authentication. Prefer a default role or explicit allowlist in disabled mode. `deploy/integration-test.js` and rotation tests assume enabled mode; their authentication-rejection assertions do not apply when disabled.

Configuration is startup-only, without hot reload. Recreate Pods after ConfigMap changes and revalidate acquisition.

Headers and whole-request reads have 5-second budgets, idle keep-alive connections expire after 30 seconds, and response writes get refresh timeout + 5 seconds, tracking refresh-budget changes. This bounds slow connection use. GET credentials needs no body; the read budget does not bound handler waiting for a shared refresh. Callers still need a total deadline. Refresh failures are logged once per attempt, without repeating fallback logs for every backoff cache hit.

## Node.js business example

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `IDAAS_AKLESS_CREDENTIALS_URI` | Shared-port URI when unset; `http://127.0.0.1:61765/credentials` when both are absent | Explicit URI wins; HTTP only; default response format |
| `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | `61765` | Optional shared port used only without a full business URI |
| `IDAAS_AKLESS_CLOUD_ROLE_ARN` | Empty | Nonempty overrides URI `roleArn`; otherwise preserve the URI, then use the Sidecar default |
| `IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS` | `15000` | Total deadline, integer milliseconds from 1 to 2147483647 |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | `true` | Only `true`/`false`; align with the Sidecar; false skips file reads and authentication headers |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | `/run/secrets/idaas-akless/credential-auth/token` | Reread on each acquisition; send a bare JWT, never the IDaaS bootstrap file |
| `PORT` | `3000` | Example HTTP service port |

Unset or empty `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` uses the default on both ends. A nonempty value must be a trusted absolute path. An explicitly empty token-file argument passed directly to Node.js `fetchJson` is still invalid, without fallback.

In both modes the helper accepts only `http://127.0.0.1:<port>/credentials` or `http://[::1]:<port>/credentials`, disables proxies/redirects and limits bodies to 1MiB. Enabled mode rereads and retries once on 401 within one total deadline; other failures do not fall back. It does not read vendor SDK variables. URI-only SDK configuration cannot access an authentication-enabled endpoint without a file token. See [SDK integration](../docs/SDK-INTEGRATION.md) and [deployment](README.md).

The helper validates numeric IPv4/IPv6 loopback URIs, but the Sidecar listens only on `127.0.0.1`, so use IPv4. Its deadline starts before token reading and is checked afterward; AbortSignal cannot interrupt an already-started filesystem operation. Do not put authentication files on remote filesystems that can block for long periods.
