# 文档索引

[English](README.md) | **简体中文**

当前公开文档均提供英文和简体中文版本。可从下表选择主题，每页顶部均可切换到另一语言。

## 文档导航

| 文档 | 覆盖内容 |
| --- | --- |
| [概览与首次运行](../README.zh-CN.md) | 项目概览与首次部署 |
| [客户接入指南](CUSTOMER-INTEGRATION.zh-CN.md) | 控制台配置、客户部署、业务改造与验收 |
| [Kubernetes / ACK 部署](../deploy/README.zh-CN.md) | Kubernetes / ACK 模板及部署命令 |
| [配置契约](../deploy/CONFIGURATION.zh-CN.md) | CLI、环境变量、默认值、升级与超时策略 |
| [业务 SDK 接入](SDK-INTEGRATION.zh-CN.md) | 认证 HTTP、云 SDK 提供器及刷新职责 |
| [HTTP 响应契约](CREDENTIALS-RESPONSE.zh-CN.md) | 三云字段、raw、校验、缓存及错误 |
| [安全边界](SECURITY-HARDENING.zh-CN.md) | Token 信任、隔离、日志及运行边界 |
| [Node.js 业务示例](../business-example/README.zh-CN.md) | 无额外依赖的 Node.js HTTP 示例 |
| [三云集成测试](../business-example/multicloud/README.zh-CN.md) | 三云 STS 及只读云资源检查 |
| [原生 URI 真实集成测试](../verification/native-uri/README.zh-CN.md) | 固定版本阿里云原生 URI 认证与真实刷新检查 |
| [离线 SDK 契约验证](../verification/sdk-contracts/README.zh-CN.md) | 13 项处理器响应到 SDK 的离线字段检查 |
| [测试与验证](TESTING.zh-CN.md) | 可复用回归及各测试的证明边界 |
| [开源发布检查](RELEASING.zh-CN.md) | 源码与镜像准备、脱敏、版本及许可证检查 |
| [GitHub Actions 镜像发布](PUBLISHING-IMAGES.zh-CN.md) | ACR 用户名/密码 Secrets、amd64/arm64 发布、验收与重跑 |
| [二进制依赖许可证](../licenses/README.zh-CN.md) | 锁定的 Go 二进制依赖许可证与声明 |

## 源码对应关系

复核文档和注释时，以这些实现入口交叉检查；测试断言不能代替实现契约。

| 契约 | 实现入口 |
| --- | --- |
| CLI 默认值与优先级 | [options.go](../cmd/idaas-akless-sidecar/options.go) 及程序 `--help` |
| HTTP 参数、错误和三云字段 | [handler.go](../internal/credentials/handler.go)、[contract.go](../internal/credentials/contract.go)、[types.go](../internal/credentials/types.go) |
| 共享刷新、缓存有效期与退避 | [developerapi.go](../internal/credentials/developerapi.go)、[cache.go](../internal/credentials/cache.go)、[constants.go](../internal/credentials/constants.go) |
| 本地 Token 信任与轮换 | [auth.go](../internal/projectedauth/auth.go)、[projected.go](../internal/projectedauth/projected.go)，Linux 挂载检查在 `projected_linux.go` |
| 业务 HTTP 认证与 deadline | [credentials-client.js](../business-example/credentials-client.js)，[app.js](../business-example/app.js) 演示一次性调用 |
| 运行镜像与发布 | 四个 Dockerfile、[发布工作流](../.github/workflows/publish-images.yml) 和 [本地构建脚本](../deploy/build-images.sh) |
| 测试入口与覆盖率门槛 | [Makefile](../Makefile) 和各测试目录的 package/lock 文件，证明边界见 [测试说明](TESTING.zh-CN.md) |

## 语言与维护约定

英文使用无语言后缀的 `.md`，简体中文使用 `.zh-CN.md`；同语言正文中的链接默认指向同语言页面。修改参数名称与默认值、权限、接口契约、示例、验收边界或发行行为时，同步更新两个版本。命令、字段及标识符保留协议拼写，说明文字、注释和描述性占位符按语言翻译。

文档、模板和镜像应使用同一发行版本。许可证及 NOTICE 法律原文保留原有措辞，许可证说明提供双语。被忽略的历史归档和本地执行报告不属于当前对客文档。

代码注释重点解释代码本身不易表达的信任假设、所有权、时序和失败行为；操作步骤放在对应指南，避免在多处注释重复维护版本与默认值列表。验收记录应注明日期，并区分离线替身、容器冒烟、真实云集成和跨到期长测。
