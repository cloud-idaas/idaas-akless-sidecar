# 使用 IDaaS Akless Sidecar 为 Kubernetes 应用获取临时云凭据

[English](CUSTOMER-INTEGRATION.md) | **简体中文**

IDaaS Akless Sidecar 与业务容器运行在同一个 Pod 中。Sidecar 使用 Kubernetes ServiceAccount 的 OIDC Token 向 IDaaS 认证，获取已授权云角色的临时凭据；业务通过本地 HTTP 接口取证，再使用云产品 SDK 访问资源。业务无需保存长期 AccessKey 或 IDaaS Client Secret，也无需集成 IDaaS SDK。

本文先以阿里云角色跑通一个不依赖业务 SDK 的示例，再说明如何接入已有业务。当前 Sidecar 支持阿里云、AWS 和腾讯云临时凭据；各语言 SDK 的自动刷新和认证能力需要单独验证，不能仅凭配置凭据 URI 判断接入成功。

配套附件：[客户部署模板](examples/customer-deployment.yaml)。请使用同一发布版本的正文、附件和镜像。

源码仓库：[cloud-idaas/idaas-akless-sidecar](https://github.com/cloud-idaas/idaas-akless-sidecar)。版本标签和公开镜像引用尚待发布确认，本文用 `<...>` 标注待填写的发行信息。源码自建路径可用于验证；仓库 `deploy/` 仅提供占位模板，不含可直接使用的租户配置或发布镜像。发布前检查见 [发布说明](RELEASING.zh-CN.md)。

## 接入流程与改造范围

```text
Kubernetes 投射引导 Token → Sidecar → IDaaS M2M 认证 → PAM 获取云角色 STS
业务容器 + 本地认证 Token → Sidecar /credentials → 临时 AK + SK + 安全令牌
业务容器 + 临时凭据 → 云产品 API
```

| 操作方 | 需要完成的配置 | 完成标志 |
| --- | --- | --- |
| IDaaS / 云账号管理员 | 托管云账号与角色；创建 M2M 客户端、OIDC 信任源和联邦凭证；授予 PAM 功能权限与角色数据权限 | 账号、角色可用，应用与目标角色授权关系正确 |
| Kubernetes 管理员 | 获取集群 Issuer/JWKS；配置专用 ServiceAccount、两份投射 Token、Sidecar 和只读挂载 | 新 Pod 启动，业务能认证取证 |
| 业务开发者 | 使用可持续取证的 SDK Provider，或实现认证 HTTP 取证、字段映射和刷新 | 以目标角色调用实际业务 API，跨凭据到期后持续成功 |

Sidecar 的配置不替代云角色权限策略，也不会自动修改业务 SDK 的凭据来源。下文命令在本地 Bash 终端执行，特别标注的代码通过 `kubectl exec` 在容器内运行。所有 `<...>` 都需要替换为真实值。

## 一、准备接入环境

需要准备：

- 已开通机器身份管理（M2M）和特权管理（PAM）能力的 IDaaS EIAM 实例，以及对应管理权限。
- Linux Kubernetes 集群，本文模板使用 Kubernetes 1.33+ 的原生 Sidecar；节点也应支持该特性。旧集群不直接使用本文模板。参考 [Kubernetes Sidecar 容器](https://kubernetes.io/docs/concepts/workloads/pods/sidecar-containers/)。
- 本地 `kubectl`、Git、Python 3；源码构建镜像时另需 Docker Engine 和 Buildx。仅拉取公开镜像不需要安装 Go。
- Pod 能解析并通过 HTTPS 访问本实例的 IDaaS Token 端点和 Developer API；业务能访问目标云产品端点。镜像拉取由节点访问相应镜像仓库。
- 一个专用于验证的命名空间和 ServiceAccount。本文使用 `akless-example` / `akless-business`，请勿在已有同名业务资源上直接执行示例。

首先确认目标集群、版本和节点架构：

```bash
kubectl config current-context
kubectl version -o yaml
kubectl get nodes -o custom-columns=NAME:.metadata.name,VERSION:.status.nodeInfo.kubeletVersion,ARCH:.status.nodeInfo.architecture,OS:.status.nodeInfo.operatingSystem
```

**检查结果：**上下文为目标集群，服务端及节点符合要求，节点为 Linux，记录 `amd64` 或 `arm64`。混合架构集群应使用覆盖所有调度目标架构的镜像，或在 Pod 中配置 `nodeSelector` 限定架构。

### 获取指定版本的源码与文档附件

从项目公开发布页获取以下信息：GitHub 仓库 HTTPS 地址、源码版本标签、公开 Sidecar 镜像及其 digest。本文使用变量承接发布信息，镜像来源可选公开 ACR 或 Docker Hub；两者的地址不能互相推导。

```bash
export AKLESS_GIT_URL='https://github.com/cloud-idaas/idaas-akless-sidecar.git'
export AKLESS_VERSION='<源码版本标签>'
git clone --branch "$AKLESS_VERSION" --depth 1 "$AKLESS_GIT_URL" idaas-akless-sidecar
cd idaas-akless-sidecar
export AKLESS_WORK="$(mktemp -d "${TMPDIR:-/tmp}/idaas-akless.XXXXXX")"
export AKLESS_NAMESPACE=akless-example
```

**检查结果：**当前目录包含 `Dockerfile`、`cloud_idaas.json.example`、`business-example/credentials-client.js` 和 `docs/examples/customer-deployment.yaml`。工作目录位于仓库外，使用系统临时目录并限制为当前用户访问。后续命令均从这个仓库根目录执行；关闭终端后需恢复这些变量；系统可能清理临时目录，重新开始时应重新生成配置。

首次验证请保持本文的命名空间和 ServiceAccount 名称。若改为其他值，必须同步修改 IDaaS 应用联邦凭证中的命名空间/服务账号，以及 Deployment 的 `serviceAccountName`；仅修改 `AKLESS_NAMESPACE` 不会自动改变信任条件。

## 二、配置 IDaaS 和目标云角色

### 2.1 托管云账号和业务角色

登录 [IDaaS 管理控制台](https://yundun.console.aliyun.com/?p=idaas)，进入目标 EIAM 实例的管理控制台。

1. 进入 **资产管理 > 云身份**，单击 **添加云账号**，按当前账号或其他账号选择接入方式。
2. 按页面引导完成 RAM 身份提供商、系统云角色与权限策略配置，并运行页面检测。
3. 在该云账号的 **云角色管理** 中添加业务需要使用的 RAM 角色，按引导配置角色信任策略，并仅授予目标资源上所需的操作权限。
4. 记录目标角色的**外部 ID**。阿里云角色通常为 `acs:ram::<account-id>:role/<role-name>`，后续作为 `IDAAS_AKLESS_DEFAULT_ROLE_ARN` 使用；不要填写 IDaaS 内部的云角色 ID。

**检查结果：**云账号及系统角色可用，业务角色启用且可用；角色权限是业务需要的权限。业务使用的是目标角色，不是用于账号管理的系统角色。账号接入细节见 [云身份管理](https://help.aliyun.com/zh/idaas/eiam/user-guide/cloud-identity-management)。

### 2.2 获取 Kubernetes Issuer 与验签公钥

在可以访问该集群且有权限读取相关端点的终端执行：

```bash
kubectl get --raw /.well-known/openid-configuration > "$AKLESS_WORK/k8s-discovery.json"
kubectl get --raw /openid/v1/jwks > "$AKLESS_WORK/k8s-jwks.json"
python3 - "$AKLESS_WORK" <<'PY'
import json, pathlib, sys
p = pathlib.Path(sys.argv[1])
d = json.loads((p / "k8s-discovery.json").read_text())
j = json.loads((p / "k8s-jwks.json").read_text())
assert d["issuer"].startswith("https://"), "IDaaS OIDC 信任源要求 HTTPS Issuer"
assert j.get("keys"), "JWKS 不能为空"
print("Kubernetes Issuer:", d["issuer"])
print("JWKS key count:", len(j["keys"]))
PY
```

**检查结果：**两个命令和 Python 校验均成功，记录输出的 Kubernetes Issuer。若返回 403，请由集群管理员导出相同信息；不要通过关闭认证或暴露 API Server 解决。

### 2.3 创建 OIDC 联邦信任源

在 IDaaS 实例控制台进入 **登录 > 认证配置 > 联邦信任源管理**，新增 OIDC 类型的信任源。本文选择静态 JWKS，配置如下：

| 字段 | 填写值 |
| --- | --- |
| 联邦信任源名称 | 例如 `akless-k8s` |
| 信任来源 | 静态填充 |
| Issuer | 上一步集群 Discovery 中的 `issuer`，原样填写 |
| 受众标识 | `urn:example:idaas:bootstrap`，与本文模板中引导 Token 的 audience 完全一致 |
| 验签公钥 | 本地 `k8s-jwks.json` 的完整 JSON 内容 |

**检查结果：**信任源已保存且启用。这里的 Issuer 是 **Kubernetes 的签发者**；后续 SDK JSON 的 `issuer` 是 **IDaaS 授权服务器**，两者用途不同。若更换引导 audience，必须同时修改本节信任源和 Deployment 的 `oidc-federated-token` 卷。

静态 JWKS 不要求 IDaaS 访问集群 API Server，但集群签名密钥轮换时须同步更新公钥集，并在切换前验证。也可根据集群条件使用自动发现或 JWKS URI，具体字段见 [联邦信任源管理](https://help.aliyun.com/zh/idaas/eiam/user-guide/federated-credential-provider-management)。

### 2.4 创建 M2M 客户端与应用联邦凭证

1. 进入 **应用管理 > M2M 应用管理**，添加业务客户端应用；若页面提供应用类型，选择支持 OIDC 与 M2M 的类型。
2. 记录该应用的 `client_id`、IDaaS 授权服务器 Issuer、令牌端点以及实例 ID。授权服务器信息使用应用配置或对应 Discovery 的实际值。
3. 在 **通用配置 > 认证管理 > OIDC** 中新增联邦凭证。
4. 选择上一步信任源，联邦凭证名称填写 `akless-k8s-credential`，校验条件选择 **kubernetes 模式**，命名空间填 `akless-example`，服务账号填 `akless-business`。

**检查结果：**应用联邦凭证存在，主体为 `system:serviceaccount:akless-example:akless-business`；记录的凭证名称与后续 JSON 中 `applicationFederatedCredentialName` 一致。本方案不需要创建 Client Secret。字段说明见 [创建联邦凭证](https://help.aliyun.com/zh/idaas/eiam/user-guide/create-application-federated-credential)。

部署模板中的 `oidc-federated-token` 卷与这里的 OIDC 联邦凭证配合使用：Kubernetes 签发并轮换 Token，Sidecar 从 `/run/secrets/oidc-federated/token` 读取后提交给 IDaaS。IDaaS 按所选信任源的 Issuer、公钥和受众，以及联邦凭证的 ServiceAccount 条件验证它。卷名和文件路径是容器内的约定，无需填入控制台，也无需从控制台下载 Token。

### 2.5 为应用授予 PAM 功能权限和目标角色数据权限

先在 **权限中心 > 功能权限管理** 中找到 `Privileged Access Management`，进入 **授权管理 > 应用授权**，将“获取云角色访问凭证（`cloud_account_role:obtain_access_credential`）”授予上述 M2M 客户端。本文 SDK 配置只申请对应 scope：

```text
urn:cloud:idaas:pam|cloud_account_role:obtain_access_credential
```

再进入 **权限中心 > 数据权限管理**，新增授权规则。在该规则的 **权限管理** 中：

1. 在 **关联身份** 页签添加上述 M2M 客户端应用，确认授权有效期覆盖业务运行时间。
2. 在 **资产管理** 页签添加 2.1 节的目标业务云角色。

**检查结果：**同一个客户端同时具备功能权限和目标角色的数据权限。仅授予其中一项无法完成取证；修改本地白名单也不会产生 IDaaS 授权。参见 [数据权限管理](https://www.alibabacloud.com/help/zh/idaas/eiam/user-guide/data-permission-management) 和 [原生 Kubernetes 接入流程](https://help.aliyun.com/zh/idaas/eiam/use-cases/credential-free-k8s-sts-token)。

## 三、获取 Sidecar 和业务示例镜像

以下两种方式任选其一。镜像必须与所用源码/模板版本对应。

### 方式 A：使用公开镜像

从公开发布页复制完整镜像引用，推荐使用 digest：

```bash
export SIDECAR_IMAGE='<公开 ACR 或 Docker Hub 镜像地址>@sha256:<发布的 digest>'
export BUSINESS_IMAGE='<同版本 ALinux 业务示例镜像地址>@sha256:<发布的 digest>'
```

具有 Docker 的终端可提前核对架构并试拉：

```bash
docker buildx imagetools inspect "$SIDECAR_IMAGE"
docker buildx imagetools inspect "$BUSINESS_IMAGE"
docker pull --platform linux/amd64 "$SIDECAR_IMAGE"
docker pull --platform linux/amd64 "$BUSINESS_IMAGE"
```

arm64 环境将 `--platform` 改为 `linux/arm64`。不安装 Docker 时，可在第五节通过 Pod 事件和 `imageID` 核对实际拉取结果。公开镜像无需为本示例配置 `imagePullSecrets`；仍需确认集群节点能够访问仓库。

### 方式 B：下载源码后构建镜像

在已检出指定版本的仓库根目录执行；将目标镜像换成您有推送权限、节点可拉取的仓库地址：

```bash
export SIDECAR_IMAGE="<您的 registry>/<namespace>/idaas-akless-sidecar:$AKLESS_VERSION"
export BUSINESS_IMAGE="<您的 registry>/<namespace>/idaas-akless-business-example:$AKLESS_VERSION"
docker login '<您的 registry>'
docker buildx build --platform linux/amd64 \
  --build-arg "VERSION=$AKLESS_VERSION" --build-arg "REVISION=$(git rev-parse HEAD)" \
  -f Dockerfile -t "$SIDECAR_IMAGE" --push .
docker buildx build --platform linux/amd64 \
  -f business-example/Dockerfile -t "$BUSINESS_IMAGE" --push business-example
docker buildx imagetools inspect "$SIDECAR_IMAGE"
docker buildx imagetools inspect "$BUSINESS_IMAGE"
```

构建 arm64 时改为 `linux/arm64`；需要多架构时，在支持多架构构建的 Buildx builder 上使用 `--platform linux/amd64,linux/arm64`。两个镜像标签中的版本均使用 `AKLESS_VERSION`；Sidecar 构建参数会嵌入该源码版本及检出的提交号，省略时为 `dev`／`unknown`。Dockerfile 在构建阶段下载 Go 模块并编译，运行镜像不需要 Go 环境。

**检查结果：**构建推送成功，两个远端 manifest 均覆盖目标节点架构。记录两个输出 digest，并将 `SIDECAR_IMAGE` 和 `BUSINESS_IMAGE` 改为 `<镜像仓库>@sha256:<digest>` 后继续。仅在本机 `docker build` 成功不能保证集群节点能拉取镜像。

若使用私有仓库，在第四节创建命名空间后，按 4.4 节配置拉取凭据。不要将长期拉取密码写入本仓库。示例默认使用公开镜像，因此不引用测试环境的拉取 Secret。

## 四、填写 Sidecar 配置并准备示例

### 4.1 创建 SDK 配置文件

```bash
cp cloud_idaas.json.example "$AKLESS_WORK/client-config.json"
```

编辑复制出的文件。下列为本方案的完整配置结构；替换所有 `<...>`，保留 Token 文件路径约定，并将 `scope` 设为本节给出的最小值：

```json
{
  "idaasInstanceId": "<IDaaS 实例 ID>",
  "clientId": "<M2M 客户端 client_id>",
  "issuer": "<IDaaS 授权服务器 Issuer，HTTPS 完整 URL>",
  "tokenEndpoint": "<IDaaS 令牌端点，HTTPS 完整 URL>",
  "scope": "urn:cloud:idaas:pam|cloud_account_role:obtain_access_credential",
  "developerApiEndpoint": "<本实例地域的 Developer API 域名，不含路径>",
  "authnConfiguration": {
    "identityType": "CLIENT",
    "authnMethod": "OIDC",
    "applicationFederatedCredentialName": "akless-k8s-credential",
    "clientDeployEnvironment": "KUBERNETES",
    "oidcTokenFilePath": "",
    "oidcTokenFilePathEnvVarName": "OIDC_TOKEN_FILE_PATH"
  },
  "httpConfiguration": {
    "connectTimeout": 5000,
    "readTimeout": 10000
  }
}
```

Developer API 域名使用实例所在地域的实际端点，例如杭州地域的 `eiam-developerapi.cn-hangzhou.aliyuncs.com`。SDK 会规范化域名为 HTTPS。`issuer`、`tokenEndpoint` 使用 IDaaS 应用对应配置，不能填入 Kubernetes Issuer。不要将模板中的 `${...}` 字面占位符留在配置中。

`oidcTokenFilePath` 的非空值优先于环境变量；本方案留空，由 `OIDC_TOKEN_FILE_PATH=/run/secrets/oidc-federated/token` 指定引导 Token。`connectTimeout` / `readTimeout` 单位为毫秒；Sidecar 获取 IDaaS Token 和调用 Developer API 的共享刷新总超时默认 `10s`，首次接入无需另行配置。

```bash
python3 - "$AKLESS_WORK/client-config.json" <<'PY'
import json, pathlib, sys
s = pathlib.Path(sys.argv[1]).read_text()
c = json.loads(s)
assert "<" not in s and "${" not in s, "请替换全部占位符"
assert c["issuer"].startswith("https://") and c["tokenEndpoint"].startswith("https://")
a = c["authnConfiguration"]
assert (a["identityType"], a["authnMethod"], a["clientDeployEnvironment"]) == ("CLIENT", "OIDC", "KUBERNETES")
assert a.get("oidcTokenFilePath", "") == "" and a["oidcTokenFilePathEnvVarName"] == "OIDC_TOKEN_FILE_PATH"
assert c["scope"] == "urn:cloud:idaas:pam|cloud_account_role:obtain_access_credential"
print("配置格式检查通过；身份与授权仍须通过真实取证验证")
PY
```

### 4.2 创建专用资源和 ConfigMap

设置角色外部 ID，并通过 `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` 配置白名单；示例将白名单设为相同角色。本地认证开关保持字符串 `"true"`。端口显式列为 `61765`，也可省略对应配置项，省略时两端仍使用默认端口。多角色配置见 [高级配置](../deploy/CONFIGURATION.zh-CN.md#高级配置端口超时和多角色)：

```bash
export AKLESS_ROLE='<2.1 节记录的阿里云业务角色外部 ID>'
python3 - <<'PY'
import os
r = os.environ["AKLESS_ROLE"]
assert r.startswith("acs:ram::") and ":role/" in r and "<" not in r, "请填写实际阿里云角色外部 ID"
print("角色外部 ID 格式检查通过")
PY
kubectl create namespace "$AKLESS_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$AKLESS_NAMESPACE" create serviceaccount akless-business \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$AKLESS_NAMESPACE" create configmap akless-settings \
  --from-file=idaas-akless-client-config.json="$AKLESS_WORK/client-config.json" \
  --from-literal=IDAAS_AKLESS_DEFAULT_ROLE_ARN="$AKLESS_ROLE" \
  --from-literal=IDAAS_AKLESS_ALLOWED_ROLE_ARNS="$AKLESS_ROLE" \
  --from-literal=IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=true \
  --from-literal=IDAAS_AKLESS_SIDECAR_LISTEN_PORT=61765 \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$AKLESS_NAMESPACE" create configmap akless-demo-code \
  --from-file=business-example/app.js \
  --from-file=business-example/runtime.js \
  --from-file=business-example/credentials-client.js \
  --dry-run=client -o yaml | kubectl apply -f -
```

**检查结果：**命令成功，资源位于同一命名空间。此处 ConfigMap 只包含 OIDC 接入配置和角色标识，不放 Client Secret、长期 AK 或任何 Token。示例用 ConfigMap 挂载 Node.js 代码以便免构建验证；接入生产业务时将代码打进自己的镜像。

### 4.3 生成客户部署清单

```bash
kubectl set image --local -f docs/examples/customer-deployment.yaml \
  idaas-akless-sidecar="$SIDECAR_IMAGE" business="$BUSINESS_IMAGE" \
  -o yaml > "$AKLESS_WORK/deployment.yaml"
```

打开生成的 `deployment.yaml`，核对以下约定。此模板独立于仓库的测试部署清单，不包含测试租户或私有镜像地址。

| 项目 | 示例值与用途 |
| --- | --- |
| ServiceAccount | `akless-business`；与应用联邦凭证一致，无需为 Sidecar 绑定 Kubernetes API 的 Role/ClusterRole |
| 引导 Token | 独立卷 `oidc-federated-token`，audience 为 `urn:example:idaas:bootstrap`，仅 Sidecar 挂载 `/run/secrets/oidc-federated` |
| 本地认证 Token | 独立卷 `credential-auth-token`，audience 固定为 `idaas.akless.sidecar`，两容器只读挂载 `/run/secrets/idaas-akless/credential-auth` |
| Token 有效期 | 两卷默认均 `expirationSeconds: 3600`；本地认证卷只支持 600–3600 秒，当前和旧 Token 都受签发后一小时上限约束，不能自行延长；引导卷按 IDaaS 联邦要求独立设置。由 kubelet 投射并轮换，不手工复制文件，不用 `subPath` 挂载 |
| 本地认证 | 两端共用 ConfigMap 的 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED`，保持字符串 `"true"`；请求须携带本地认证 Token |
| 角色限制 | 默认角色读取 `IDAAS_AKLESS_DEFAULT_ROLE_ARN`，本地白名单读取 `IDAAS_AKLESS_ALLOWED_ROLE_ARNS`；示例只允许默认角色 |
| 文件权限 | `defaultMode: 0440`、Pod `fsGroup: 20001`、容器非 root；如现有业务已有 fsGroup，需统一调整并验证读权限 |
| Pod 上下文 | Sidecar 的 `POD_UID`、`POD_NAMESPACE`、`POD_SERVICE_ACCOUNT` 由 Downward API 注入，不能填写固定值 |
| 监听与探针 | `127.0.0.1:61765`；exec 自检，不为凭据接口创建 Service、Ingress 或宿主端口 |
| 启动顺序 | Sidecar 放在 `initContainers` 且 `restartPolicy: Always`，启动探针通过后启动业务 |

开启本地认证时，程序要求 root 持有的只读投射目录和有效的 Pod 绑定 Token；不能替换成普通可写目录或手工创建的 Token 文件。模板不使用 `hostNetwork`，同 Pod 仅放置互信容器。监听 IP 固定为 `127.0.0.1`。若端口与现有业务冲突，修改共享 ConfigMap 的 `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` 并重建 Pod；模板两端可选引用同一个键，业务客户端按共享端口构造 URI；缺键或空值时默认使用 `61765`，显式完整 URI 优先；exec 自检会读取同一端口环境变量，无需声明 Sidecar 的 `containerPort`，具体方式见 [高级配置](../deploy/CONFIGURATION.zh-CN.md#高级配置端口超时和多角色)。

### 4.4 私有仓库拉取配置（公开镜像跳过）

若平台已为目标仓库配置镜像凭据助手，确认它对本命名空间和 ServiceAccount 生效。否则可用 Docker 创建仅包含目标仓库的独立登录配置，并导入同命名空间的拉取 Secret：

```bash
export AKLESS_DOCKER_CONFIG="$AKLESS_WORK/registry-login"
mkdir -m 700 -p "$AKLESS_DOCKER_CONFIG"
docker --config "$AKLESS_DOCKER_CONFIG" login '<您的 registry>'
kubectl -n "$AKLESS_NAMESPACE" create secret generic akless-registry-pull \
  --type=kubernetes.io/dockerconfigjson \
  --from-file=.dockerconfigjson="$AKLESS_DOCKER_CONFIG/config.json" \
  --dry-run=client -o yaml | kubectl apply -f -
```

在生成的 `deployment.yaml` 的 `spec.template.spec` 中添加如下字段，再执行第五节：

```yaml
imagePullSecrets:
  - name: akless-registry-pull
```

**检查结果：**Secret 已创建，Deployment 引用相同名称。导入配置必须实际包含该仓库的有效登录信息；仅有 `credsStore` / `credHelpers` 引用的本机凭据助手不能用于节点拉取。如果组织禁止本地保存登录信息，使用平台凭据助手。此登录目录含仓库凭据，按组织凭据规范保护并在导入后清理，不提交到 Git。最终是否可拉取以第五节 Pod 事件为准。

## 五、部署并验证基本链路

### 5.1 校验与部署

```bash
kubectl -n "$AKLESS_NAMESPACE" apply --dry-run=server --validate=strict \
  -f "$AKLESS_WORK/deployment.yaml"
kubectl -n "$AKLESS_NAMESPACE" apply -f "$AKLESS_WORK/deployment.yaml"
kubectl -n "$AKLESS_NAMESPACE" rollout status deployment/akless-example --timeout=180s
kubectl -n "$AKLESS_NAMESPACE" get pods -l app=akless-customer-example
```

**检查结果：**服务端校验成功、rollout 完成、Pod 通常显示 `2/2 Running`。校验只检查 API 和准入约束，不证明镜像可拉取或上游权限正确。Sidecar 和业务示例均使用 Alibaba Cloud Linux，业务示例提供 Node.js 24.22.2；节点须能拉取两个已配置的镜像。分别记录两个 digest；私有业务镜像与 Sidecar 一样需要配置拉取凭据。

从输出中选择此次部署的新 Pod，后续一直使用该 Pod 名，避免滚动期间误查旧副本：

```bash
export POD_NAME='<此次部署的新 Pod 名>'
kubectl -n "$AKLESS_NAMESPACE" get pod "$POD_NAME" \
  -o jsonpath='{range .status.initContainerStatuses[*]}{.name}{" "}{.imageID}{"\n"}{end}'
kubectl -n "$AKLESS_NAMESPACE" exec "$POD_NAME" -c idaas-akless-sidecar -- \
  /usr/local/bin/idaas-akless-sidecar --healthcheck
kubectl -n "$AKLESS_NAMESPACE" logs "$POD_NAME" -c business --tail=10
```

**检查结果：**Sidecar `imageID` 与选定镜像一致（多架构镜像需对应平台 manifest）；自检退出码 0，成功时无输出；业务日志包含 `[ok] got STS credentials from sidecar, expires at ...`。原生 Sidecar 的状态在 `initContainerStatuses` 中。

`/healthz`、Pod Ready 和示例业务 `/healthz` 都只检查基础运行状态，不证明上游认证和云授权成功。示例只在启动时取证一次，遇到 401 最多重读 Token 并重试一次，最终失败后记录错误，不进行后台重试。请继续执行下一步主动验证。

### 5.2 从业务容器验证认证取证

以下命令适用于本文 Node.js 示例，校验完整三元组、有效期和匿名访问拒绝，不打印凭据：

```bash
kubectl -n "$AKLESS_NAMESPACE" exec -i "$POD_NAME" -c business -- node <<'JS'
const http = require('node:http');
const { fetchJson, configuredCredentialsURI } = require('/app/credentials-client');
(async () => {
  const uri = configuredCredentialsURI();
  const c = await fetchJson(uri, 15000);
  const valid = c.Code === 'Success' &&
    [c.AccessKeyId, c.AccessKeySecret, c.SecurityToken].every(v => typeof v === 'string' && v.length > 0) &&
    Date.parse(c.Expiration) > Date.now();
  if (!valid) throw new Error('credential validation failed');
  for (const url of [uri, `${uri}?format=raw`]) {
    await new Promise((resolve, reject) => {
      const req = http.get(url, { agent: false, timeout: 5000 }, res => {
        res.resume();
        if (res.statusCode === 401) resolve();
        else reject(new Error(`expected anonymous 401, got ${res.statusCode}`));
      });
      req.on('timeout', () => req.destroy(new Error('anonymous check timeout')));
      req.on('error', reject);
    });
  }
  console.log(JSON.stringify({credentialValid: true, expiration: c.Expiration, anonymousDenied: true}));
})().catch(e => { console.error(e.message); process.exitCode = 1; });
JS
```

**检查结果：**退出码为 0，输出 `credentialValid: true`、未来到期时间和 `anonymousDenied: true`。若得到 401，不要关闭认证绕过，请检查本地认证 Token 和客户端认证头。

### 5.3 验证实际云身份

在本地安装 [阿里云 CLI](https://help.aliyun.com/zh/cli/) 后执行以下命令，另需 Python 3。临时凭据通过管道传递给 CLI，仅保留在进程和管道中；不要打开 shell 跟踪、SDK debug 日志或录制中间凭据输出。

```bash
(
  set -euo pipefail
  set +x
  kubectl -n "$AKLESS_NAMESPACE" exec "$POD_NAME" -c business -- \
    node -e 'const client=require("/app/credentials-client");client.fetchJson(client.configuredCredentialsURI(),15000).then(c=>process.stdout.write(JSON.stringify(c))).catch(()=>{console.error("credential request failed");process.exitCode=1})' |
    python3 -c '
import json, sys
c = json.load(sys.stdin)
p = {"name": "akless-verification", "mode": "StsToken", "region_id": "cn-hangzhou",
     "access_key_id": c["AccessKeyId"], "access_key_secret": c["AccessKeySecret"],
     "sts_token": c["SecurityToken"]}
print(json.dumps({"current": p["name"], "profiles": [p]}))
' |
    aliyun sts GetCallerIdentity --config-path /dev/stdin \
      --profile akless-verification --region cn-hangzhou --secure
)
```

**检查结果：**命令成功，返回的账号为目标账号，`Arn` 为对应角色的 `assumed-role/<role>/<session>`，而非管理员或节点角色。本步骤验证云身份；请求从本地 CLI 发起，**尚未验证业务容器到云产品的网络和业务权限**。下一节在自己的业务中完成这两项检查。

## 六、将 Sidecar 加入已有业务 Pod

通过演示验证后，将以下配置合并到自己的 Deployment 的 `spec.template.spec`。不要用整个演示 Deployment 覆盖现有业务，也不要在不同 Pod 中通过 `127.0.0.1` 互访。

1. 在业务所在命名空间创建 `akless-settings` ConfigMap，并为业务实际使用的 ServiceAccount 建立对应 IDaaS 联邦凭证；演示命名空间中的 ConfigMap 不能跨命名空间挂载。已有 ServiceAccount 可以保留，但 IDaaS 配置必须与其一致。
2. 合并示例的 `idaas-akless-client-config`、`oidc-federated-token`、`credential-auth-token` 三个卷，以及完整的 `idaas-akless-sidecar` 原生 Sidecar 定义。保留 Downward API、角色限制、认证开关、只读挂载、安全上下文、资源配置与 exec 探针；认证开关保持字符串 `"true"`。
3. 为业务容器只添加本地认证 Token 卷挂载和取证相关变量。保留自己的镜像、启动命令、端口、资源和探针，移除演示专用的 `demo-code` 卷及 `/app` 挂载。不要把 IDaaS 配置或引导 Token 挂给业务容器。
4. Pod 合并 `fsGroup` 并验证业务 UID 可读 Token，同时保留 root 文件所有权。不要将所有容器（含原生 Sidecar）设为相同的 `runAsUser`：kubelet 会将投射 Token 归该 UID 所有，导致本地认证启动失败；默认模板使用 `10001` / `1000` 两个不同的非 root UID，详见 [容器 UID 约束](../deploy/CONFIGURATION.zh-CN.md#kubernetes-配置映射)。如果设置 `automountServiceAccountToken: false`，确认现有业务未依赖自动挂载的 Kubernetes API Token。确实需要访问 Kubernetes API 时，应由平台另外提供符合业务权限的专用身份。
5. 检查 `hostNetwork`、端口占用、准入策略、服务网格代理和容器间信任边界；本文使用非 hostNetwork Pod，凭据仅在 Pod 内 loopback 可访问。不要添加凭据 Service。

业务容器需要合并的片段（这是容器片段，不能单独 `kubectl apply`）：

```yaml
env:
  - name: IDAAS_AKLESS_CREDENTIALS_URI
    value: http://127.0.0.1:61765/credentials
  - name: IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE
    value: /run/secrets/idaas-akless/credential-auth/token
volumeMounts:
  - name: credential-auth-token
    mountPath: /run/secrets/idaas-akless/credential-auth
    readOnly: true
```

这两个变量约定供业务适配器读取，设置变量本身不会让任意 SDK 自动接入。更新业务镜像后，先对自己的最终清单执行 `kubectl apply --dry-run=server --validate=strict`，再按组织发布流程部署，并使用新 Pod 完成下面的业务验收。

### 6.1 通用 HTTP 接入：业务需要做什么

每次实际取证时读取本地认证 Token 文件，向固定的 loopback 地址发送：

```http
GET /credentials HTTP/1.1
Host: 127.0.0.1:61765
Authorization: <本地认证 Token 文件中的裸 JWT>
Accept: application/json
```

`Authorization` 不加 `Bearer `。这里不是发给 IDaaS 的 Access Token，也不是引导 Token。客户端必须在 kubelet 轮换后重新读文件，不能启动时将文件内容永久读入环境变量。收到 401 可重读文件并最多重试一次；失败时不自动转为匿名请求或其他高权限凭据。

| 返回的云凭据 | 默认 JSON 字段 | 注入业务 SDK |
| --- | --- | --- |
| 阿里云 | `AccessKeyId`、`AccessKeySecret`、`SecurityToken`、`Expiration`；`Code=Success` | AK、SK、STS 安全令牌同时设置 |
| AWS | `AccessKeyId`、`SecretAccessKey`、`Token`、`Expiration` | AK、SK、Session Token 同时设置 |
| 腾讯云 | `tmpSecretId`、`tmpSecretKey`、`token`、`expiration`、`expiredTime` | 临时 SecretId、SecretKey、Token 同时设置；`expiredTime` 为 Unix 秒 |

厂商由上游返回的角色所属云决定，不通过 URL 的厂商参数指定。只取两个密钥字段、漏掉安全令牌将导致云请求失败。

业务侧有两种刷新方式：

- **每次云操作前取证：**请求 Sidecar，校验字段和有效期，再把同一次响应的三元组交给本次云 SDK 调用。Sidecar 自身缓存凭据，业务不缓存静态 SDK 客户端。适合先完成准确接入，开销与并发需按业务评估。
- **可刷新的 Provider：**按 SDK 的 Provider 接口实现获取与提前刷新，按角色分别缓存，原子替换整组三元组。到期前留足网络重试和操作时间；刷新失败仅复用仍有效且有足够剩余时间的旧凭据，过期必须报错。不能把启动时获取的凭据交给静态 Credential 后永久复用。

请求到达刷新点时触发 Sidecar 刷新：通常在缓存剩余寿命消耗约 4/5 后，寿命允许时会提前到足以预留“旧凭据有效期门槛 + 一次刷新预算”的位置，详见 [刷新策略](../deploy/CONFIGURATION.zh-CN.md#sidecar-启动参数)。它不会主动更新业务内存中保存的凭据。业务应设置取证总 deadline（示例 `15s`），对可恢复故障按业务策略有限退避；对非幂等云操作，是否重试由业务保证。

#### Node.js 阿里云接入示例

将同版本的 `business-example/credentials-client.js` 复制到业务代码目录。该客户端需要 Node.js 24+，每次取证重读 Token；限制数字 loopback `/credentials`、不跟随重定向，并设置响应大小上限。不要将其改为可请求用户提供的 URL。

以下示例演示“每次操作前取证”，可作为业务自检命令。先在自己的 Node.js 项目中安装固定版本的产品 SDK，再将代码保存为 `cloud-identity.cjs`，与 `credentials-client.js` 放在同一目录并一起构建进业务镜像：

```bash
npm install --save-exact @alicloud/sts20150401@1.2.0 @alicloud/openapi-core@1.0.8
```

```javascript
'use strict';
const Sts = require('@alicloud/sts20150401');
const { $OpenApiUtil } = require('@alicloud/openapi-core');
const { fetchJson, configuredCredentialsURI } = require('./credentials-client');

async function getCloudIdentity() {
  // 每次云操作前重新取证，Sidecar 负责上游缓存。
  const c = await fetchJson(configuredCredentialsURI(), 15000);
  if (c.Code !== 'Success' ||
      ![c.AccessKeyId, c.AccessKeySecret, c.SecurityToken].every(v => typeof v === 'string' && v.length > 0) ||
      !(Date.parse(c.Expiration) > Date.now() + 30000)) {
    throw new Error('credential incomplete or less than 30 seconds remaining');
  }
  const client = new Sts.default(new $OpenApiUtil.Config({
    type: 'sts',
    accessKeyId: c.AccessKeyId,
    accessKeySecret: c.AccessKeySecret,
    securityToken: c.SecurityToken,
    endpoint: 'sts.cn-hangzhou.aliyuncs.com',
    connectTimeout: 5000,
    readTimeout: 10000
  }));
  const result = await client.getCallerIdentity();
  return { accountId: result.body.accountId, arn: result.body.arn };
}

module.exports = { getCloudIdentity };
if (require.main === module) {
  getCloudIdentity().then(x => console.log(JSON.stringify(x))).catch(() => {
    // 不打印 SDK 请求、响应或原始错误对象，避免泄漏临时凭据。
    console.error('cloud identity verification failed');
    process.exitCode = 1;
  });
}
```

在改造后的业务容器中执行 `node <业务目录>/cloud-identity.cjs`，预期返回目标账号及角色。产品 SDK 参考 [阿里云 STS Node.js SDK](https://github.com/aliyun/alibabacloud-typescript-sdk/tree/master/sts-20150401)。将同样的取证和三元组注入方式用于实际云产品 API，例如使用已有 OSS SDK 读取一个明确授权的对象，并校验内容；endpoint、权限和调用超时按对应产品配置。不要在业务容器内临时安装依赖代替构建发布。

### 6.2 AWS 容器凭据提供器

使用支持 Token File 的 AWS SDK 版本，业务代码初始化可刷新的默认凭据链，并配置：

```yaml
env:
  - name: AWS_CONTAINER_CREDENTIALS_FULL_URI
    value: http://127.0.0.1:61765/credentials
  - name: AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE
    value: /run/secrets/idaas-akless/credential-auth/token
```

保留相同的本地认证 Token 挂载；将 Sidecar 的默认角色、白名单和 IDaaS 资产授权换为目标 AWS 角色。排除静态 AK、共享配置文件、`AWS_CONTAINER_CREDENTIALS_RELATIVE_URI` 及其他身份注入对来源的覆盖。变量含义与版本支持参考 [AWS 容器凭据提供器](https://docs.aws.amazon.com/sdkref/latest/guide/feature-container-credentials.html)。

此处不承诺所有 AWS SDK 版本均能安全接入。生产前必须验证所选版本发送裸 JWT、每次更新能重读文件、可持续刷新、不把 Token 发给代理或重定向目标；再在同一业务进程中调用 `GetCallerIdentity` 和实际业务 API 完成验收。未通过时采用受控 HTTP 适配器。

### 6.3 阿里云 URI 模式与腾讯云 SDK

默认本地认证下，仅设置 `ALIBABA_CLOUD_CREDENTIALS_URI` 不能保证 SDK 携带文件 Token；推荐使用上面的认证 HTTP 适配器或自定义 Provider。腾讯云需通过认证 HTTP 取证，将临时三元组映射到所用 SDK 的 Credential/Provider，并实现到期刷新。具体语言和产品 SDK 的 Provider 接口以其官方版本文档为准，当前仓库不提供所有语言的完整 Provider。

本地认证开关可以显式关闭，但会使能访问本地接口的进程无需 Token 即可取证，削弱 SSRF 防护。本文接入及验收均保持开启；兼容性设置详见仓库 `deploy/CONFIGURATION.zh-CN.md`，不能把关闭认证作为默认接入方法。

指定 Node.js SDK 版本的原生 URI 取证、认证拒绝和云端身份验证可参考仓库的 [独立集成测试](../verification/native-uri/README.zh-CN.md)。可选长测在发现刷新后结束，不等待首份 STS 实际到期，不能代替下一节的跨到期业务验收。

## 七、上线前验收

对改造后的真实业务完成下列检查并记录结果，不能仅凭示例日志通过上线验收：

| 检查 | 操作与通过标准 |
| --- | --- |
| 身份和业务权限 | 在业务容器中用 Sidecar 返回的凭据调用云身份 API及一项业务实际依赖的 API；账号/角色、目标资源和返回内容均符合预期 |
| 来源切换 | 移除业务静态密钥和不需要的凭据来源；核对云审计记录中的角色，确认实际使用 Sidecar 身份 |
| 本地访问隔离 | 无 Token 请求默认和 raw 接口均为 401；另一个 Pod 无法通过 Pod IP 访问本 Pod 的 61765；业务不能读取引导 Token |
| Token 轮换 | 同一业务进程持续取证并调用云 API，观察至少两次 kubelet 本地认证 Token 轮换；记录轮换次数、业务调用成功数和错误数，不记录文件内容或 Token 哈希 |
| 云凭据刷新 | 同一业务进程持续运行到至少跨过首次 STS 的 `Expiration`，记录后续凭据的到期时间和云 API 成功结果；首次凭据过期后仍能正常调用 |
| 短暂故障恢复 | 在测试环境使 Sidecar 暂时不可用，确认业务取证有总超时并有限退避，恢复后继续成功；禁止匿名或管理员凭据回退 |
| 有效期不足 | 测试环境验证没有足够剩余寿命或已过期凭据时停止使用并报错，不能继续以过期凭据调用 |

演示进程启动时只取一次凭据，不能替代上述长期业务测试。可在专用测试 Pod 中将本地认证 Token 的 `expirationSeconds` 暂调为 `600` 以加快轮换观察，验收后恢复生产设置；不要调整生产集群时钟。轮换测试方法见同版本仓库的 `deploy/rotation-integration-test.js`，使用前确认其目标工作负载和前提。

Kubernetes Token 的文件轮换、Sidecar 缓存的 STS 刷新、业务 SDK 内部凭据更新是三件独立的事。已有 STS 在撤销 IDaaS 授权后不一定即时失效；权限调整后的验证应覆盖重新取证及已签发凭据的剩余有效期。

## 八、常见问题与排查

先查看当前 Pod 的事件和 Sidecar 日志，避免在排障中打印 Token 文件或凭据正文：

```bash
kubectl -n "$AKLESS_NAMESPACE" describe pod "$POD_NAME"
kubectl -n "$AKLESS_NAMESPACE" logs "$POD_NAME" -c idaas-akless-sidecar --tail=50
```

| 现象 | 排查及修复 |
| --- | --- |
| `ImagePullBackOff` | 检查实际镜像地址、节点到仓库连通性、是否私有、Secret 是否在同命名空间；公开镜像地址以发布页为准 |
| `exec format error` | 镜像架构与节点不符，重建相应架构或调整调度 |
| 原生 Sidecar 清单被拒绝 | 核对控制面、节点版本及准入限制，不删除 `restartPolicy: Always` 后继续套用原流程 |
| `initialize local credential authentication failed` | 核对只读投射卷、root 文件所有权、非 root 用户、fsGroup、Downward API、固定本地 audience 和 Pod 绑定；检查是否所有容器使用相同 UID，导致 kubelet 改变文件所有者。不要复制 Token 或 chmod 投射文件 |
| 连接拒绝 | 检查是否同一 Pod、Sidecar 是否启动、61765 冲突、实际监听地址和业务 URI是否一致 |
| HTTP 400 | 读取错误码；`InvalidAuthentication` 检查是否误加 Bearer 或重复认证头；其他错误检查 roleArn 与 format 参数 |
| HTTP 401 | 本地 Token 缺失或不匹配；重读本 Pod 的本地认证 Token 后最多重试一次，确认不是引导 Token |
| HTTP 403 `RoleNotAllowed` | 请求角色不在本地白名单；先核对授权意图，再更新白名单及重建 Pod |
| HTTP 503 `AuthenticationUnavailable` | Sidecar 参考 Token 文件无效、过期或不可读；检查 kubelet 投射和本地时间同步，不回退匿名 |
| HTTP 502 | 查看上游认证或 Developer API 错误日志；检查 DNS、HTTPS、配置、角色状态、功能/数据权限、总超时和返回凭据有效期 |
| `invalid_client_assertion` | 引导 Token 的 iss/aud/sub/kid 与信任源、JWKS 和应用联邦凭证不匹配；核对集群签名密钥是否轮换 |
| 取到 STS 但云 API 被拒绝 | 核对实际云身份、是否同时注入安全令牌、云角色权限及资源策略、地域和端点；IDaaS 授权不等于云产品授权 |
| 初次成功，运行一段时间失败 | 检查是否缓存了 Token 内容或静态 SDK 客户端；分别核对 Token 轮换、STS 到期和 SDK 刷新 |
| 修改 ConfigMap 未生效 | 配置在启动时读取；更新后重建 Pod；排查 CLI 参数是否覆盖环境变量 |

Developer API 客户端不使用环境 HTTP 代理且不跟随重定向。若环境只能经显式 HTTP 代理出网，不能仅设置 `HTTPS_PROXY` 就认为该链路可用，应先与平台确认支持的网络路径。

## 九、配置更新、升级和清理

Sidecar 参数优先级为 **CLI > 非空环境变量 > 默认值**。本示例通过 ConfigMap 的 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` 同时配置两端认证，保持字符串 `"true"`；默认角色与白名单分别来自 `IDAAS_AKLESS_DEFAULT_ROLE_ARN` 和 `IDAAS_AKLESS_ALLOWED_ROLE_ARNS`。端口、超时和多角色覆盖方式见同版本仓库的 [配置契约](../deploy/CONFIGURATION.zh-CN.md)。

升级原来使用 `8080` 的部署时，先显式保留旧端口，或同步迁移到 `61765`；端口 ConfigMap 键可省略，业务代码与模板需使用匹配版本，见 [升级说明](../deploy/CONFIGURATION.zh-CN.md#默认端口与升级)。

修改 JSON、角色、白名单、端口或认证开关后，更新 ConfigMap 并重建 Pod；修改默认角色时同步核对白名单。第四节命令会将白名单设为默认角色并开启认证；自定义配置时应保留实际需要的值。高级配置若修改了 Deployment，则重新校验并应用清单。

```bash
kubectl -n "$AKLESS_NAMESPACE" rollout restart deployment/akless-example
kubectl -n "$AKLESS_NAMESPACE" rollout status deployment/akless-example --timeout=180s
```

升级镜像时，使用同版本文档和模板更新 `SIDECAR_IMAGE` 和 `BUSINESS_IMAGE`，重新生成、校验和应用清单，再从新 Pod 复核身份、业务权限和轮换。保留两个上一版镜像 digest、部署清单与配置快照；Deployment 回滚不会自动恢复 ConfigMap、Secret 或 IDaaS 授权。若 ConfigMap 同名更新，需恢复对应旧配置并重建 Pod 才能完成配置回退。

仅清理本文创建的演示资源时执行：

```bash
kubectl -n "$AKLESS_NAMESPACE" delete deployment akless-example
kubectl -n "$AKLESS_NAMESPACE" delete configmap akless-settings akless-demo-code
kubectl -n "$AKLESS_NAMESPACE" delete serviceaccount akless-business
```

删除前确认它们未被其他工作负载复用。IDaaS 侧按需撤销演示应用的功能/数据授权并删除演示联邦凭证；不要删除仍被其他业务使用的云角色或信任源。

## 附录：凭据 HTTP 接口

| 请求 | 说明 |
| --- | --- |
| `GET /credentials` | 使用默认角色，返回厂商默认字段 |
| `GET /credentials?roleArn=<URL 编码的角色外部 ID>` | 显式请求角色，须同时满足本地白名单和 IDaaS 授权 |
| `GET /credentials?format=raw` | 返回 Sidecar 声明的 Developer API 字段，不保证原始响应字节或未知字段透传 |
| `GET /healthz` | 无需本地认证的基础 HTTP 检查，不调用上游取证 |

所有 `/credentials` 请求，包括缓存命中与 raw 格式，均要求本地认证。`roleArn` 省略或为空时使用默认角色；指定角色应通过标准 URL 编码构造，不能直接拼接未编码的值。`format` 只允许省略或为 `raw`，不支持 `format=aws` 等厂商选择参数。完整响应契约见同版本仓库 `docs/CREDENTIALS-RESPONSE.zh-CN.md`。
