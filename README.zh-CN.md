# IDaaS Akless Sidecar

[English](README.md) | **简体中文**

全部指南见 [文档索引](docs/README.zh-CN.md)。

所有交付镜像的最终运行层统一采用固定 digest 的 Alibaba Cloud Linux 3.13.4；Node.js 示例和测试镜像保持 24.22.2，使用固定 digest 的 glibc 运行时制品。Go 编译阶段与最终运行操作系统分别管理。

首次接入请阅读 [客户接入指南](docs/CUSTOMER-INTEGRATION.zh-CN.md)，包含 IDaaS 控制台配置、公开镜像与源码构建、业务改造和逐步验收，并附独立客户部署模板。

部署文件均为占位模板，使用前填写自己的配置、角色、引导 audience 和镜像，不能直接部署未替换的模板。

CLI、环境变量、ConfigMap 键及 SDK 字段的名称与对应关系统一见 [参数命名与跨层映射](deploy/CONFIGURATION.zh-CN.md#参数命名与跨层映射)。

IDaaS Akless Sidecar 是运行在业务 Pod 内的临时凭据代理。它使用 Kubernetes ServiceAccount Token 向 IDaaS 认证，获取云角色临时凭据，通过本地 HTTP 接口供业务容器使用。业务无需集成 IDaaS SDK，也无需配置长期云 AK。

当前支持阿里云、AWS、腾讯云。Sidecar 使用 IDaaS Go Core SDK 获取引导身份，无需云产品 SDK；业务使用自己的产品 SDK 调用云 API。

本仓交付 Go Sidecar 程序与镜像构建文件、Kubernetes 部署模板、可运行的 Node.js 业务示例和 SDK 响应验证工具。IDaaS 负责身份与云角色授权，Sidecar 负责凭据获取、缓存和响应转换，业务负责调用云 API。适用于需要在 Kubernetes 中获取临时云凭据的任意语言应用。

```text
投射的 ServiceAccount Token → IDaaS Core SDK → IDaaS Access Token
    → Developer API → 云角色临时凭据
    → http://127.0.0.1:61765/credentials → 同 Pod 的业务容器 → 云 API
```

## 业务如何接入

1. 在 IDaaS 配置 M2M 应用、Kubernetes OIDC 联邦凭据、云账号角色及应用授权。
2. 将 Sidecar 与业务部署到同一 Pod，为 Sidecar 挂载 IDaaS 配置和投射的 ServiceAccount Token。
3. 业务通过 HTTP 获取凭据，或配置 SDK 凭据提供器。
4. 验证云身份、实际业务权限和凭据到期后的刷新。

| 云厂商 | 接入方式 | 业务容器配置 |
| --- | --- | --- |
| 阿里云 | 支持文件认证的自定义取证适配器 | 使用本仓认证 HTTP 客户端并注入产品 SDK；仅设置 URI 不足以访问 |
| AWS | 支持 Token File 的容器凭据提供器 | 同时设置凭据 URI 与 `AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`，逐版本验收 |
| 腾讯云 | HTTP 获取后映射到可刷新的 Credential/Provider | 读取 `tmpSecretId/tmpSecretKey/token`，按有效期刷新 |

使用 SDK 提供器时，需确认版本支持对应协议，且默认链没有被静态凭据等来源覆盖。使用通用 HTTP 时，业务负责更新产品 SDK 中的凭据，不能永久复用启动时取得的一份 STS。

代码与配置见 [SDK 接入方案](docs/SDK-INTEGRATION.zh-CN.md)；可运行示例见 [Node.js HTTP 业务示例](business-example/README.zh-CN.md)。

## 首次运行：部署示例并验证

以下命令从仓库根目录执行，以阿里云默认角色和仓库自带 Node.js 示例跑通链路。需要 Docker/Buildx、容器仓库、Kubernetes 1.33+ 及已配置的 `kubectl`；默认模板采用原生 Sidecar。镜像构建不要求本机安装 Go。IDaaS 侧需已准备 M2M 应用、OIDC 联邦凭据和获得应用授权的阿里云角色。

### 1. 确认集群和联邦身份

```bash
kubectl config current-context
kubectl get nodes -o custom-columns=NAME:.metadata.name,ARCH:.status.nodeInfo.architecture
```

确认输出为目标集群，并记录节点架构。示例命名空间为 `idaas-akless`、ServiceAccount 为 `idaas-akless-demo`，IDaaS 信任的主体应为 `system:serviceaccount:idaas-akless:idaas-akless-demo`。引导 Token 的 audience 必须与 IDaaS 信任配置一致，不能保留占位符；本地认证 Token 使用独立固定受众。

### 2. 构建并推送镜像

版本发布和 amd64/arm64 混合集群使用 [GitHub Actions 镜像发布](docs/PUBLISHING-IMAGES.zh-CN.md)，一次推送双架构索引并验证两平台运行结果。下列命令仅用于本地单架构验证，不能对同一 tag 分别执行两种架构的推送，否则后一次会覆盖前一次。

```bash
export ACR_REGISTRY='<your-registry>'
export ACR_NS='<your-namespace>'
export IMG_TAG='<your-version>'
export IMG_ARCH=linux/amd64
docker login "$ACR_REGISTRY" --username='<your-user>'
./deploy/build-images.sh
```

仅部署 arm64 节点时，将 `IMG_ARCH` 改为 `linux/arm64`。成功后脚本输出两个镜像引用：

```text
<registry>/<namespace>/idaas-akless-sidecar:<version>
<registry>/<namespace>/idaas-akless-business-example:<version>
```

### 3. 填写配置与镜像引用

| 修改位置 | 填写内容与检查结果 |
| --- | --- |
| `deploy/configmap.yaml` 的 `data` 下的 `idaas-akless-client-config.json` | 用本租户控制台导出的完整 JSON 替换占位配置，确认实例、M2M 应用、联邦凭据名及 endpoint 均正确 |
| JSON `oidcTokenFilePath` | 留空；`oidcTokenFilePathEnvVarName` 为 `OIDC_TOKEN_FILE_PATH`，对应投射文件 `/run/secrets/oidc-federated/token` |
| ConfigMap `IDAAS_AKLESS_DEFAULT_ROLE_ARN` | 填入已授权的阿里云角色外部 ID |
| ConfigMap `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` | 本地角色白名单，示例包含默认角色 |
| ConfigMap `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | 保持字符串 `"true"` 开启本地 Token 认证，两个容器共用此项 |
| ConfigMap `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | 模板显式列为字符串 `"61765"`，监听、自检与业务客户端共用此项；可省略，不配时仍使用 `61765` |
| `deploy/deployment.yaml` 的两个 `image` | 分别填入上一步推送的完整镜像引用 |
| Deployment `oidc-federated-token` 投射受众 | 与 IDaaS 联邦受众完全一致；独立本地认证受众 `idaas.akless.sidecar` 保持不变 |

YAML 不会展开本机的 `$ACR_REGISTRY` 等变量，`image` 中应填写实际值。保留 Token 和配置挂载、Sidecar loopback 监听及 exec 探针。完整参数及优先级见 [配置契约](deploy/CONFIGURATION.zh-CN.md)。

### 4. 应用清单

```bash
kubectl apply -f deploy/namespace.yaml
```

私有仓库需要在 `idaas-akless` 命名空间配置 `acr-secret`，具体命令见 [部署指南](deploy/README.zh-CN.md#4-应用)。若已使用 ACK ACR 凭据助手或公开仓库，按实际情况删除 `imagePullSecrets`。完成拉取凭证配置后执行：

```bash
bash deploy/validate-manifests.sh
kubectl apply -f deploy/serviceaccount.yaml -f deploy/configmap.yaml -f deploy/deployment.yaml
kubectl -n idaas-akless rollout status deployment/akless-demo --timeout=180s
kubectl -n idaas-akless get pods -l app=akless-demo
```

预期校验成功、rollout 完成、Pod 为 `2/2 Running`。失败时先查看 `kubectl -n idaas-akless describe pod <pod-name>` 的事件，检查镜像拉取、配置和探针。

### 5. 验证服务与有效凭据

将 `POD_NAME` 设为上一步新部署的 Pod 名，避免滚动更新时验证到其他版本：

```bash
export POD_NAME='<new-pod-name>'
kubectl -n idaas-akless exec "$POD_NAME" -c idaas-akless-sidecar -- \
  /usr/local/bin/idaas-akless-sidecar --healthcheck
kubectl -n idaas-akless logs "$POD_NAME" -c business --tail=10
```

自检成功时无输出且退出码为 0；示例成功时日志包含 `[ok] got STS credentials from sidecar, expires at ...`。原生 Sidecar 的启动探针会等待本地监听可用后再启动业务，但上游 IDaaS 仍可能暂时不可用。示例仅启动时请求一次，失败时可用下方命令重新取凭据验证，不必依赖启动日志。

下面命令检查 HTTP 状态、阿里云完整三元组及有效期，只输出校验结果和到期时间：

```bash
kubectl -n idaas-akless exec -i "$POD_NAME" -c business -- node --input-type=module <<'JS'
try {
  const { fetchJson, configuredCredentialsURI } = await import('/app/credentials-client.js');
  const uri = configuredCredentialsURI();
  const c = await fetchJson(uri, 15000);
  const complete = c.Code === 'Success' &&
    [c.AccessKeyId, c.AccessKeySecret, c.SecurityToken].every(v => typeof v === 'string' && v.length > 0);
  if (!complete || !(Date.parse(c.Expiration) > Date.now())) throw new Error('Invalid credential');
  console.log(JSON.stringify({ credentialValid: true, expiration: c.Expiration }));
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
JS
```

预期输出 `{"credentialValid":true,"expiration":"<未来时间>"}`，命令退出码为 0。`400` 检查角色与参数，`403` 检查本地白名单，`502` 查看 Sidecar 日志排查认证、上游连通性或角色授权。

`401` 或 `503` 检查本地 Token 挂载、读权限、Pod 绑定、过期与轮换；不要通过匿名重试绕过失败。

### 6. 验证真实云身份与权限

按 [部署验收命令](deploy/README.zh-CN.md#5-验收) 使用取得的 STS 调用 `aliyun sts GetCallerIdentity`，核对返回的 assumed-role 账号和角色；再调用一项业务实际需要的云 API，检查返回结果符合预期。服务就绪、取到凭据和云 API 授权成功是三项独立检查。

## 接入自己的业务

跑通示例后，用自己的应用容器替换 Deployment 的 `business` 容器，保留 Sidecar、配置卷与两个独立 Token 卷、对应只读挂载和 ServiceAccount。移除示例专用的 3000 端口、Node 用户配置和 `/healthz` 存活探针，改用自己应用的端口、安全设置与健康检查。

- 通用 HTTP：在业务容器配置 `IDAAS_AKLESS_CREDENTIALS_URI` 并主动请求，按厂商字段更新产品 SDK 凭据。
- SDK 提供器：将业务代码初始化为可刷新的默认凭据链，并设置对应变量；本仓 Node.js 示例使用通用 HTTP，不会读取 SDK 提供器变量。

例如 AWS 业务容器增加：

```yaml
env:
  - name: AWS_CONTAINER_CREDENTIALS_FULL_URI
    value: http://127.0.0.1:61765/credentials
  - name: AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE
    value: /run/secrets/idaas-akless/credential-auth/token
```

同时将 Sidecar 默认角色改为已授权的 AWS 角色。阿里云需使用支持文件认证的适配器，不能只设置 `ALIBABA_CLOUD_CREDENTIALS_URI`；腾讯云通过认证 HTTP 映射并实现刷新。完整接入示例及安全验证见 [SDK 接入方案](docs/SDK-INTEGRATION.zh-CN.md)。

默认开启本地认证，所有凭据请求（包括raw和缓存命中）都必须携带 `Authorization: <本地认证 JWT>`，不添加Bearer。认证文件由kubelet投射并轮换；默认路径为 `/run/secrets/idaas-akless/credential-auth/token`。缺失/错误为401，认证头非法为400，参考文件失效为503；认证失败不会自动转为匿名。使用默认模板的只读挂载及fsGroup，不为业务挂载引导卷。

修改容器 UID 时需保留本地认证文件的 root 所有权；所有容器（含原生 Sidecar）显式使用相同 `runAsUser` 会改变投射文件所有者，当前实现不支持。具体见 [容器 UID 约束](deploy/CONFIGURATION.zh-CN.md#kubernetes-配置映射)。

兼容或测试场景可显式配置 `--credential-auth-enabled=false` 或 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false`，仅接受 `true/false`。Node.js 示例使用同名环境变量；默认 ConfigMap 的 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` 为字符串 `"true"`，两个容器共用此项；修改后应用 ConfigMap 并重建 Pod。关闭后不读取本地认证文件，但仍要求 Sidecar 向 IDaaS 认证，并限制 loopback 和角色权限；启动输出 WARN，本地 Token 的 SSRF 凭据保护不再生效。生产推荐保持开启，具体步骤见 [配置契约](deploy/CONFIGURATION.zh-CN.md#显式关闭本地认证)。

## HTTP 接口

| 接口 | 用途 |
| --- | --- |
| `GET /credentials` | 默认角色的临时凭据 |
| `GET /credentials?roleArn=<URL编码后的角色外部ID>` | 指定角色的临时凭据 |
| `GET /credentials?format=raw` | Sidecar 声明的 Developer API 响应字段 |
| `GET /healthz` | 本地 HTTP 服务可响应时返回 `ok` |

`roleArn` 省略或为空时使用默认角色。角色外部 ID 不要求 ARN 前缀，厂商由上游响应决定，权限由 IDaaS 校验。完整字段、raw 范围及错误码见 [响应契约](docs/CREDENTIALS-RESPONSE.zh-CN.md)。

## 缓存与超时

- 按角色缓存并合并刷新，由请求触发；刷新点为取回凭据时剩余寿命的 4/5，寿命允许时会提前到足以预留“后台返回门槛 + 一次刷新预算”的位置。
- 到达刷新点或已有刷新任务时，旧凭据剩余有效期超过门槛即可立即返回，同角色仅运行一次后台刷新；没有缓存、已经过期或达到临期门槛时等待共享结果。门槛默认 5 分钟，通过 `--credential-refresh-min-validity` 或 `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY` 调整，应与业务 SDK 的提前刷新窗口匹配。
- 刷新失败可返回仍有效的旧凭据，不返回过期凭据；失败完成后退避 10 秒。上述门槛只决定刷新时是否等待，不为上游新凭据或原有失败兜底增加最小寿命限制，详见 [配置契约](deploy/CONFIGURATION.zh-CN.md#sidecar-启动参数)。
- 最多保留 64 个角色条目，容量已满时回收空闲、无有效凭据且不在退避中的条目。
- 上游刷新默认预算为 10 秒；客户端取消会结束本次请求的等待，共享刷新继续受独立预算约束，业务需设置整次请求 deadline。
- HTTP 优雅关闭预算默认是刷新预算加 5 秒，即默认 15 秒；`--shutdown-timeout` / `IDAAS_AKLESS_SHUTDOWN_TIMEOUT` 支持 `auto` 或更长时长。Pod 终止宽限需覆盖业务退出及 Sidecar drain，超过关闭预算会关闭剩余连接，详见 [配置契约](deploy/CONFIGURATION.zh-CN.md#sidecar-启动参数)。
- HTTP 请求头和请求读取超时均为 5 秒，空闲 keep-alive 连接在 30 秒后关闭，响应写入预算为刷新总超时再加 5 秒。共享刷新失败每次尝试记录一次，等待请求和退避响应不重复打印失败日志；panic 另记录脱敏代码位置，缓存容量不足等其他请求错误单独记录。

## 安全与验收

Sidecar 的监听 IP 固定为 `127.0.0.1`，默认端口 `61765`；端口冲突时可通过 `--listen-port` 或 `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` 调整，并同步业务 URI 与健康自检配置。模板两端可选引用同一个端口键，缺键或空值均默认使用 `61765`；业务优先使用显式 URI，否则按共享端口构造地址；旧部署须显式保留 `8080` 或同步迁移，见 [升级说明](deploy/CONFIGURATION.zh-CN.md#默认端口与升级)。同 Pod 容器共享应用身份，应只部署互信容器。使用专用 ServiceAccount 和最小权限云角色；可选白名单提供额外本地限制。ServiceAccount 无需 Kubernetes API 权限，业务容器只读挂载独立受众的本地认证 Token，不挂载 IDaaS 配置或引导 Token。

不要将临时 AK、SK、Token 写入日志或配置文件。`/healthz` 和 Pod Ready 只证明基础运行状态，凭据请求也可能命中缓存；验收应核对实际云身份并调用业务所需的云 API。

## 开发验证

本地编译需要 Go 1.24+（使用受限目录文件访问），Node.js 示例需要 Node.js 24+。`make build` 生成 `bin/idaas-akless-sidecar`。默认开启认证，运行还需受信本地投射文件及由Downward API注入的Pod上下文；显式关闭仅跳过本地认证，不改变 IDaaS 引导身份要求。

`bin/idaas-akless-sidecar --version` 无需运行配置即可输出 version/revision。发行构建使用 `make build VERSION=<发行版本> REVISION=<源码 commit>`；本地默认版本为 `dev`，revision 为 Git 描述，已跟踪文件有修改时带 `-dirty`。Docker 构建支持 `VERSION`、`REVISION` 两个 build argument，默认 `dev`/`unknown`；镜像脚本使用 `IMG_TAG` 和 Git 描述，也可显式传入 `REVISION`。

```bash
go test -race ./...
go vet ./...
make lint
python3 -m unittest discover -s deploy -p 'test_*.py'
node --test deploy/rotation-integration-test.test.js
npm test --prefix business-example
```

`make lint` 下载并使用固定的 golangci-lint v2.6.2 和 Go 1.24.6，按最低支持的 Go 版本系列检查，不依赖本机安装的 lint 版本或默认 Go 版本。镜像脚本测试使用 Docker 命令替身，不构建或推送镜像。

`cmd/` 负责启动与参数，`internal/credentials/` 负责获取、缓存及转换，`internal/health/` 负责健康检查，`internal/projectedauth/` 负责受信本地 Token 投射、Pod 绑定与轮换宽限。实现入口见 [源码对应关系](docs/README.zh-CN.md#源码对应关系)，当前响应的离线 SDK 检查见 [验证说明](verification/sdk-contracts/README.zh-CN.md)。

阿里云 Node.js SDK 的真实原生 URI 取证及可选刷新验证见 [独立集成测试](verification/native-uri/README.zh-CN.md)。长期测试的统一入口、环境要求和验证边界见 [测试说明](docs/TESTING.zh-CN.md)。

`make check` 一次执行完整离线回归；`make check-linux` 另用本地临时容器验证 Linux 只读挂载和非 root 文件权限，不需要云账号。

三云 STS、Credentials URI 接入和真实资源查询见 [业务端集成测试](business-example/multicloud/README.zh-CN.md)。

候选内容扫描、Git 历史脱敏及公开源码/镜像验收见 [发布检查](docs/RELEASING.zh-CN.md)。

许可证：Apache-2.0，见 [LICENSE](LICENSE)。
运行镜像在 `/usr/share/licenses/idaas-akless-sidecar/` 中附带项目许可证及 [二进制依赖许可证](licenses/README.zh-CN.md)。
