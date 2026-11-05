# 三云 STS 与资源查询集成测试

[English](README.md) | **简体中文**

本目录保留可重复运行的业务容器集成测试。测试使用操作者自己的 IDaaS 配置和三云角色，通过 Sidecar 获取 STS，再用固定版本 SDK 查询 OSS、COS、AWS 中国区 S3 的 Bucket 信息。测试只读，空列表可以通过，403、超时或取证失败会使程序非零退出。

## 接入路径与边界

| 云 | 本地认证开启时 | 原生 Credentials URI |
| --- | --- | --- |
| 阿里云 | 认证 URI 适配器 → `ali-oss` | 固定版本 `@alicloud/credentials` 2.4.7 不发送本地认证 Token，预期被 401 拒绝；独立关闭认证的 Pod 验证原生取证及 OSS 查询 |
| 腾讯云 | 认证 URI 适配器 → `cos-nodejs-sdk-v5` | 本测试采用适配器，不宣称 COS SDK 原生支持该 URI |
| AWS | 认证 URI 适配器 → `S3Client` | `fromHttp` 从文件读取认证 Token，访问 URI 后向 `S3Client` 提供临时凭据 |

适配器每次解析凭据都重新访问 URI，并保留同代 AK、SK、Session Token 和过期时间。本地认证复用上层 `credentials-client.js`。OSS/COS 每次查询重新创建客户端；长期业务应将取证逻辑接入 SDK 刷新机制。AWS 使用异步 Provider。本测试不替代跨 STS 到期、至少两次 kubelet 轮换的长期验收。

资源查询使用 OSS 杭州入口、COS GetService 和 S3 `cn-north-1`。角色分别需要 `oss:ListBuckets`、`cos:GetService`、`s3:ListAllMyBuckets` 权限。其他地域或 AWS 分区需调整程序端点后独立验证。不要为通过测试自动扩大角色权限。

## 本地回归

从仓库根目录执行，无需真实租户配置：

```bash
npm ci --prefix business-example/multicloud --ignore-scripts --no-audit --no-fund
npm test --prefix business-example/multicloud
```

测试包含三层，均使用虚构凭据，不访问 IDaaS 或云资源：

- `integration.test.cjs`：字段映射、角色与 URI、有效期边界、资源查询和结果汇总；产品 SDK 使用替身。
- `sdk-providers.test.cjs`：使用依赖锁定的真实 AWS/阿里云 URI SDK，仅替换文件或 HTTP 边界，核对来源优先级、Token 重读、错误处理及阿里云模拟时钟刷新。
- `http-chain.test.cjs`：本地 HTTP 服务模拟 Sidecar，串联真实文件读取、认证 HTTP helper、三云映射与产品 SDK 边界，检查轮换重试、同代三元组及失败传播。

覆盖率门槛为本目录两个运行文件的行、分支各 80%，不把测试文件或第三方 SDK 算入分母。覆盖率不代替真实云端验收。

## 场景覆盖与剩余边界

| 场景 | 当前自动化覆盖 | 真实环境仍需验收 |
| --- | --- | --- |
| 三云角色及取证来源 | 角色 URL 编码、配置缺失、远端 URI 拒绝；AWS 显式压制 RELATIVE_URI 和静态 Token 环境回退 | 预期租户、账号及角色，部署环境不存在其他身份来源 |
| 三元组与有效期 | 三云缺失/空白/类型错误、无效/过期时间、剩余 60 秒边界；阿里云 `Code=Success`；腾讯云两个到期时间一致性 | 真实签发寿命、时钟同步和实际云 API 接受情况 |
| 本地认证及轮换 | 三云 HTTP 组件均验证 401 重读一次、403/502 不重试；AWS 真实 Provider 验证文件重读及缺失时不回退 | 至少两次真实 kubelet 轮换、Pod/受众隔离及投射卷只读；通用安全测试入口见 [测试说明](../../docs/TESTING.zh-CN.md) |
| 原生 URI | AWS 固定版本真实 Provider 的解析、错误、来源限制；阿里云固定版本真实 Provider 的预期 401、缓存及模拟临期刷新；腾讯云仅走适配器 | 同版本 SDK 的真实网络行为；阿里云公开 Credential 模型不暴露到期字段，本目录原生正向用例仅检查三元组和资源调用，额外有效期/身份验收见 [native-uri](../../verification/native-uri/README.zh-CN.md) |
| 产品 SDK 注入及资源查询 | OSS、COS、S3 的完整 STS 参数、HTTPS/地域/超时、空列表、权限失败传播、AWS 客户端释放及不输出 Bucket 名称 | 使用自己的授权角色运行下文真实查询；列表成功不能证明对象读写权限或预期身份 |
| 失败判定与日志 | 单项失败不阻断其他检查；配置缺失 CLI 非零退出；SDK 原始正文/凭据不输出；正文中的伪造 401 不算认证拒绝 | 真实云侧拒绝、上游中断及恢复后的结果 |
| 长期刷新 | 三云适配器替换完整凭据代际；AWS 原生 Provider 重读；阿里云真实 SDK 在模拟时钟下刷新 | 三云同一业务进程、同一 SDK 客户端跨首份 STS 到期后持续调用；当前一次性资源查询程序不提供这项长测 |

正式声明某云业务接入验收完成前，还需执行以下场景并保存脱敏证据：

1. 按下文运行三云正向矩阵，确认每项结果及业务进程退出码。阿里云原生 URI 的认证拒绝与关闭认证的正向测试分别判定。
2. 为每云增加预期账号/角色的身份断言，再执行取证及资源查询。当前三云资源矩阵没有自动身份比对；独立 `native-uri` 的阿里云身份检查不能替代其他两云或适配器路径。
3. 使用专用拒绝角色分别验证：本地白名单拒绝、IDaaS 未授权角色取证失败、已获 STS 但云侧列表权限不足。最后一种应表现为 STS 项成功、资源项失败、总体非零退出；不能把真实 403 改成成功断言。
4. 增加专门的长测入口，保持同一业务进程和 SDK 客户端，连续跨过首份 STS 到期、至少两次真实 kubelet 轮换；确认 AK/SK/Token 同代更新，取证故障有界失败、恢复后继续成功。重复运行短测或模拟时钟结果不能替代此项。

## 准备真实集群测试

先按 [部署指南](../../deploy/README.zh-CN.md) 创建命名空间、ServiceAccount 和镜像拉取凭据。在 IDaaS 中为此 ServiceAccount 配置联邦凭证，并授权三云角色。

将仓库根目录的 `cloud_idaas.json.example` 复制到仓库外的受保护工作目录，填写实例、应用、端点及联邦凭证名称。`oidcTokenFilePath` 保持空，`oidcTokenFilePathEnvVarName` 使用 `OIDC_TOKEN_FILE_PATH`。不在 Git 中保存真实配置。

```bash
export AKLESS_TEST_CONTEXT='<your-kubectl-context>'
export AKLESS_TEST_CONFIG='<absolute-path-outside-repository>/client-config.json'
export IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN='<authorized-alibaba-role-arn>'
export IDAAS_AKLESS_TEST_AWS_ROLE_ARN='<authorized-aws-china-role-arn>'
export IDAAS_AKLESS_TEST_TENCENT_ROLE_ARN='<authorized-tencent-role-identifier>'
export AKLESS_TEST_ROLES="$IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN,$IDAAS_AKLESS_TEST_AWS_ROLE_ARN,$IDAAS_AKLESS_TEST_TENCENT_ROLE_ARN"
kubectl --context "$AKLESS_TEST_CONTEXT" -n idaas-akless create configmap idaas-akless-multicloud-config \
  --from-file=idaas-akless-client-config.json="$AKLESS_TEST_CONFIG" \
  --from-literal="IDAAS_AKLESS_DEFAULT_ROLE_ARN=$IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN" \
  --from-literal="IDAAS_AKLESS_ALLOWED_ROLE_ARNS=$AKLESS_TEST_ROLES" \
  --from-literal=IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=true \
  --from-literal=IDAAS_AKLESS_SIDECAR_LISTEN_PORT=61765 \
  --dry-run=client -o yaml | kubectl --context "$AKLESS_TEST_CONTEXT" apply -f -
```

构建并推送测试镜像，以 `business-example` 为上下文：

```bash
docker buildx build --platform linux/amd64 --push \
  -f business-example/multicloud/Dockerfile \
  -t '<registry>/<namespace>/idaas-akless-multicloud-test:<version>' business-example
```

将 `deploy/multicloud-test.yaml` 复制到仓库外，替换两个 Pod 的全部占位符：Sidecar 镜像、测试业务镜像、引导 audience 及三个 `IDAAS_AKLESS_TEST_*_ROLE_ARN` 环境变量。环境变量须与上面的角色一致；YAML 不展开本机变量。镜像建议固定 digest，audience 须匹配 IDaaS 信任源；本地认证 audience `idaas.akless.sidecar` 保持不变。

```bash
export AKLESS_TEST_MANIFEST='<absolute-path-outside-repository>/multicloud-test.yaml'
kubectl --context "$AKLESS_TEST_CONTEXT" apply --dry-run=server --validate=strict -f "$AKLESS_TEST_MANIFEST"
kubectl --context "$AKLESS_TEST_CONTEXT" apply -f "$AKLESS_TEST_MANIFEST"
kubectl --context "$AKLESS_TEST_CONTEXT" -n idaas-akless logs -f akless-multicloud-test -c business
kubectl --context "$AKLESS_TEST_CONTEXT" -n idaas-akless logs -f akless-multicloud-native-ali-test -c business
```

两个 Pod 使用独立 ConfigMap 和标签，不修改 `akless-demo`。第一个保持本地认证开启，执行 9 项检查；第二个显式关闭本地认证且不挂载其 Token，仅执行阿里云原生 URI 的 2 项正向检查。它们不创建 Service，最长运行 900 秒，要求原生 Sidecar 支持。

输出为 JSON：云、模式、通过标志、过期时间或返回的 Bucket 数量；失败只输出经过限制的错误码及 HTTP 状态，不输出资源名称、凭据或原始 SDK 异常。Bucket 数量是本次返回条数，不一定是账号资源总量。成功要求 summary 分别为 9/9 和 2/2，并确认业务容器终止退出码为 0：

```bash
kubectl --context "$AKLESS_TEST_CONTEXT" -n idaas-akless get pods -l app=akless-multicloud-test \
  -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.containerStatuses[?(@.name=="business")].state.terminated.exitCode}{"\n"}{end}'
```

尚未终止或没有退出码不算通过。阿里云认证开启时的预期 401 仅验证拒绝边界。再次运行前删除这两个测试 Pod 再 apply；结束后清理它们及专用 ConfigMap，保留仍被其他工作负载使用的资源。
