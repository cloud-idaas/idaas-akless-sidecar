# 开源发布检查

[English](RELEASING.md) | **简体中文**

## 发布内容

保留运行代码、构建文件、占位配置、当前接入/安全文档和 [可复用测试](TESTING.zh-CN.md)。不提交真实租户配置、云账号角色、私有镜像地址、集群上下文、临时探针、执行报告或废弃设计快照。配置放在仓库外的受保护目录；`.gitignore` 只能防止误加入，不能清除已跟踪文件或历史提交。

所有公开示例使用明确占位符或虚构 fixture，真实集成测试从操作者配置读取角色。Docker 构建上下文采用允许列表，镜像不携带本地配置与测试报告。

交付的 Sidecar、业务示例和两个 SDK 测试镜像必须在 /etc/os-release 中报告 ID=alinux，运行底座采用 Alibaba Cloud Linux 3.13.4，保留 Sidecar UID 10001 和 Node UID/GID 1000。Go/Alpine 和官方 Node glibc 镜像仅作为构建或制品阶段。升级固定输入时复核 amd64/arm64 描述、运行库、CA 信任及许可证，保留 Node 的 /usr/share/licenses/nodejs/LICENSE 和 npm 自带许可。

## 验证发行候选

1. 执行 [本地回归与适用的集成测试](TESTING.zh-CN.md)，确认没有删除长期测试或以假凭据结果替代云端验证。
2. 用 Gitleaks 扫描候选内容和拟公开的所有 Git 历史，输出强制脱敏；逐条核对命中，不能为通过扫描而忽略整个测试或文档目录。
3. 人工复查实例/应用标识、账号/角色、内部域名、私有 registry、资源名称、个人路径和运维上下文。这些信息未必被密钥扫描器识别。
4. 检查文档链接、CLI/环境变量、权限 scope、Token 挂载和 SDK 版本边界，与本版本实现一致。
5. 确认公开源码 URL、版本标签、公开镜像地址和 digest；在无内部登录凭据的环境验证源码下载、匿名镜像拉取及所宣称架构。
6. 从发布源码构建镜像，在隔离环境按客户接入指南验证身份、业务权限、Token 轮换与 STS 跨期运行。源码、模板、文档与镜像必须属于同一版本。
7. 镜像内的 `/usr/share/licenses/idaas-akless-sidecar/` 应包含项目许可证及 [二进制依赖许可证](../licenses/README.zh-CN.md)。依赖升级时同步复核适用的版权及声明；源码包也保留这些材料。基础镜像及完整发行物的许可证、SBOM 与实际交付材料仍需按发行候选核验。

Gitleaks 8.30.1 命令示例：以下 Git 扫描覆盖所有本地 refs，目录扫描针对仓库外准备的候选目录（不含 `.git`、依赖缓存及真实本地配置）：

```bash
gitleaks git . --log-opts=--all --redact --no-banner
gitleaks dir '<release-candidate-directory>' --redact --no-banner
```

工作区清理不能移除历史对象。仅将清理后且已复核的候选内容导入公开仓库，不复制私有提交历史，并复核所有拟公开的提交、分支和标签。

目标 GitHub 仓库为 `https://github.com/cloud-idaas/idaas-akless-sidecar.git`。发布前检查该仓库实际内容和 push URL；内部 GitLab 的 origin 不会发布到 GitHub。采用全新历史导入清理后的候选内容时，只向独立检出复制已复核的源码文件，排除 .git、忽略的本地文件、缓存和报告，不 mirror 私有仓库，也不推送旧仓库的全部分支/标签。先把 workflow 放入 GitHub 默认分支再使用手工触发，发行 tag 仅指向已复核的 GitHub 提交。

若发现真实密钥、私钥或 Token，先由凭据负责人吊销或轮换，再清理候选内容和历史；删除文件不能使已经泄露的凭据失效。不要在 issue、PR、测试日志或扫描报告中复制原值。

当前文档中的 `<...>` 发行信息必须在正式发布时补齐；占位符不代表已发布的公开地址或镜像。

## 发行身份与镜像许可证

自动发布 amd64/arm64 镜像到 ACR 企业版，参见 [GitHub Actions 镜像发布](PUBLISHING-IMAGES.zh-CN.md)，将现有 Registry 用户名/密码存入 GitHub Secrets 并使用版本 tag 触发；已有目标仓库推拉权限时无需新增授权。`deploy/build-images.sh` 继续用于本地单架构构建，不要分别将两个架构推送到同一个标签。创建发行 tag 前仍需完成上面的候选验证。

在已经复核的干净发行检出中，用 `make build VERSION=<发行版本> REVISION=<完整源码 commit>` 构建二进制。直接构建 Sidecar 镜像时传入 `--build-arg VERSION=<发行版本> --build-arg REVISION=<完整源码 commit>`；两者同时写入二进制与 OCI version/revision 标签。`deploy/build-images.sh` 将 `IMG_TAG` 作为版本，revision 默认取 Git 描述，可通过 `REVISION` 显式覆盖。正式发行应显式指定已核验的完整 commit，不能将工作区默认 `dev`、`unknown` 或带 `-dirty` 的描述当作正式来源凭证。

核对镜像中的 `/usr/local/bin/idaas-akless-sidecar --version` 和 OCI 标签，与源码 tag、镜像 digest 及验收记录对应。版本字符串只是构建声明，不能替代来源验证或镜像签名。

Sidecar、Node 业务示例及两个 Node 集成镜像均应携带 `/usr/share/licenses/idaas-akless-sidecar/LICENSE`。隔离构建上下文分别在 `business-example/LICENSE`、`verification/native-uri/LICENSE` 保存根 LICENSE 的一致副本，变更根许可证时同步；`make check` 会阻止副本漂移。Sidecar 另携带 Go 依赖许可，Node 依赖和基础镜像的完整许可/SBOM 仍按实际发行物核验。
