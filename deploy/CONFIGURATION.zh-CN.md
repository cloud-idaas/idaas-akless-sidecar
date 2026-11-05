# 配置契约

[English](CONFIGURATION.md) | **简体中文**

## 参数命名与跨层映射

本文是当前版本参数名、默认值和覆盖规则的统一入口；CLI 以程序 `--help` 为准。中英文接入指南、ConfigMap 和业务示例均沿用下表名称，未列出的旧参数不作为当前参数的别名。

本地认证统一使用 `credential-auth` 命名：投射卷为 `credential-auth-token`，默认目录为 `/run/secrets/idaas-akless/credential-auth`，目录内文件名为 `token`，文档称为“本地认证 Token”。这里的认证指业务访问 Sidecar 凭据接口的本地认证，不是 IDaaS 联邦认证或云角色授权；它与 OIDC 引导 Token、IDaaS Access Token 分别承担不同职责。

两个部署模板在 Sidecar 与业务容器中显式设置 `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE=/run/secrets/idaas-akless/credential-auth/token`，使挂载路径与实际读取路径一致，也适用于默认路径仍为旧值、但已支持该环境变量的镜像。

| 含义 | CLI / HTTP | 环境变量 / 配置 | 约定 |
| --- | --- | --- | --- |
| Sidecar 默认角色 | `--default-role-arn` | `IDAAS_AKLESS_DEFAULT_ROLE_ARN`，ConfigMap 同名 | 请求未指定非空角色时使用 |
| 本次请求角色 | HTTP `roleArn` | Node.js 示例 `IDAAS_AKLESS_CLOUD_ROLE_ARN`；上游字段 `cloudAccountRoleExternalId` | 同一个云角色外部 ID 在各协议层的映射；请求角色与默认角色用途不同 |
| 本地角色白名单 | `--allowed-role-arns` | `IDAAS_AKLESS_ALLOWED_ROLE_ARNS`，ConfigMap 同名 | 逗号分隔的角色外部 ID 列表 |
| 本地认证开关 | `--credential-auth-enabled` | `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED`，ConfigMap 同名 | Sidecar 与 Node.js 业务客户端共用同名变量 |
| 本地认证 Token 文件 | `--credential-auth-token-file` | `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | 两端使用同名变量；默认模板挂载同一路径，指向同一份投射 Token |
| OIDC 引导 Token 文件 | SDK `oidcTokenFilePath` | SDK `oidcTokenFilePathEnvVarName` 指定环境变量名，模板固定为 `OIDC_TOKEN_FILE_PATH` | 仅 Sidecar 使用，不与本地认证 Token 混用 |
| SDK 配置文件 | `--config` | `CLOUD_IDAAS_CONFIG_PATH` | 保留既有 SDK 配置命名；JSON 字段保持 SDK 契约 |
| Sidecar 监听端口 | `--listen-port` | `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | 整数端口，IP 固定 |
| 业务访问凭据的 URI | 无 | `IDAAS_AKLESS_CREDENTIALS_URI` | 完整 HTTP URI，须与 Sidecar 端口一致 |
| 上游刷新总超时 | `--credential-refresh-timeout` | `IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT` | 带单位时长，例如 `10s`；覆盖 IDaaS Token 获取与 Developer API 调用 |
| HTTP 优雅关闭预算 | `--shutdown-timeout` | `IDAAS_AKLESS_SHUTDOWN_TIMEOUT` | 默认 `auto`：刷新超时 + `5s`；显式时长不能短于该值 |
| 后台刷新旧凭据有效期门槛 | `--credential-refresh-min-validity` | `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY` | 带单位正时长，默认 `5m`；刷新时旧凭据余量严格超过门槛才立即返回 |
| 业务取证请求总超时 | 无 | Node.js 示例 `IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS` | 整数毫秒，例如 `15000`；覆盖本地文件读取、HTTP 及一次 401 重试 |

角色值统一称为“云角色外部 ID”。现有参数中的 `ARN` / `Arn` 保留兼容，参数值不要求 ARN 前缀；不要填 IDaaS 内部云角色 ID。Node.js 示例的角色优先级为：非空 `IDAAS_AKLESS_CLOUD_ROLE_ARN` > URI 中的非空 `roleArn` > Sidecar 默认角色。

`CREDENTIAL_AUTH_*` 表示本地认证，`CREDENTIAL_REFRESH_TIMEOUT` 表示 Sidecar 上游刷新预算，`CREDENTIALS_URI` / `CREDENTIALS_TIMEOUT_MS` 表示业务凭据请求。它们不是同义别名。后续文档不应另造缩写或省略 `ARN` 后缀；新增项目参数沿用 `IDAAS_AKLESS_` 前缀，外部 SDK 的既有字段与标准环境变量保留原名。

SDK 配置的文件映射固定为：源码示例 `cloud_idaas.json.example` → ConfigMap 键 `idaas-akless-client-config.json` → 卷内文件 `client-config.json` → 容器路径 `/etc/idaas-akless/client-config.json`。这些是不同位置的文件名和键名，不是四个配置参数；配置 CLI 和环境变量填写最后的容器路径。

原生云 SDK 的变量属于独立契约：AWS 使用 `AWS_CONTAINER_CREDENTIALS_FULL_URI` 和 `AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`，阿里云 URI 提供器使用 `ALIBABA_CLOUD_CREDENTIALS_URI`。它们与本项目变量的接入能力不同，不能直接互换；以 [SDK 接入说明](../docs/SDK-INTEGRATION.zh-CN.md) 为准。

## Sidecar 启动参数

优先级为 CLI > 非空环境变量 > 默认值。显式 CLI 空值覆盖环境变量，例如 `--allowed-role-arns=` 关闭白名单；`--config=` 无效。

| CLI | 环境变量 | 默认值 | 用途 |
| --- | --- | --- | --- |
| `--config` | `CLOUD_IDAAS_CONFIG_PATH` | `/etc/idaas-akless/client-config.json` | IDaaS Core SDK 配置 |
| `--credential-auth-enabled` | `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | `true` | 仅接受 `true` 或 `false`；显式 `false` 关闭业务访问 Sidecar 的本地认证 |
| `--credential-auth-token-file` | `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | `/run/secrets/idaas-akless/credential-auth/token` | 本 Pod 专用的只读投射认证文件；开启认证时必须是非空绝对路径，关闭时不读取 |
| `--listen-port` | `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | `61765` | 可选监听端口，范围 `1–65535`；IP 固定为 `127.0.0.1`，健康自检使用相同端口 |
| `--default-role-arn` | `IDAAS_AKLESS_DEFAULT_ROLE_ARN` | 空 | 请求未指定非空 `roleArn` 时的默认角色 |
| `--allowed-role-arns` | `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` | 空 | 可选本地白名单，逗号分隔；空表示仅依赖 IDaaS 授权 |
| `--credential-refresh-timeout` | `IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT` | `10s` | 获取 IDaaS Token 和调用 Developer API 的总预算，必须为带单位的正时长 |
| `--credential-refresh-min-validity` | `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY` | `5m` | 刷新时旧凭据剩余有效期严格超过此值即可立即返回，否则等待共享刷新；必须为带单位的正时长 |
| `--healthcheck` | 无 | `false` | 自检本地 `/healthz`，不初始化 SDK |
| `--shutdown-timeout` | `IDAAS_AKLESS_SHUTDOWN_TIMEOUT` | `auto` | HTTP drain 的有限预算；自动跟随刷新超时 + `5s`，显式值不能更短 |
| `--version` | 无 | `false` | 输出构建 version/revision 后退出，不读取 SDK 或本地认证配置，也不验证运行端口/超时 |

同角色请求共享一次刷新，刷新预算从该共享任务开始计时。客户端取消会结束本次请求的等待，不取消已开始的共享刷新；业务需另设整次请求 deadline。优雅关闭预算默认跟随“刷新超时 + 5 秒”，默认配置为 15 秒。可通过 `--shutdown-timeout` 或 `IDAAS_AKLESS_SHUTDOWN_TIMEOUT` 设置更长的有限时长，或用 `auto` 恢复自动计算；显式值短于响应写入预算时启动失败。

收到 SIGTERM/SIGINT 后停止接受新请求，并在关闭预算内等待在途 HTTP 请求完成；超过预算后关闭连接、取消请求上下文并非零退出。该等待不承诺保留已经没有 HTTP 等待者的后台刷新任务，进程终止后内存缓存随之消失。Kubernetes 的 `terminationGracePeriodSeconds` 应覆盖业务容器退出、Sidecar 关闭预算及余量；原生 Sidecar 在业务容器退出后才终止，不能把整个 Pod 宽限都视为 Sidecar 独享。增大刷新或关闭预算时同步调整 Pod 宽限及业务请求 deadline。

刷新由请求触发，不创建周期任务。达到刷新点时，旧凭据余量超过门槛则立即返回，并启动唯一的后台刷新；已有刷新任务时也可立即复用余量充足的旧凭据。余量等于或低于门槛、缓存为空或已过期时等待共享刷新。默认门槛为 `5m`，不是所有 SDK 的统一兼容保证：应按实际 SDK 提前刷新窗口及业务调用余量调整，例如在 Sidecar 环境中设置 `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY=15m`。该参数不改变业务 SDK 的自身刷新配置。

取回凭据的剩余寿命记为 L，先按经过 4/5 L 确定刷新点；如果 L 足以容纳门槛和一次刷新预算，则必要时将刷新点提前到 `到期时间 - 门槛 - 刷新预算`。短寿命凭据保留比例刷新点，避免一入缓存就持续重取。只在 SDK 临期访问时才收到请求的场景仍可能等待上游，本策略不保证提前定时取证。

门槛仅决定刷新时是否等待，不保证每个成功响应都具备该最小剩余寿命。上游新返回的完整、未过期凭据仍可接受；刷新失败及退避期继续保留原有未过期旧凭据兜底，不因余量低于门槛而取消降级能力。所有响应保留真实到期时间，并继续执行凭据校验和输出前本地认证，不能通过后台刷新返回过期凭据。

## IDaaS Core SDK 配置

使用控制台导出的完整 JSON；字段示例见 [cloud_idaas.json.example](../cloud_idaas.json.example)。

Sidecar 始终将解析后的非空配置路径传给 Core SDK；文件缺失或无效会使启动失败，不会再回退到 `~/.cloud_idaas/client-config.json` 或 SDK 的零散环境变量。`--version` 和 `--healthcheck` 不加载该文件。

- `clientId` 为 M2M 应用身份；实例、issuer、tokenEndpoint、scope、Developer API 和联邦凭据名称均使用本租户配置。
- 非空 `oidcTokenFilePath` 优先于环境变量。部署示例将其留空，通过 `oidcTokenFilePathEnvVarName=OIDC_TOKEN_FILE_PATH` 读取投射文件路径。不要保留 `${oidcTokenFilePath}` 字面占位符。
- SDK `httpConfiguration.connectTimeout/readTimeout` 单位为毫秒，只作用于 Core SDK。Sidecar 刷新预算单独配置，并通过 context 约束 Token 获取和 Developer API 请求。
- Sidecar 自己的 Developer API 客户端只接受 HTTPS，拒绝 URL 内嵌账号密码与 fragment，禁止重定向及环境代理，成功响应最多 1MiB；非成功响应不读取正文。这些限制不新增开关，也不修改 Core SDK 自身的 HTTP 配置。

## Kubernetes 配置映射

默认模板要求 Kubernetes 1.33+。`idaas-akless-sidecar` 位于 `initContainers` 并设置容器级 `restartPolicy: Always`；`business` 位于 `containers`。Sidecar 的 `startupProbe` 复用 `--healthcheck`，本地监听可用后才启动业务；该探针不调用 IDaaS，不保证上游取证成功。Pod 终止时业务先退出，Sidecar 随后停止。运行期间 Sidecar 重启仍可能造成短暂取证失败，业务必须处理错误。Sidecar 的运行状态位于 `status.initContainerStatuses`。

| ConfigMap 键 | 用途 |
| --- | --- |
| `idaas-akless-client-config.json` | 通过 `items` 映射为 `client-config.json`，挂载到 `/etc/idaas-akless/client-config.json` 的 SDK 配置 |
| `IDAAS_AKLESS_DEFAULT_ROLE_ARN` | Sidecar 默认角色 |
| `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` | 逗号分隔的本地角色白名单，示例包含默认角色；空值关闭本地白名单，但不绕过 IDaaS 授权 |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | Sidecar 和 Node.js 业务客户端共用的本地认证开关，默认模板为字符串 `"true"` |
| `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | 模板显式列为字符串 `"61765"`，属于可选共享端口；键缺失或值为空时两端仍默认使用 `61765`，exec 自检读取同一环境变量 |

默认 ConfigMap 显式列出客户端配置 JSON、默认角色、角色白名单、本地认证开关和监听端口五项，客户模板的 `akless-settings` 使用相同的五个键。监听端口展示为字符串 `"61765"`，属于可选覆盖项，删除该键或将值设为空仍使用默认端口。环境变量类键采用大写下划线并与消费它的环境变量同名；文件类键采用文件名格式，JSON 内部字段保持 SDK 契约。认证开关值必须是字符串 `"true"` 或 `"false"`。监听 IP 固定为 `127.0.0.1`，两端通过 `optional: true` 引用同一端口键；键缺失时不注入该环境变量，由程序默认使用 `61765`。上游刷新总超时沿用 `10s`；Node.js 业务请求超时沿用 `15000` 毫秒，无需重复配置这些超时默认值。

Deployment 的 `OIDC_TOKEN_FILE_PATH` 必须等于 Token 挂载目录加投射文件名，示例为 `/run/secrets/oidc-federated/token`。业务通过 `configuredCredentialsURI()` 选择 URI：非空 `IDAAS_AKLESS_CREDENTIALS_URI` > 非空共享端口构造的 loopback URI > `http://127.0.0.1:61765/credentials`。模板不再注入依赖变量展开的 URI；省略端口键时，两端均使用默认值。显式业务 URI 必须与 Sidecar 的最终监听端口一致。

`oidc-federated-token` 卷中的文件是 Kubernetes 签发并轮换的 OIDC Token，Sidecar 读取后用于 IDaaS OIDC 联邦凭证认证。它的签发者（集群 Issuer）和受众（`audience`）须匹配控制台联邦信任源，ServiceAccount 主体须匹配应用联邦凭证；SDK 的 `applicationFederatedCredentialName` 指定所用联邦凭证。卷名和文件路径是本地约定，无需填入 IDaaS 控制台，也无需从控制台下载 Token。

挂载目录统一为 `/run/secrets/oidc-federated`，投射相对路径为 `token`；SDK 通过上述环境变量读取完整路径，不依赖旧目录名。采用独立目录的只读整卷挂载，不使用 `subPath`，以保留 kubelet 的 Token 轮换更新。

本地凭证接口默认要求认证，覆盖普通响应、raw 和缓存命中。Deployment 另投射 audience 为 `idaas.akless.sidecar` 的 Token 到 `/run/secrets/idaas-akless/credential-auth/token`，仅 sidecar 与指定业务容器只读挂载；IDaaS 引导 Token 仍仅给 sidecar。两个 Token 必须使用不同卷，禁止 subPath。`POD_UID`、`POD_NAMESPACE`、`POD_SERVICE_ACCOUNT` 由 Downward API 注入 sidecar，不需手填。

默认模板以非 root 用户运行。开启认证时，认证目录必须由 root 持有，并位于实际只读挂载的文件系统；认证文件也必须由 root 持有、组和其他用户不可写。支持模板声明的 `0440` 以及 kubelet 在 fsGroup 下生成的 `0640`。投射卷根目录可能为 `3777`，目录权限位不等于容器有写权限，程序以固定目录句柄的只读文件系统标志判断，不执行 chmod、写入或轮换。大小不超过 8KiB，允许卷内 kubelet 符号链接。启动入口在 Linux 上检查已固定的目录句柄和文件所有者；这些检查不等于证明卷类型，部署仍须使用标准 projected 卷。

**容器 UID 约束：**如果 Pod 内所有容器（包括 `initContainers` 中的原生 Sidecar）都显式使用相同的 `runAsUser`，或继承同一个 Pod 级 `runAsUser`，kubelet 会将投射的 ServiceAccount Token 文件归该 UID 所有。当前本地认证要求文件由 root 持有，因此不支持这个组合，启动时会拒绝该文件。默认模板的 Sidecar 使用 UID `10001`、业务使用 UID `1000`；接入自己的业务时应保留不同的非 root UID，并核对准入策略处理后的最终配置。`fsGroup` 调整组读权限，不能恢复 root 文件所有权；不要通过 chmod、复制 Token 或关闭认证绕过。平台行为见 [Kubernetes 投射卷安全上下文说明](https://kubernetes.io/docs/concepts/storage/projected-volumes/#securitycontext-interactions)。

本地认证卷 `credential-auth-token` 的 `expirationSeconds` 支持范围为 **600–3600 秒**，默认 3600。程序对**当前 Token 和旧 Token**都采用 `min(exp, iat+3600秒)` 作为有效截止时间；JWT 声明更长的 `exp` 也不会延长本地认证有效期。不要把该卷改为 7200 等更长时长：如果 kubelet 尚未换发文件，程序会在签发一小时后返回 `503`，即使 JWT 尚未达到 `exp`，也必须等到有效的新 Token 投射后才能恢复。此本地上限不会调整 kubelet 的轮换时机。独立的 `oidc-federated-token` 引导卷不受这项本地认证上限约束，应按 IDaaS 联邦配置要求设置。

开启认证时，启动时文件、挂载或 Pod 上下文无效则退出；运行中参考文件失效则取证返回503，原有 `/healthz` 仍只检查进程存活。认证文件每次取证及输出前重读，旧值宽限截止取以下时间最早值：首次观察到变化时间加60秒、新 Token 的 iat 加60秒、旧 Token 的 `min(exp, iat+3600秒)`。因此延迟投射或延迟观察可能使旧 Token 立即失效，客户端应重新读取文件；不验证文件之外的任意SA JWT。任何认证错误都不会自动转为匿名。

`deploy/rotation-integration-test.js` 根据新旧 Token 的 `iat/exp`、服务端允许的 30 秒签发时钟偏差和请求时间判断宽限。明确仍在宽限内时要求旧 Token 返回200，明确超过最晚截止时要求401；服务端首次观察时间未知或请求跨截止点时，允许200或401。最终在最晚宽限截止后再次验证旧 Token 为401、新 Token 为200，不再假定投射后必有完整60秒宽限。该脚本的离线回归命令为 `node --test deploy/rotation-integration-test.test.js`；离线替身不代替真实 kubelet 轮换验收。

### 默认端口与升级

默认监听端口从 `8080` 改为 `61765`，IP 仍固定为 `127.0.0.1`。`61765` 属于 [RFC 6335 的动态／私有范围](https://www.rfc-editor.org/rfc/rfc6335.html#section-8.1.2)，并避开 [Linux 默认临时端口范围 `32768–60999`](https://docs.kernel.org/networking/ip-sysctl.html#ip-variables)。它是可覆盖的本地默认值，不是 IANA 登记的专用端口，也不保证所有环境均空闲。非默认 Linux 临时端口范围及其他操作系统需按实际配置检查。

升级旧部署时，先显式设置 `IDAAS_AKLESS_SIDECAR_LISTEN_PORT=8080`，并保留两端 URI 与健康自检的旧端口；使用新模板时，可将共享 ConfigMap 键设为字符串 `"8080"`。切换到 `61765` 时，在同一 Pod 模板中同步更新监听、业务 URI 和探针；端口 ConfigMap 键可省略，业务代码与模板需使用匹配版本。不要只升级镜像而让旧业务 URI 仍指向 `8080`。配置在启动时读取，修改 ConfigMap 后必须重建 Pod。

端口占用会使服务报错退出，不自动递增、不随机换端口，也不同时监听旧端口。

### 高级配置：端口、超时和多角色

首次接入保留模板默认值。确有需要时更新相应 ConfigMap 键或容器环境变量，重建 Pod 后重新验收。

- **端口冲突**：使用仓库模板时，将 ConfigMap 的 `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` 改为可用端口，例如字符串 `"61865"`，并重建 Pod；两端与自检会使用相同端口，业务客户端据此构造 URI；未设置显式业务 URI 时无需另配。自行接入时同步设置 Sidecar 的端口和业务容器的完整 URI；若使用 AWS 等厂商的凭据 URI 变量，也同步更新。exec 自检会读取同一 Sidecar 环境变量，无需声明 Sidecar 的 `containerPort`。IP 固定为 `127.0.0.1`，不能配置为 `0.0.0.0`、Pod IP、主机名或 IPv6 地址。若使用 CLI `--listen-port=61865` 覆盖环境变量，须同时在 startup/readiness 的自检命令中添加相同的 `--listen-port=61865`。
- **上游较慢**：在 Sidecar 中设置 `IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT`，例如 `20s`。它控制一次上游取证的总超时，不是刷新周期，也不替代 SDK JSON 中的 HTTP 超时。增大时一起评估业务请求 deadline 和优雅关闭预算；Node.js 示例的 `IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS` 可按需调整，例如 `30000`。
- **多个角色**：将 ConfigMap 的 `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` 设为逗号分隔的角色列表，包含默认角色和业务所需角色；更新后重建 Pod。业务通过 `roleArn` 请求目标角色，仍须获得 IDaaS 授权。修改默认角色时应同步核对白名单；空白名单表示关闭本地角色限制。

配置文件路径与本地认证文件路径已有默认值，常规接入保持模板约定即可；覆盖参数见文首完整列表。

完整监听地址配置已移除：`--listen` 会报未知参数，`IDAAS_AKLESS_SIDECAR_LISTEN_ADDR` 不再读取。服务和健康自检都使用固定的 `127.0.0.1` 与校验后的端口；端口 `0`、超出范围的值和完整地址均被拒绝。

### 显式关闭本地认证

默认 ConfigMap 的 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` 为字符串 `"true"`，两端从同一个键读取。为 SDK 兼容或受控测试关闭时，先确认两端镜像支持该模式，再将该键改为字符串 `"false"`，应用 ConfigMap 并重建 Pod。保留原 Token 卷不会影响关闭模式，IDaaS 引导卷不可移除。

```yaml
data:
  IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED: "false"
```

上述为 ConfigMap 局部配置，修改时保留其余键。恢复开启时将该键设回字符串 `"true"`，应用 ConfigMap 并重建 Pod；生产环境推荐保持开启。若额外使用 CLI 覆盖，须核对两端最终模式一致。

独立运行可设置 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false`，或仅对服务端使用 `--credential-auth-enabled=false`；业务客户端也需显式配置对应模式。关闭后客户端不读取本地认证 Token 文件、不发送 Authorization、401 不重试。非法值启动/取证失败，CLI 显式值优先于环境变量；空环境变量沿用默认开启。

关闭仅跳过本地 Token 检查，不改变 Sidecar 的 IDaaS 引导认证、云角色授权、白名单、loopback 监听、响应格式或缓存校验。启动输出一次 WARN。任何可访问该 loopback 接口的进程都能请求已授权角色的 STS，业务 SSRF 可能因此泄漏凭据；角色白名单只能缩小影响，不能替代认证。关闭模式建议使用默认角色或明确白名单。`deploy/integration-test.js` 和轮换脚本用于开启模式的安全验收，关闭后不能沿用其认证拒绝断言。

配置在启动时读取，不支持热更新。修改 ConfigMap 后重新创建 Pod，并验收凭据链路。

HTTP 服务读取请求头及整个请求的预算均为 5 秒，空闲 keep-alive 连接在 30 秒后关闭；响应写入预算为上游刷新总超时再加 5 秒，调整刷新预算时自动同步，避免慢客户端无限占用连接。GET 凭据接口不需要请求体，读取预算不限制处理器等待共享刷新的时间。调用方仍需配置自己的总 deadline。刷新失败每次尝试记录一次原因，退避期的缓存命中不逐请求重复打印降级日志。

## Node.js 业务示例

| 环境变量 | 默认值 | 用途 |
| --- | --- | --- |
| `IDAAS_AKLESS_CREDENTIALS_URI` | 未设置时由共享端口构造，两者均省略时为 `http://127.0.0.1:61765/credentials` | 显式 URI 优先；仅支持 `http:`，请求默认格式 |
| `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | `61765` | 可选共享端口；仅在未设置完整业务 URI 时用于构造地址 |
| `IDAAS_AKLESS_CLOUD_ROLE_ARN` | 空 | 非空时覆盖 URI 的 `roleArn`；否则保留 URI 参数，再由 Sidecar 回退默认角色 |
| `IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS` | `15000` | 整次请求 deadline，1～2147483647 的整数毫秒 |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | `true` | 仅接受 `true` 或 `false`；需与 Sidecar 一致，关闭时不读取文件、不发送认证头 |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | `/run/secrets/idaas-akless/credential-auth/token` | 每次取证重新读取的认证文件；发送裸JWT，不能填入IDaaS引导文件 |
| `PORT` | `3000` | 示例 HTTP 服务端口 |

`IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` 未设置或为空字符串时，两端均使用默认路径；非空值必须为可信绝对路径。直接调用 Node.js `fetchJson` 时，显式传入空字符串作为 Token 文件参数仍为配置错误，不会回退默认路径。

本 HTTP 示例两种模式均仅允许 `http://127.0.0.1:<port>/credentials` 或 `http://[::1]:<port>/credentials`，禁代理与重定向，响应最多1MiB。开启认证时401最多重读并重试一次，两次尝试共用总deadline，其他失败不自动回退。它不读取厂商SDK变量。开启认证时，仅配置 Credentials URI 而不携带文件Token的SDK无法访问；接入方法见 [SDK 接入](../docs/SDK-INTEGRATION.zh-CN.md)，部署命令见 [部署指南](README.zh-CN.md)。

客户端 HTTP helper 能校验数字 IPv4/IPv6 loopback URI，但当前 Sidecar 固定只监听 `127.0.0.1`，接入时使用 IPv4 URI。HTTP deadline 从取证开始计时，文件读取完成后会检查是否超时；已开始的文件系统操作本身无法由该 AbortSignal 强制中断，不应将认证文件放在可能长时间阻塞的远程文件系统上。
