# Akless Sidecar 安全说明

[English](SECURITY-HARDENING.md) | **简体中文**

本文描述当前实现。启动参数、环境变量、默认值及覆盖顺序统一见 [配置契约](../deploy/CONFIGURATION.zh-CN.md)，HTTP 参数及响应见 [凭据响应契约](CREDENTIALS-RESPONSE.zh-CN.md)。

## 两类投射 Token

| 名称 | 配置入口 | 默认文件 | 使用方与用途 |
| --- | --- | --- | --- |
| OIDC 引导 Token | SDK `oidcTokenFilePath`，或由 `oidcTokenFilePathEnvVarName` 指定的环境变量；模板使用 `OIDC_TOKEN_FILE_PATH` | `/run/secrets/oidc-federated/token` | 仅 Sidecar 挂载，用于向 IDaaS 认证；受众与 IDaaS 联邦配置一致 |
| 本地认证 Token | `--credential-auth-token-file` / `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | `/run/secrets/idaas-akless/credential-auth/token` | Sidecar 与指定业务容器只读挂载，用于业务访问 `/credentials`；受众为 `idaas.akless.sidecar` |

两者由 kubelet 投射并轮换，使用独立卷及整目录只读挂载，不使用 `subPath`。业务不能挂载 OIDC 引导 Token 或 IDaaS 配置。`applicationFederatedCredentialName` 是 IDaaS 应用联邦凭证的名称，不是 Token 文件名、卷名或 ServiceAccount 名称。

本地认证 Token 投射期限只支持 600–3600 秒，默认 3600；当前和旧 Token 的有效截止时间都不晚于 `min(exp, iat+3600秒)`。更长的 JWT 有效期不会放宽该上限，也不会促使 kubelet 提前换发，可能导致签发一小时后持续返回 `503`，直到有效新 Token 投射。此上限不适用于独立的 OIDC 引导 Token；详细部署约束见配置契约。

## 本地访问控制

本地认证默认开启，由 `--credential-auth-enabled` / `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` 控制。默认模板的同名 ConfigMap 键同时供 Sidecar 与 Node.js 业务示例读取，只接受字符串 `"true"` 或 `"false"`。

开启时，业务发送 `Authorization: <本地认证 JWT>`，不加 `Bearer`。Sidecar 比对受信投射文件，检查 Pod UID、命名空间、ServiceAccount、受众和有效期；不接受任意同 ServiceAccount 的 JWT，也不通过本地 JWT 签名校验或 TokenReview 建立信任。文件必须满足 Linux 只读挂载、root 所有权等要求，具体约束见配置契约。

普通响应、`format=raw` 和缓存命中均执行认证及输出前复核。运行中参考文件失效返回 `503`，无有效访问 Token 返回 `401`；非法认证头返回 `400`。轮换时仅保留受有效期约束的旧值宽限，客户端应每次取证重读文件；文件或 HTTP 错误均不会触发自动匿名降级。

认证参考文件故障会记录 `WARN local credential authentication unavailable: reason=<固定原因代码>`，恢复后记录一次 `local credential authentication recovered`。同一持续故障仅记录一次；原因变化或故障反复出现时，失败日志最多每分钟一次，被限频的原因若持续存在会在后续取证检查中补记。仅对已记录的故障输出恢复日志。正常轮换及缺失/错误请求 Token 不逐请求记录；诊断在取证或输出前检查时触发，不由 `/healthz` 主动轮询。

原因代码包括 `file_missing`（文件/目录缺失）、`file_permission_denied`（无法读取）、`file_io_failed`（其他文件 I/O 错误）、`invalid_file`（类型或大小不符）、`unsafe_permissions`（所有权或写权限不安全）、`invalid_token`（格式或解析失败）、`invalid_binding`（受众或 Pod 绑定不符）、`invalid_validity`（时间声明缺失或非法）、`expired`（到期）、`maximum_age_exceeded`（超过一小时上限）。启动错误也携带固定原因代码，另包括 `invalid_configuration`、`non_root_required` 和 `invalid_mount`。本地认证诊断不包含 Token、JWT 声明、路径或底层错误原文；其他 SDK／启动日志可能包含配置路径或实例元数据。HTTP 错误仍保持统一，不暴露内部原因。

显式关闭本地认证后，客户端不读本地认证 Token 文件、不发送认证头；Sidecar 仍执行 IDaaS 引导认证、角色授权和本地角色白名单。关闭会失去本地 Token 提供的 SSRF 凭据保护，生产推荐保持开启。

## 角色、网络与运行边界

角色请求参数固定为 `roleArn`；省略或为空时使用 `--default-role-arn` / `IDAAS_AKLESS_DEFAULT_ROLE_ARN`。可选白名单为 `--allowed-role-arns` / `IDAAS_AKLESS_ALLOWED_ROLE_ARNS`，空值关闭本地角色限制；所有角色仍须获得 IDaaS 授权。参数值是云角色外部 ID，不强制 ARN 前缀。

Sidecar 仅监听 `127.0.0.1`，端口由 `--listen-port` / `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` 控制。其 Developer API 客户端限制 HTTPS，禁止重定向及环境代理，并限制响应大小。业务示例仅向数字 loopback 的 `/credentials` 发送 Token，同样禁止重定向及代理。

默认模板采用原生 Sidecar。`--healthcheck` 仅检查本地 `/healthz`，不检查实时取证或云权限。缓存按角色保存在进程内，刷新失败仅可复用仍有效的凭据，不能返回过期凭据。业务 SDK 仍需实现凭据更新。

同 Pod 仅部署互信容器，使用专用 ServiceAccount、最小权限云角色和独立的 Token 挂载。该机制不提供进程身份或即时撤销，不能阻止 RCE、任意文件读取或已泄漏 STS 的使用；节点元数据及其他凭据代理的隔离由部署平台负责。

## 验证

本地认证、缓存、响应及集群测试的入口与前提见 [测试说明](TESTING.zh-CN.md)。不能将一次本地认证 Token 轮换视为全部凭据链路的长期轮换验收；真实环境须分别验证本地 Token、Sidecar STS 缓存和业务 SDK 的持续刷新。

发行内容和历史信息处理见 [发布检查](RELEASING.zh-CN.md)。当前配置和响应分别以配置契约与凭据响应契约为准。
