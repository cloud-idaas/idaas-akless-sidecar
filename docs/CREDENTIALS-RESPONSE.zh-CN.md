# 凭据端点响应契约

[English](CREDENTIALS-RESPONSE.md) | **简体中文**

`GET /credentials` 提供默认输出和 `format=raw` 两种方式。所有示例使用假凭据。

默认开启本地认证，两种输出及缓存命中都要求 `Authorization: <本Pod本地认证 JWT>`，不添加Bearer。缺失或不匹配返回401，重复或非法认证头返回400；受信认证文件失效返回503。认证失败不读取凭据缓存、不调用上游，响应提交前再次检查认证有效性。`/healthz` 不要求Token，也不返回凭据。

只有显式设置 `--credential-auth-enabled=false` 或 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false` 才关闭本地认证，普通响应、raw、缓存命中统一生效。此模式不读取本地认证文件，不检查 Authorization；方法、参数、角色限制、上游认证授权、凭据完整性和过期校验保持生效。关闭会失去本地Token的SSRF保护，生产推荐保持开启。配置错误、文件错误和401/503均不会触发自动匿名降级，详细用法见 [配置契约](../deploy/CONFIGURATION.zh-CN.md#显式关闭本地认证)。

## 请求

```http
GET /credentials?roleArn=<URL编码后的角色外部标识>
GET /credentials?roleArn=<URL编码后的角色外部标识>&format=raw
```

`roleArn` 是云角色外部标识，转发为 Developer API 的 `cloudAccountRoleExternalId`；阿里云和 AWS 通常使用角色 ARN，但 Sidecar 不强制 ARN 前缀。标识必须为有效 UTF-8，最多 2048 字节，不得包含空白或控制字符。省略或空值使用已配置的默认角色。`format` 只能省略或单次传入 `raw`；空值、其他值及重复值返回 `400`。重复 `roleArn` 或无效 URL 编码也返回 `400`，参数错误不触发上游请求。GET 请求先执行本地认证，再校验参数和角色，因此未通过认证时可能先返回 `401`、`400 InvalidAuthentication` 或 `503`；其他方法在认证前返回 `405`。

## 默认输出

阿里云和 AWS 的成功响应字段按对应 HTTP 凭据提供器格式输出；这不代表任意 SDK 版本都支持本接口的本地文件认证，接入限制见 [SDK 接入](SDK-INTEGRATION.zh-CN.md)。腾讯云返回已声明的 Developer API 凭据 DTO，不增加外层包装。当前仅支持这三家云；Entra ID/Azure、华为云、GCP 等其他厂商的新响应在默认与 raw 模式下均被拒绝、不写入缓存。若已有仍有效的受支持云凭据，刷新失败可回退旧缓存；没有有效回退则返回 `502`。不支持通过 `format` 选择其他协议名。

### 阿里云 `alibaba_cloud`

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

### 腾讯云 `tencent_cloud`

```json
{
  "tmpSecretId": "FAKE_ID",
  "tmpSecretKey": "FAKE_SECRET",
  "token": "FAKE_TOKEN",
  "expiration": "2030-01-01T00:00:00Z",
  "expiredTime": 1893456000
}
```

阿里云与 AWS 的 `Expiration` 统一输出 UTC 整秒 `YYYY-MM-DDTHH:mm:ssZ`。SDK 提供器配置见 [业务接入](SDK-INTEGRATION.zh-CN.md)，固定版本验证见 [验证说明](../verification/sdk-contracts/README.zh-CN.md)。

腾讯云 `expiredTime` 是 Unix 秒级绝对到期时间，与 `expiration` 同时保留；业务侧将 `tmpSecretId/tmpSecretKey/token` 映射到所用 SDK，并实现长驻服务刷新。

## raw 输出

输出 Sidecar 声明的账号、角色及三家云凭据字段；不透传上游未知字段，缺失的已声明指针字段序列化为 `null`。校验针对所选云厂商；上游提供的其他已声明字段仍会保留在 raw 输出中。

腾讯云示例：

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

raw 中阿里云凭据字段为 `accessKeyId/accessKeySecret/securityToken/expiration`，AWS 为 `accessKeyId/secretAccessKey/sessionToken/expiration`，不使用默认输出的协议大小写。腾讯云 DTO 与默认输出一致。raw 不提供其他厂商凭据，也不能绕过厂商与有效期校验。

## 校验、缓存与错误

- 默认与 raw 共用完整性和有效期校验；缺少必要字段、不支持的云类型、无效或到期时间不一致均拒绝，不进入缓存。
- `accessCredentialExpiresAt` 是统一 Unix 秒级绝对到期时间；厂商绝对时间必须与其在 Unix 秒精度上一致。腾讯云 `expiredTime` 必须存在并一致，不能用 `0` 替代缺失值。
- 三云凭据中的 `expiration` 均为必填的 RFC3339 时间字符串，允许时区偏移和小数秒；校验按 Unix 整秒比较。阿里云与 AWS 默认输出统一为 UTC 整秒，raw 和腾讯云默认输出保留上游字符串。
- 过期凭据不返回；刷新失败仅可降级使用仍有效的旧凭据。不同输出方式共享同一份角色缓存。
- 刷新由请求触发，按取回凭据时剩余寿命的 4/5 确定刷新点；寿命允许时，为预留“后台返回门槛 + 一次刷新预算”提前刷新点。短寿命凭据仍按比例刷新，避免一入缓存就持续重取。
- 达到刷新点或已有共享刷新时，旧凭据余量严格超过后台返回门槛则立即返回，同角色仅运行一次后台刷新；缓存为空、过期或余量不足则等待结果。门槛默认 `5m`，由 `--credential-refresh-min-validity` / `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY` 配置，需匹配所用 SDK 的提前刷新窗口。客户端取消只结束自身等待，共享刷新仍受独立超时预算约束。
- 上述门槛不保证所有成功响应的最小寿命：新上游结果仍按完整性与实际到期时间校验；刷新失败及退避期仍可返回未过期旧凭据。真实到期时间不作延长，默认和 raw 使用同一策略及输出前认证。
- 刷新失败完成后退避 10 秒。最多保留 64 个角色条目（含失败退避条目）；新角色入缓存且容量已满时，尝试回收未在刷新、已无有效凭据且退避已结束的条目，无可回收条目则返回 `502`。
- 所有 `/credentials` 响应设置 `Content-Type: application/json; charset=utf-8`、`Cache-Control: no-store` 和 `X-Content-Type-Options: nosniff`；`/healthz` 返回纯文本 `ok`。
- 仅 GET；角色缺失/非法和参数错误为 `400`，可选白名单拒绝为 `403`，非 GET 为 `405`（`Allow: GET`），上游失败或无效凭据为 `502`，内部异常为 `500`。
- 错误使用顶层 `code/message`，不返回凭据、角色详情或上游错误正文。日志不记录完整凭据或上游错误正文。

```json
{"code":"CredentialUnavailable","message":"failed to obtain credential"}
```

角色白名单可选；配置时校验，不配置时依赖 IDaaS 服务端权限。SDK 字段映射与刷新见 [业务接入](SDK-INTEGRATION.zh-CN.md)。
