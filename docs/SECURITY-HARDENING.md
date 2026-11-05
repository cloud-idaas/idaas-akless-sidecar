# Akless Sidecar security notes

**English** | [简体中文](SECURITY-HARDENING.zh-CN.md)

This page describes the current implementation. See the [configuration contract](../deploy/CONFIGURATION.md) for options, environment variables, defaults and precedence, and the [credential response contract](CREDENTIALS-RESPONSE.md) for HTTP parameters and responses.

## Two projected tokens

| Token | Configuration | Default file | Consumer and purpose |
| --- | --- | --- | --- |
| OIDC bootstrap token | SDK `oidcTokenFilePath` or the variable named by `oidcTokenFilePathEnvVarName`; templates use `OIDC_TOKEN_FILE_PATH` | `/run/secrets/oidc-federated/token` | Sidecar only; authenticates to IDaaS with the audience configured for IDaaS federation |
| Local authentication token | `--credential-auth-token-file` / `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | `/run/secrets/idaas-akless/credential-auth/token` | Read-only in Sidecar and designated business containers; authenticates `/credentials` requests; audience `idaas.akless.sidecar` |

Kubelet projects and rotates both. Use distinct volumes and whole-directory read-only mounts without `subPath`. Business containers must not mount the bootstrap token or IDaaS configuration. `applicationFederatedCredentialName` is the application's IDaaS federated credential name, not a token filename, volume name or ServiceAccount name.

The local projection supports 600–3600 seconds, default 3600. Current and previous tokens are valid no later than `min(exp, iat+3600 seconds)`. Longer JWT expiry does not relax this cap or make kubelet rotate sooner. It can cause continuous `503` after one hour until a valid new projection appears. The cap does not apply to the separate bootstrap token. See configuration for deployment constraints.

## Local access control

Local authentication defaults to enabled, controlled by `--credential-auth-enabled` / `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED`. The default ConfigMap key is read by both Sidecar and Node.js business example, accepting only strings `"true"` / `"false"`.

Enabled clients send `Authorization: <local-auth-JWT>` without `Bearer`. The Sidecar compares against a trusted projection and checks Pod UID, namespace, ServiceAccount, audience and validity. It neither accepts arbitrary JWTs for the same ServiceAccount nor establishes trust using local signature verification or TokenReview. Trust depends on the Linux read-only mount, root ownership and other file constraints in the configuration contract.

Normal responses, `format=raw` and cache hits all authenticate and recheck before output. An invalid runtime reference returns `503`; a missing/invalid request token returns `401`; malformed headers return `400`. Previous-token grace is expiry-bounded. Clients should reread the file for every acquisition. File/HTTP errors never trigger automatic anonymous fallback.

Reference failures log `WARN local credential authentication unavailable: reason=<fixed-reason-code>`; recovery logs `local credential authentication recovered` once. The same continuous failure is logged once. Changing/recurrent reasons are limited to one failure log per minute; a suppressed reason that persists is logged on a later acquisition check. Recovery logs follow only previously logged failures. Normal rotation and missing/wrong request tokens are not logged per request. Diagnostics run on acquisition/output rechecks, not by polling from `/healthz`.

Reason codes include `file_missing` (missing file/directory), `file_permission_denied` (unreadable), `file_io_failed` (other I/O failure), `invalid_file` (type/size), `unsafe_permissions` (ownership/write permissions), `invalid_token` (format/parsing), `invalid_binding` (audience/Pod binding), `invalid_validity` (missing/invalid time claims), `expired` and `maximum_age_exceeded` (one-hour cap). Startup errors also carry fixed codes, including `invalid_configuration`, `non_root_required` and `invalid_mount`. Local-authentication diagnostics contain no tokens, claims, paths or raw underlying errors. Other SDK/startup logs may include configuration paths or instance metadata. HTTP errors remain generic and expose no internal reason.

Explicitly disabled clients do not read local token files or send authentication headers. The Sidecar still performs IDaaS bootstrap authentication, role authorization and local allowlist enforcement. Disabling removes local-token SSRF protection; keep it enabled in production.

## Role, network and runtime boundaries

The role query parameter is `roleArn`; omitted/empty uses `--default-role-arn` / `IDAAS_AKLESS_DEFAULT_ROLE_ARN`. The optional allowlist is `--allowed-role-arns` / `IDAAS_AKLESS_ALLOWED_ROLE_ARNS`; empty disables local restrictions. All roles still require IDaaS authorization. Values are cloud-role external IDs, without an enforced ARN prefix.

The listener is fixed to `127.0.0.1`, with port configured through `--listen-port` / `IDAAS_AKLESS_SIDECAR_LISTEN_PORT`. The Developer API client permits HTTPS only, disables redirects/environment proxies and limits response size. The business example sends tokens only to numeric-loopback `/credentials`, also without redirects/proxies.

Default templates use native Sidecars. `--healthcheck` checks only local `/healthz`, not live credential acquisition or cloud permissions. Per-role caches stay in memory; failed refreshes can reuse only unexpired credentials. Business SDKs must still implement credential replacement.

Run mutually trusted containers in one Pod. Use a dedicated ServiceAccount, least-privilege roles and separate token mounts. This mechanism does not establish process identity or immediate revocation, and cannot prevent RCE, arbitrary file reads or use of leaked STS. Node metadata and other credential-proxy isolation are platform responsibilities.

## Verification

See [testing](TESTING.md) for local authentication, cache, response and cluster entry points and prerequisites. One local-token rotation is not long-term acceptance of the whole chain. Separately verify local-token rotation, Sidecar STS cache refresh and sustained business SDK refresh.

See [release checks](RELEASING.md) for distribution and historical information. The configuration and response contracts are authoritative for current settings and fields.
