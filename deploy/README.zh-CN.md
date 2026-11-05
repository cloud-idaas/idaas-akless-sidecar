# Kubernetes / ACK 部署

[English](README.md) | **简体中文**

从仓库根目录执行以下命令。部署清单创建一个包含 Sidecar 和 Node.js 业务示例的 Pod，业务通过 Pod loopback 获取阿里云临时凭据。

租户、角色、引导 audience 和镜像均为占位符，必须替换后再校验与部署。真实配置放在仓库外，长期集成测试入口见 [测试说明](../docs/TESTING.zh-CN.md)。

默认 ConfigMap 显式列出客户端配置 JSON、默认角色、角色白名单、本地认证开关和共享端口，认证开关保持字符串 `"true"`，端口为字符串 `"61765"`。共享端口为可选项，两端通过 `optional: true` 引用同一个键；缺键或空值时默认使用 `61765`。业务优先使用显式 URI，否则按共享端口构造地址，exec 自检继承 Sidecar 环境变量；监听 IP 固定为 `127.0.0.1`，上游刷新总超时默认 `10s`。升级旧部署时显式保留 `8080`，或同步迁移监听、业务 URI 和探针；端口键可省略，业务代码与模板需使用匹配版本；修改 ConfigMap 后重建 Pod。端口、超时、多角色及认证开关的调整方式见 [配置契约](CONFIGURATION.zh-CN.md)。

## 1. 准备

- Kubernetes 1.33 或更高版本，支持原生 Sidecar 和 ServiceAccount Token 投射，`kubectl` 指向目标集群。
- IDaaS M2M 应用、OIDC 联邦凭据、云账号角色及应用授权已配置。
- IDaaS 能验证集群 Token 的签发者、受众、主体和签名；使用 discovery 时，IDaaS 可访问签发者的 discovery/JWKS。
- Docker/Buildx 可用，镜像仓库已创建并有推送和拉取权限。

```bash
kubectl config current-context
kubectl get nodes -o custom-columns=NAME:.metadata.name,ARCH:.status.nodeInfo.architecture
```

## 2. 构建镜像

```bash
export ACR_REGISTRY='<your-registry>'
export ACR_NS='<your-namespace>'
export IMG_TAG='<your-version>'
export IMG_ARCH=linux/amd64
docker login "$ACR_REGISTRY" --username='<your-user>'
./deploy/build-images.sh
```

脚本构建、校验架构并推送 `idaas-akless-sidecar` 和 `idaas-akless-business-example`。arm64 节点设置 `IMG_ARCH=linux/arm64`。每次只构建一个架构；Buildx 驱动需能将产物导入 Docker 镜像存储。

`--no-push` 仅构建并检查本地 `:build-linux-amd64` 标签，arm64 对应 `:build-linux-arm64`。默认基础镜像由 Dockerfile 固定：最终运行层为 Alibaba Cloud Linux 3.13.4，Node 24.22.2 从 glibc 制品阶段提取，Go 编译阶段保留独立工具链。无本机 Buildx 时，脚本可使用运行中的 Colima VM。

需要镜像代理时，分别设置完整的 `BUILD_BASE_IMAGE`（Go 编译器）、`RUNTIME_BASE_IMAGE`（两镜像的 ALinux 底座）、`NODE_BASE_IMAGE`（Node glibc 制品）引用，保留版本及多架构索引 digest。未设置时不覆盖 Dockerfile 默认值。旧的非空 `BASE_MIRROR` 前缀会提前报错，避免把 ALinux 运行层意外换回 Alpine。

`--help` 或 `-h` 只显示用法，不执行构建或推送。未知、重复和多余参数在调用 Docker 前报错退出；不带参数时仍默认构建并推送。

## 3. 配置清单

| 文件 | 需要配置的内容 |
| --- | --- |
| [configmap.yaml](configmap.yaml) | 用控制台导出的完整 JSON 替换 `idaas-akless-client-config.json` 的值；设置默认角色、白名单，并保持认证开启 |
| [deployment.yaml](deployment.yaml) | 替换两个镜像引用、Token audience 和拉取凭证；与配置的 ServiceAccount 一致 |
| [serviceaccount.yaml](serviceaccount.yaml) | 专用 ServiceAccount，主体与 IDaaS 联邦配置一致 |
| [namespace.yaml](namespace.yaml) | 部署命名空间，与其他清单一致 |

SDK JSON 的 `oidcTokenFilePath` 留空，`oidcTokenFilePathEnvVarName` 为 `OIDC_TOKEN_FILE_PATH`；Deployment 将该变量设为投射文件 `/run/secrets/oidc-federated/token`。导出的 `${oidcTokenFilePath}` 是占位符，需替换为空字符串。JSON 不支持注释。

Developer API 必须从 Pod 内可达。默认角色需获得 IDaaS 应用授权；通过 `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` 配置本地白名单，包含默认角色及业务所需角色，具体方式、参数优先级和超时单位见 [配置契约](CONFIGURATION.zh-CN.md)。

填写后应满足：

| 检查项 | 预期 |
| --- | --- |
| 联邦 subject | `system:serviceaccount:idaas-akless:idaas-akless-demo`，修改命名空间或 SA 时同步修改信任配置 |
| 引导 Token audience | 与 IDaaS 联邦配置完全一致，不能保留 `<idaas-bootstrap-audience>`；本地认证 audience 保持 `idaas.akless.sidecar` |
| 默认角色 | 阿里云业务示例使用已授权的阿里云角色，不保留 `<account-id>` 等占位符 |
| 两个镜像引用 | 已推送的实际 registry/namespace/tag，架构与节点一致；YAML 不展开本机环境变量 |
| SDK JSON | 来自同一租户的完整导出；Token 文件路径与投射挂载对应 |

本地认证 Token必须位于独立的 `credential-auth-token` 投射卷，固定audience为 `idaas.akless.sidecar`，不修改成IDaaS或API受众。业务只挂该卷，不能读引导卷；保留0440、fsGroup和Downward API配置。启动会检查Token及Pod上下文。发布前验证本地Token不能兑换IDaaS身份或被API Server认证，并验证至少两次真实kubelet轮换。

本接口加固不阻断其他凭据入口。平台需限制业务访问节点元数据及其他凭据代理，避免SSRF旁路；文件读取、RCE和节点失陷不在该防护保证内。

## 4. 应用

```bash
kubectl apply -f deploy/namespace.yaml
```

私有仓库配置 `acr-secret`，或使用 ACK ACR 凭据助手并移除清单中的 `imagePullSecrets`。Docker 配置含目标仓库的有效登录信息时可导入：

```bash
kubectl -n idaas-akless create secret generic acr-secret \
  --type=kubernetes.io/dockerconfigjson \
  --from-file=.dockerconfigjson="$HOME/.docker/config.json" \
  --dry-run=client -o yaml | kubectl apply -f -
```

仅导入需要的仓库信息。`credsStore`/`credHelpers` 引用的本机凭据助手不能在节点中使用；这种情况使用专用仓库凭据或 ACK 凭据助手。

```bash
bash deploy/validate-manifests.sh
kubectl apply -f deploy/serviceaccount.yaml -f deploy/configmap.yaml -f deploy/deployment.yaml
kubectl -n idaas-akless rollout status deployment/akless-demo --timeout=180s
```

校验脚本只做服务端 dry run，目标命名空间须已存在。Sidecar 只监听 loopback，就绪探针使用容器内 `exec --healthcheck`；探针的 4 秒超时大于程序的 2 秒自检超时。

成功时 dry run 无错误、`rollout status` 报告部署完成。失败时执行 `kubectl -n idaas-akless describe pod <pod-name>`，按事件处理拉取权限、占位配置或探针错误。

## 5. 验收

```bash
kubectl -n idaas-akless get pods
kubectl -n idaas-akless logs deployment/akless-demo -c idaas-akless-sidecar --tail=20
kubectl -n idaas-akless logs deployment/akless-demo -c business --tail=10
```

预期 `2/2 Running`，业务成功获取凭据时输出过期时间。原生 Sidecar 的 startupProbe 会等待本地监听可用，但不检查 IDaaS 上游取证。示例启动时执行一次取证，其中本地认证 401 最多重读 Token 并重试一次；最终失败后不做后台重试，可用下方命令主动验证。滚动更新时，用新 Pod 名称替换下面的 `deployment/akless-demo`，保证验证到目标版本。

先验证 Sidecar 本地 HTTP，再从业务容器检查凭据完整性，命令见 [README 验证步骤](../README.zh-CN.md#5-验证服务与有效凭据)。预期分别为自检退出码 0，以及 `credentialValid: true` 和未来到期时间。检查不会输出临时密钥。

本机安装 Python 3 和阿里云 CLI 后，验证默认角色身份：

```bash
(
  set -euo pipefail
  set +x
  kubectl -n idaas-akless exec deployment/akless-demo -c business -- \
    node -e 'const client=require("/app/credentials-client");client.fetchJson(client.configuredCredentialsURI(),15000).then(c=>process.stdout.write(JSON.stringify(c))).catch(()=>{console.error("credential request failed");process.exitCode=1})' |
    python3 -c '
import json, sys
c = json.load(sys.stdin)
p = {"name": "sts-verification", "mode": "StsToken", "region_id": "cn-hangzhou",
     "access_key_id": c["AccessKeyId"], "access_key_secret": c["AccessKeySecret"],
     "sts_token": c["SecurityToken"]}
print(json.dumps({"current": p["name"], "profiles": [p]}))
' |
    aliyun sts GetCallerIdentity --config-path /dev/stdin \
      --profile sts-verification --region cn-hangzhou --secure
)
```

核对返回的 assumed-role 身份，再调用业务实际需要的云 API。该命令通过标准输入传递临时凭据，不落文件；不要开启命令跟踪或记录凭据正文。健康检查和 Pod Ready 不代表云 API 权限已验证。

预期 `Arn` 形如 `acs:ram::<account-id>:assumed-role/<role>/<session>`，账号和角色应与授权目标一致。身份正确仍需验证具体业务 API 权限；例如执行业务需要的对象读取操作，并检查取得预期对象，而不是仅凭 Pod Ready 通过验收。

## 更新与排障

更新镜像引用和对应配置后重新校验、应用清单。仅修改 ConfigMap 时需执行 `kubectl -n idaas-akless rollout restart deployment/akless-demo`。固定 digest 的镜像不会因覆盖标签而更新；Deployment 回滚也不会恢复 ConfigMap 或 Secret。

| 现象 | 检查项 |
| --- | --- |
| 镜像拉取失败或 `exec format error` | 仓库权限、拉取 Secret、目标架构 |
| `400` / `403` | 默认或请求角色、参数编码、本地白名单 |
| `401` / `503` | 本地Token挂载、读权限、受众、Pod上下文、过期与轮换；不能匿名重试 |
| `502` | Sidecar 日志、Pod 上游连通性、IDaaS 授权、响应有效期及缓存容量 |
| `invalid_client_assertion` | Token 的 `iss/aud/sub/kid` 与 IDaaS 信任配置 |
| 配置未生效 | Pod 是否已重建，CLI 是否覆盖环境变量 |

仅当命名空间全部为可删除的演示资源时，使用 `kubectl delete namespace idaas-akless` 清理。
