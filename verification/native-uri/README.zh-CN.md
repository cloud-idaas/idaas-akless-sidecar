# 阿里云 SDK 原生 Credentials URI 真实集成测试

[English](README.md) | **简体中文**

本测试使用 Node.js `@alicloud/credentials` 2.4.7 和 `@alicloud/sts20150401` 1.2.0。测试镜像与业务演示镜像分离，依赖通过 `package-lock.json` 固定。测试真实网络取证和云端身份，不替换 HTTP transport、SDK Provider、时钟或响应。

四种组合：

| SDK 配置 | Sidecar 认证 | 预期 |
|---|---|---|
| 显式 `type: credentials_uri` | 开启 | SDK 因 HTTP 401 失败 |
| 默认凭据链及 `ALIBABA_CLOUD_CREDENTIALS_URI` | 开启 | SDK 因 HTTP 401 失败 |
| 显式 `type: credentials_uri` | 关闭 | 原生取证、STS 字段检查、产品 SDK 身份调用成功 |
| 默认凭据链及 `ALIBABA_CLOUD_CREDENTIALS_URI` | 关闭 | 原生取证、STS 字段检查、产品 SDK 身份调用成功 |

原生 URI Provider 不读取本项目的本地认证 Token。认证开启的预期是拒绝，而非成功取证；该结果不能作为降低生产认证要求的理由。需要开启认证时继续使用支持文件认证的适配器，见 [SDK 接入方案](../../docs/SDK-INTEGRATION.zh-CN.md)。

## 执行真实集群测试

前提：已有正常运行的 `akless-demo` Deployment、ConfigMap、ServiceAccount 和镜像拉取权限；监听端口默认为 61765，也支持显式配置的其他端口；默认角色为待验证的阿里云角色。执行机安装 Python 3、kubectl、Docker Buildx，并已登录目标镜像仓库。操作者在目标命名空间需能读取来源 Deployment／端口 ConfigMap，创建、读取、监听和删除测试 Pod，并在其业务容器中执行命令。

来源 Sidecar 的 `command` 仅支持未设置、空数组或 `['/usr/local/bin/idaas-akless-sidecar']`，所有启动参数必须放在 `args`。shell 包装、自定义启动程序以及在 `command` 中附加参数均在创建 Pod 前报错；请先将来源部署调整为支持的直接启动方式。测试 Pod 显式直接执行该二进制，避免镜像 entrypoint 吞掉测试参数。

运行器在创建 Pod 前读取来源 Sidecar 的 `--listen-port`（优先）、端口环境变量字面量或其 `configMapKeyRef`，未配置时使用 `61765`；端口必须在 `1–65535` 内。标记 `optional: true` 的端口引用缺少 ConfigMap 或键时使用默认值，空环境值也使用默认值；必填键缺失、其他无法解析的 `valueFrom` 和非法端口会提前报错。测试 Sidecar 的 CLI、环境变量、自检与 SDK URI 使用同一解析后的端口，也支持显式保留旧端口 `8080`。对满足上述前提的来源部署，认证场景会同时规范化 Sidecar 的 CLI 与环境变量，保留 `args` 中的配置文件路径及其他启动参数，不受来源认证开关覆盖；主 Deployment 不变。

从仓库根目录构建测试镜像，替换仓库地址：

```bash
docker buildx build --platform linux/amd64 --push \
  -t '<registry>/<namespace>/idaas-akless-sdk-test:<tag>' \
  -f verification/native-uri/Dockerfile verification/native-uri
```

从构建输出取得 digest，执行。镜像引用必须以 `@sha256:` 和完整的 64 位小写十六进制 digest 结尾；格式错误时在任何集群访问前退出：

```bash
python3 verification/native-uri/run.py \
  --context '<ACK-context>' \
  --namespace idaas-akless \
  --deployment akless-demo \
  --image '<registry>/<namespace>/idaas-akless-sdk-test:<tag>@sha256:<digest>' \
  --expected-role-arn 'acs:ram::<account-id>:role/<role-name>'
```

脚本复制现有 Deployment 的 Pod spec 创建两个独立 Pod，使用独立标签，不修改主 Deployment 或 ConfigMap。替换业务镜像及显式 `env` 列表，继续使用真实 Sidecar；认证关闭 Pod 的两端均移除名为 `credential-auth-token` 的卷和挂载。运行器禁用节点元数据／CLI 来源，并将 SDK 凭据文件指向不存在的路径。

来源应为仓库演示部署或具备同等隔离的部署。其他 `initContainers`、业务 `envFrom` 及剩余卷挂载会被继承，运行器不会清除所有可能的身份来源。应检查其中没有静态 SDK 凭据、其他凭据文件，业务也不能读取 IDaaS 配置或引导 Token。自定义本地认证卷名时需调整测试，不能假定按默认名称移除即可覆盖。

成功要求 SDK 取得真实 STS，缓存复用检查通过，使用同一 Credential 实例注入 STS 产品 SDK 后，`GetCallerIdentity` 返回预期账号与角色。错误凭据、错误身份和任何场景失败都导致非零退出；不会将“能解析字段”视为云 API 成功。运行器在 `finally` 中尝试删除 Pod；若进程被中断或清理失败，检查标签为 `test-purpose=native-credentials-uri` 的 Pod，只删除属于本次执行的名称。活跃期限仅终止执行，不会删除 Pod 对象。日志仅输出检查名称，不输出 Token、AK、SK 或 SDK 错误正文。

## 真实自动刷新长测

为上面的执行命令添加 `--wait-for-refresh`，会对两个认证关闭场景分别保持同一原生 Credential 实例，每 5 秒调用一次，等待 access-key ID 或 security token 变化。随后比较 Sidecar 当前 access-key ID 与 Provider 结果，要求过期时间延后，并再次由产品 SDK 验证云端身份；该快照比较并未逐项比较完整三元组。等待超过初始 STS 到期后 120 秒仍未刷新则失败；不会修改时钟、过期时间或内部缓存。

长测通常需要等待 STS 自然接近到期，两种 SDK 配置顺序执行，可能持续一至两小时。发现新凭据并再次验证身份后即结束，不等待首份 STS 实际到期，因此验证的是原生 Provider 的临期自动刷新，不能替代“跨过首份 STS 到期后仍持续成功”的业务验收。默认命令只验证取证、有效凭据复用及身份调用。本测试不验证具体业务云资源权限或所有语言 SDK。

单轮刷新窗口（初始 STS 到期时间加 120 秒）最多为 7200 秒，每次执行的进程超时为 7300 秒。长测 Pod 总活跃期限为 15000 秒，覆盖两轮执行、启动等待及余量；较长寿命凭据可能使两轮总耗时接近四小时。普通模式仍为 900 秒 Pod 期限。

## 本地回归

```bash
npm ci --prefix verification/native-uri --ignore-scripts --no-audit --no-fund
node --test --experimental-test-coverage \
  --test-coverage-include='verification/native-uri/integration.cjs' \
  verification/native-uri/integration.test.cjs
python3 -m unittest discover -s verification/native-uri -p run_test.py
```

本地测试使用替身校验测试程序的通过/失败判定、身份检查、刷新超时及清理行为；这些测试不能替代上面的真实集群测试。
