# 业务 SDK 接入

[English](SDK-INTEGRATION.md) | **简体中文**

Sidecar 默认开启本地认证，默认响应和 raw 输出都要求文件 Token。业务只读挂载 audience 为 `idaas.akless.sidecar` 的本地认证 Token 投射卷，默认文件为 `/run/secrets/idaas-akless/credential-auth/token`；不能挂载或转用 IDaaS 引导 Token。

兼容或受控测试场景可显式关闭本地认证，参见 [关闭步骤与风险](../deploy/CONFIGURATION.zh-CN.md#显式关闭本地认证)。Sidecar 使用 `--credential-auth-enabled=false` 或同名环境变量 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false`；本仓 Node.js 客户端读取该环境变量后不读文件、不发送认证头。原生云 SDK 不读取这个项目专用变量，需按各 SDK 的凭据提供器配置处理。关闭本地认证不改变 IDaaS 引导身份及角色授权，也不能证明 SDK 已完成持续刷新验收。

## 已实现的认证 HTTP 客户端

本仓 [Node.js 客户端](../business-example/credentials-client.js) 使用内置模块取证，无额外依赖：

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

`configuredCredentialsURI()` 优先读取非空完整 URI，其次读取共享端口；两者均省略或为空时使用 `http://127.0.0.1:61765/credentials`。共享端口范围为 `1–65535`。原生厂商 SDK 仍须按其标准变量显式配置完整 URI。

默认开启认证，每次实际取证重新读取 Token，发送裸 JWT 的 Authorization 头；客户端只接受数字 loopback `/credentials`，当前 Sidecar 固定监听 `127.0.0.1`，因此使用 IPv4 URI。禁代理与重定向，响应最多 1MiB。开启时 401 重读文件并最多重试一次，两次共用 HTTP deadline；文件读取完成后会检查是否超时，但已开始的文件系统操作不能被强制中断。文件失败不匿名请求；关闭时 401 不重试。路径可通过 `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` 覆盖，应使用可信绝对路径及只读投射挂载；支持非 root 读取 root 持有的 `0640` 文件，拒绝组/其他用户可写和服务用户自己持有的可写文件。不要将该客户端用于用户输入 URL、下载或 Webhook。

需要指定角色时，使用 `IDAAS_AKLESS_CLOUD_ROLE_ARN`，或通过 `URL.searchParams` 设置 URI 的 `roleArn`。上面的调用代码与业务示例一致：非空环境变量覆盖 URI 角色，URI 未指定非空角色时由 Sidecar 使用默认角色。`fetchJson` 本身只读取本地认证开关与 Token 文件环境变量，URI、角色和请求超时由调用方传入；只复制底层 helper 不会自动读取这三个业务配置。所有名称及跨层映射见 [配置契约](../deploy/CONFIGURATION.zh-CN.md#参数命名与跨层映射)。业务示例只在启动时取证一次；长期业务仍需按 Expiration 刷新并保持同代 AK、SK、Token 原子替换，静态 Credential 对象不会主动刷新。

## AWS Token File 模式

在业务容器配置：

```text
AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:61765/credentials
AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE=/run/secrets/idaas-akless/credential-auth/token
```

只读挂载同一份本地认证 Token 投射文件，不能同时设置会覆盖该来源的静态凭据、`AWS_CONTAINER_CREDENTIALS_RELATIVE_URI` 或其他 Token 变量。文件是裸JWT；SDK不能自动添加Bearer。本仓离线响应检查不证明各SDK版本的文件重读、轮换、代理及重定向行为；只有这些门禁通过的版本才可用于生产，必要时采用受控适配器。

## 阿里云与腾讯云

阿里云 Node.js 原生 URI 提供器另有 [真实集成测试](../verification/native-uri/README.zh-CN.md)，覆盖显式配置和默认凭据链、认证开关、真实 STS 及云端身份验证。可选长测验证临期自动刷新，但不等待首份 STS 实际到期；跨到期持续调用仍须单独验收。全部测试入口及前提见 [测试说明](TESTING.zh-CN.md)。

默认认证模式下，仅设置 `ALIBABA_CLOUD_CREDENTIALS_URI` 不会自动满足本接口的文件认证要求。生产推荐使用支持文件认证的取证适配器，将返回值注入产品SDK。选择显式无认证模式兼容URI提供器时，需接受本地SSRF保护缺失，并独立验证所选SDK的字段、网络和刷新行为。本次未交付所有语言的可刷新Provider，不把已有字段映射探针视为认证接入已完成。

| 云 | 默认返回字段 | 业务接入 |
| --- | --- | --- |
| 阿里云 | AccessKeyId、AccessKeySecret、SecurityToken、Expiration | 注入产品SDK并按到期时间刷新 |
| AWS | AccessKeyId、SecretAccessKey、Token、Expiration | 通过门禁的Token File提供器或受控适配器 |
| 腾讯云 | tmpSecretId、tmpSecretKey、token、expiration、expiredTime | 映射临时Credential并实现刷新 |

## 认证与环境验收

Sidecar 在旧凭据余量充足时可立即返回缓存并后台刷新；默认后台返回门槛为 `5m`，通过 `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY` 或 `--credential-refresh-min-validity` 在 Sidecar 配置。请按所用 SDK 的提前刷新窗口及调用余量核对门槛，防止 SDK 持续重取同一临期凭据。该门槛只决定刷新时是否等待，不改变上游新凭据和刷新失败时未过期兜底的寿命要求；业务仍须检查实际 Expiration。刷新由请求触发，SDK 只在临期时访问 Sidecar 的场景仍可能等待上游，不代表已提供定时提前刷新。

以下认证拒绝及Token轮换检查适用于默认开启模式；关闭模式应另验证无Token取证、角色拒绝、非法参数和过期拒绝，不能宣称通过相同的SSRF防护验收。

- 无Token、错误Token、其他Pod Token不能获取凭据；缓存命中和raw输出同样受保护。
- 文件缺失、权限错误或过期时拒绝取证；不得发送匿名请求，也不能切换更高权限凭据来源。
- 至少两次真实kubelet轮换，持续同一业务进程验证文件重读；连续验证STS更新及最终过期处理。
- 验证重定向及代理不能获得Token；通用业务HTTP客户端不能自动附加本地Token。
- 验证本地Token不能兑换IDaaS身份或被API Server接受为SA身份。不能把已认证后的RBAC 403当作受众隔离成功。
- 核对实际云账号和角色，并调用业务需要的云API；不打印Token、凭据正文或秘密字段。

本地认证是共享秘密持有校验，不提供进程身份或即时撤销；文件读取漏洞、RCE及已签发STS的使用不受它阻断。限制节点元数据及其他凭据代理旁路由部署平台负责。配置见 [配置契约](../deploy/CONFIGURATION.zh-CN.md)，响应字段见 [凭据契约](CREDENTIALS-RESPONSE.zh-CN.md)，离线检查范围见 [验证说明](../verification/sdk-contracts/README.zh-CN.md)。
