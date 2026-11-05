# 测试与验证

[English](TESTING.md) | **简体中文**

本仓保留长期单元测试、离线 SDK 契约检查和需要自备环境的集成测试。真实租户配置、集群上下文、私有镜像和执行报告不作为测试源码提交。所有命令从仓库根目录执行。

## 本地回归

需要 Go 1.24+、Node.js 24+、Python 3。先安装两个固定版本 SDK 测试目录的依赖；以下命令不需要 Kubernetes 或云账号，不构建或推送镜像：

一条命令执行下述离线回归：`make check`（会用 `npm ci` 安装锁定测试依赖）。`make test` 仅运行 Go 测试。

若 npm 默认缓存出现 `EACCES`／权限错误，可为本次执行使用独立临时缓存：

```bash
npm_config_cache="$(mktemp -d "${TMPDIR:-/tmp}/idaas-npm-cache.XXXXXX")" make check
```

Go 对未变输入可能复用成功结果；需要强制重新执行时使用 `go test -race -count=1 ./...`。`make check` 执行 SDK 运行器的回归；另行运行 13 项真实处理器到 SDK 的离线用例，仍需按后文准备依赖并执行 `make check-sdk-contracts`。

```bash
npm ci --prefix business-example/multicloud --ignore-scripts --no-audit --no-fund
npm ci --prefix verification/native-uri --ignore-scripts --no-audit --no-fund
go test -race ./...
go vet ./...
make lint
python3 -m unittest discover -s deploy -p 'test_*.py'
python3 -m unittest discover -s verification/native-uri -p run_test.py
python3 -m unittest discover -s verification/projected-auth -p run_test.py
python3 -m unittest discover -s verification/sdk-contracts -p '*_test.py'
node --test business-example/*.test.js \
  verification/native-uri/*.test.cjs deploy/*.test.js
npm test --prefix business-example/multicloud
```

Go 测试覆盖启动参数、本地认证、Pod 绑定、Token 轮换、缓存并发、超时及三云响应契约。后台刷新回归用受控上游阻塞，验证三云默认/raw 响应在刷新完成前返回旧凭据、并发只刷新一次、临期切换为等待，以及取消、失败退避和过期拒绝。Node.js/Python 回归使用替身验证客户端、测试运行器和镜像脚本的行为，不访问真实云资源。`make lint` 首次运行需下载固定版本的工具。

## 生命周期与聚合门禁

生命周期回归使用真实 HTTP 请求和独立子进程的 SIGTERM，验证在途响应在关闭预算内完成、超时关闭连接并取消请求，以及版本查询不初始化 SDK。`deploy/test_check.py` 用命令替身验证聚合入口实际调用覆盖率门槛并传播失败，同时防止隔离镜像上下文中的 LICENSE 副本漂移；这些测试不访问云端。`make check` 会执行 multicloud 的权威 npm test 脚本，实际启用运行文件行、分支各 80% 的阈值。

## Linux 只读挂载验证

`make check-linux` 需要可用的 Docker Engine 和 Go，不需要 Kubernetes 或云账号。运行器交叉编译当前测试源码，用临时 Docker 卷创建假 JWT 和权限 fixture，再以 UID 10001、GID 20001、无 capabilities 的进程执行 `TestProjectedMountRuntime`。覆盖只读投射卷成功、可写卷拒绝、非 root 文件/目录所有者拒绝，以及 kubelet 风格目录权限；该测试不再依赖手工准备 fixture。

测试容器禁网络、根文件系统只读。仅 fixture 创建容器临时具有 CHOWN/FOWNER 能力，退出后清理专用卷。默认运行镜像沿用根 Dockerfile 的固定 digest；首次使用可能拉取公开基础镜像，可用 `python3 verification/projected-auth/run.py --image <已准备的兼容镜像>` 指定本地镜像。测试程序通过标准输入传输，兼容不共享宿主临时目录的 Docker/Colima。普通 `go test` 仍会跳过这项真实挂载测试；它不能代替真实 kubelet 的轮换验收。

## 离线 SDK 契约检查

[verification/sdk-contracts](../verification/sdk-contracts/README.zh-CN.md) 将真实处理器生成的假凭据交给固定版本 SDK，共 13 项字段映射检查。依赖准备方式见该目录说明；不监听端口或调用云 API，不证明真实网络取证或自动刷新。

## 真实集成测试

必须先用自己的配置完成 [部署](../deploy/README.zh-CN.md)，并核对目标集群、命名空间、角色及镜像。

| 入口 | 前提与覆盖范围 |
| --- | --- |
| `deploy/integration-test.js` | 阿里云默认角色、本地认证开启；认证拒绝、缓存、raw、参数及投射卷隔离 |
| `deploy/anonymous-integration-test.js` | 独立 Pod 显式关闭认证、不挂载本地 Token、设置非空角色白名单；匿名取证及剩余参数/权限边界 |
| `deploy/rotation-integration-test.js` | 独立 Pod 开启认证，本地投射 Token 为 600 秒；观察一次真实 kubelet 轮换与旧 Token 宽限结束 |
| [native-uri](../verification/native-uri/README.zh-CN.md) | 阿里云 Node.js 原生 URI、认证开关、真实身份与可选临期刷新 |
| [multicloud](../business-example/multicloud/README.zh-CN.md) | 三云 STS、URI 适配器及只读资源查询 |

三云目录另有字段/汇总单测、固定版本原生 URI SDK 边界测试及本地 HTTP 组件测试。执行 `npm test --prefix business-example/multicloud` 会检查运行代码的行和分支覆盖率各不低于 80%；详细场景和仍需真实环境完成的身份、权限及跨期长测见该目录的用例矩阵。

三个脚本都需要包含同版本 `/app/credentials-client.js` 的 Node.js 业务容器；优先使用显式 `IDAAS_AKLESS_CREDENTIALS_URI`，未配置时按共享端口构造地址，端口也省略时默认使用 `61765`。例如在默认开启认证的演示 Pod 中运行：

```bash
export POD_NAME='<your-test-pod>'
kubectl -n idaas-akless exec -i "$POD_NAME" -c business -- node < deploy/integration-test.js
```

关闭模式与轮换测试使用各自独立 Pod，然后以同样方式执行对应脚本。不要直接修改生产 Pod 的认证模式或投射周期。默认阿里云角色、默认本地 Token 路径是这些脚本的前提；自定义配置需要同步调整测试。

测试以进程退出码和预期检查结果判定成功，Pod Ready 或仅有 PASS 日志不算完整通过。保存报告前去除凭据、资源名称、真实租户及集群信息。单次轮换、临期刷新和跨过首份 STS 到期的持续运行分别验收；客户长期运行还需至少两次 kubelet 轮换及实际业务 API 调用。
