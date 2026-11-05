# Sidecar 二进制依赖许可证

[English](README.md) | **简体中文**

运行镜像将项目根目录的 `LICENSE` 放在
`/usr/share/licenses/idaas-akless-sidecar/LICENSE`，并将本目录放在
`/usr/share/licenses/idaas-akless-sidecar/third-party/`。

| 组件 | 版本 | 许可证副本 |
| --- | --- | --- |
| IDaaS Go Core SDK | v0.1.0-beta.1 | [Apache-2.0](idaas-go-core-sdk.LICENSE)、[NOTICE](idaas-go-core-sdk.NOTICE) |
| golang-jwt/jwt/v4 | v4.5.2 | [MIT](golang-jwt.LICENSE) |
| google/uuid | v1.6.0 | [BSD-3-Clause](google-uuid.LICENSE) |
| Go 运行时与标准库 | 构建二进制使用的编译器版本 | [BSD-3-Clause](go.LICENSE) |

许可证文本复制自锁定版本的 Go 模块源码和 Go 发行物。变更依赖时，
应与 `go.mod/go.sum` 一起复核并更新这些副本及适用的第三方声明。
基础镜像的操作系统包是独立组件；本目录不是完整的镜像 SBOM。
Node/Python/Java 验证 SDK 不链接到 Sidecar 二进制中。
