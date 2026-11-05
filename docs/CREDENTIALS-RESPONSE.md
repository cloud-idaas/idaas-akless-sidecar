# Credential endpoint response contract

**English** | [简体中文](CREDENTIALS-RESPONSE.zh-CN.md)

`GET /credentials` supports the default format and `format=raw`. All examples use fake credentials.

Local authentication is enabled by default. Both formats and cache hits require `Authorization: <this-Pod-local-auth-JWT>`, without Bearer. Missing/mismatched tokens return 401; duplicate/malformed headers return 400; an invalid trusted reference file returns 503. Authentication failure neither reads the credential cache nor calls upstream. Authentication is rechecked before committing the response. `/healthz` needs no token and returns no credentials.

Only explicit `--credential-auth-enabled=false` or `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false` disables local authentication for normal responses, raw output and cache hits alike. Disabled mode neither reads the local token nor checks Authorization. Method/query/role restrictions, upstream authentication/authorization, content and expiry validation remain. Disabling removes local-token SSRF protection; keep it enabled in production. Configuration errors, file errors and 401/503 never cause automatic anonymous fallback. See [configuration](../deploy/CONFIGURATION.md#explicitly-disabling-local-authentication).

## Requests

```http
GET /credentials?roleArn=<URL-encoded-role-external-ID>
GET /credentials?roleArn=<URL-encoded-role-external-ID>&format=raw
```

`roleArn` is a cloud-role external ID, forwarded as Developer API `cloudAccountRoleExternalId`. Alibaba Cloud and AWS commonly use role ARNs, but no ARN prefix is enforced. Identifiers must be valid UTF-8, at most 2048 bytes, and contain no whitespace or control characters. An omitted/empty value uses the configured default. `format` must be omitted or provided once as `raw`; empty, other or duplicate values return `400`. Duplicate `roleArn` and invalid URL encoding also return `400`, without upstream access. GET requests authenticate before query/role validation, so unauthenticated requests may instead receive `401`, `400 InvalidAuthentication` or `503`; other methods return `405` before authentication.

## Default output

Alibaba Cloud and AWS success fields follow their respective HTTP credential-provider formats. This does not mean every SDK version supports local file authentication; see [SDK integration](SDK-INTEGRATION.md). Tencent Cloud returns the declared Developer API credential DTO without an extra wrapper. Only these three clouds are supported. New responses for Entra ID/Azure, Huawei Cloud, GCP or other vendors are rejected in both default and raw formats and are not cached. A still-valid supported credential can be used on refresh failure; without a valid fallback, the response is `502`. `format` does not select other protocol names.

### Alibaba Cloud `alibaba_cloud`

```json
{
  "Code": "Success",
  "AccessKeyId": "FAKE_ID",
  "AccessKeySecret": "FAKE_SECRET",
  "SecurityToken": "FAKE_TOKEN",
  "Expiration": "2030-01-01T00:00:00Z"
}
```

### AWS `aws`

```json
{
  "AccessKeyId": "FAKE_ID",
  "SecretAccessKey": "FAKE_SECRET",
  "Token": "FAKE_TOKEN",
  "Expiration": "2030-01-01T00:00:00Z"
}
```

### Tencent Cloud `tencent_cloud`

```json
{
  "tmpSecretId": "FAKE_ID",
  "tmpSecretKey": "FAKE_SECRET",
  "token": "FAKE_TOKEN",
  "expiration": "2030-01-01T00:00:00Z",
  "expiredTime": 1893456000
}
```

Alibaba Cloud and AWS `Expiration` is normalized to UTC whole seconds, `YYYY-MM-DDTHH:mm:ssZ`. See [business integration](SDK-INTEGRATION.md) for providers and [verification](../verification/sdk-contracts/README.md) for pinned-version checks.

Tencent `expiredTime` is an absolute Unix-second expiry retained alongside `expiration`. Map `tmpSecretId/tmpSecretKey/token` into your SDK and implement refresh for long-running services.

## Raw output

Raw output includes the account, role and three-cloud credential fields declared by the Sidecar. Unknown upstream fields are not passed through; absent declared pointer fields are serialized as `null`. Validation applies to the selected vendor; raw output retains other declared fields if upstream supplies them.

Tencent example:

```json
{
  "cloudAccountId": "cloud_account_example",
  "cloudAccountRoleId": "cloud_role_example",
  "cloudAccountRoleName": "example-role",
  "cloudAccountRoleExternalId": "example-role-external-id",
  "cloudAccountVendorType": "tencent_cloud",
  "cloudAccountRoleAccessCredential": {
    "accessCredentialExpiresAt": 1893456000,
    "alibabaCloudStsToken": null,
    "awsStsToken": null,
    "tencentCloudStsToken": {
      "tmpSecretId": "FAKE_ID",
      "tmpSecretKey": "FAKE_SECRET",
      "token": "FAKE_TOKEN",
      "expiration": "2030-01-01T00:00:00Z",
      "expiredTime": 1893456000
    }
  }
}
```

Raw Alibaba Cloud fields are `accessKeyId/accessKeySecret/securityToken/expiration`; AWS fields are `accessKeyId/secretAccessKey/sessionToken/expiration`, with different casing from their default contracts. Tencent's DTO matches its default output. Raw output neither adds other vendors nor bypasses vendor and expiry checks.

## Validation, caching and errors

- Default and raw share content and expiry validation. Missing required fields, unsupported cloud types, invalid times and inconsistent expirations are rejected and never cached.
- `accessCredentialExpiresAt` is the common absolute Unix-second expiry. Vendor absolute times must match at Unix-second precision. Tencent `expiredTime` must be present and equal; zero cannot stand in for a missing field.
- All three vendors require RFC3339 `expiration` strings, allowing timezone offsets and fractional seconds. Validation compares Unix whole seconds. Alibaba Cloud/AWS default output is UTC whole seconds; raw and Tencent default retain upstream strings.
- Expired credentials are never returned. A failed refresh can fall back only to still-valid old credentials. Both formats share one per-role cache.
- Refresh is request-triggered, initially after 4/5 of the lifetime remaining at fetch completion. When lifetime permits, it moves earlier to reserve the cached-lifetime margin plus one refresh budget. Short-lived credentials retain proportional refresh to avoid continuous fetching immediately after caching.
- Once refresh is due or running, cached lifetime strictly above the margin returns immediately while one background refresh runs per role. Empty, expired or insufficiently valid cache makes callers wait. The default margin is `5m`, configured by `--credential-refresh-min-validity` / `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY`; align it with the SDK's early-refresh window. Cancellation ends only that caller's wait; shared refresh retains its own timeout.
- The margin is not a minimum lifetime guarantee for all successes. New upstream results are checked for content and actual expiry; failed refreshes/backoff may still return unexpired old credentials. Actual expiry is not extended. Both formats use the same strategy and output-time authentication.
- Failure backoff lasts 10 seconds from refresh completion. At most 64 role entries, including failed/backoff entries, are retained. When admitting a new role at capacity, reclamation considers entries not refreshing, without valid credentials, and outside backoff. No reclaimable entry means `502`.
- All `/credentials` responses set `Content-Type: application/json; charset=utf-8`, `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`. `/healthz` returns plain text `ok`.
- GET only. Missing/invalid roles or query errors return `400`; optional allowlist rejection `403`; other methods `405` with `Allow: GET`; upstream failure/invalid credentials `502`; internal exceptions `500`.
- Errors use top-level `code/message` and never expose credentials, role details or upstream error bodies. Logs omit complete credentials and upstream error bodies.

```json
{"code":"CredentialUnavailable","message":"failed to obtain credential"}
```

The local role allowlist is optional. Configured lists are enforced; otherwise IDaaS server-side permissions apply. See [business integration](SDK-INTEGRATION.md) for SDK field mapping and refresh.
