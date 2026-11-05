# GitHub Actions 双架构镜像发布

[English](PUBLISHING-IMAGES.md) | **简体中文**

使用 ACR 控制台“访问凭证”中已配置的 Registry 用户名和固定密码，从 GitHub Actions 发布 Sidecar 与业务示例的 `linux/amd64`、`linux/arm64` 镜像。这里的密码不是 RAM AccessKey Secret，也不是阿里云控制台登录密码。

## 1. 最小依赖及执行顺序

版本 tag → checkout → QEMU → Buildx → 从 GitHub Secrets 读取 ACR 用户名/密码直接登录 Registry → 两个镜像分别双架构构建并推送 → 验证远端索引 digest 与两平台运行结果。

入口为 `.github/workflows/publish-images.yml`，四个官方 Actions 固定完整 SHA。复用现有 Dockerfile，`deploy/build-images.sh` 保留本地单架构用途。无需创建 RAM AccessKey、OIDC 提供商或发布角色，也不调用 GetAuthorizationToken 换取临时密码。宿主机无需额外安装项目 Go/Node/Java 工具链。

本工作流不自动部署 Kubernetes、不执行真实云资源测试、不更新 latest、不发布测试镜像、不生成 SBOM/签名。正式创建 tag 前仍需完成发行候选测试。

四个 Dockerfile 的最终运行层统一为 Alibaba Cloud Linux 3.13.4。Go 编译阶段保留 Go/Alpine；Node.js 24.22.2 和 npm 从固定 digest 的官方 glibc 镜像提取到 ALinux，并安装运行依赖、保留不同的非 root UID。这些制品阶段不是最终交付的操作系统层。ALinux 和工具链默认值在 Dockerfile 中锁定多架构索引 digest，发布检查会在两个平台核对 /etc/os-release、UID 10001/1000 和 Node 24。

## 2. 首次发布前的准备

1. 按 `docs/RELEASING.zh-CN.md` 清理并复核候选内容和拟公开历史；不能直接公开含真实测试环境信息的旧历史。
2. 发布 GitHub 仓库为 `cloud-idaas/idaas-akless-sidecar`，准备 ACR 中预建的 `idaas-akless-sidecar`、`idaas-akless-business-example` 两个仓库。
3. 使用已批准且 GitHub runner 可达的 Registry 入口。本工作流不修改网络访问控制，普通 GitHub 托管 runner 不具备你的 VPC 连接。
4. 确认这组凭据所属账号可向两个目标仓库推送和拉取。已具备权限时不重复授权；只有 docker login 成功不能证明有推送权限。权限不足时按第 4 节处理。
5. 镜像可见性及消费者拉取方式与源码是否公开分别配置。
6. 构建网络需能访问固定输入镜像仓库、Go 模块源及 `https://mirrors.aliyun.com/` 的 ALinux OS/updates RPM 源；SDK 测试镜像还需访问 npm 仓库。

## 3. 获取 ACR 用户名和固定密码

### 3.1 直接使用已创建的访问凭据

复用 ACR 控制台中已有的访问凭据，不需要另建 RAM AccessKey，也不需要创建 OIDC 提供商。建议使用权限范围适合发布的 RAM 用户所属凭据；不要为了发布扩大到整个账号的管理员权限。

控制台路径：ACR → 选择目标地域 → 实例列表 → 目标实例 → 实例管理 → 访问凭证。复制页面给出的 docker login 命令中的完整用户名和 Registry 主机名，使用该身份设置的 Registry 固定密码。

RAM 用户名可能包含 `@` 和账号默认域名，必须照控制台命令原样复制；不要自行替换为纯数字账号 ID、显示名称或 AccessKey ID。不同 RAM 用户的固定密码彼此独立。

### 3.2 尚未设置或遗忘固定密码

在目标身份登录的 ACR 访问凭证页面设置固定密码。有可用凭据就跳过此步骤，不主动重置。RAM 用户进入控制台需要相应控制台访问权限；缺少操作权限时请账号管理员协助，不是再创建 OIDC 提供商。

密码遗忘时，按控制台流程重新设置；先确认其他镜像推拉任务是否依赖旧密码，再安排更新。固定密码没有固定到期时间，直到重置或撤销前持续有效；GitHub job 完成后的 docker logout 不会撤销云侧密码。

### 3.3 保存到 GitHub Secrets

GitHub 仓库 → Settings → Secrets and variables → Actions → Secrets → New repository secret：

| Secret | 实际值来源 |
| --- | --- |
| `ACR_USERNAME` | ACR 访问凭证页面 docker login 命令中的完整用户名 |
| `ACR_PASSWORD` | 在同一身份下设置的 Registry 固定密码 |

密码按原样录入，不附加引号、JSON、空格或换行。不要把实际密码写进钉钉文档、源码、Variables、镜像、构建参数或日志，也不要开启 set -x。GitHub 保存后不能读回 Secret 明文，可以更新替换。

变更密码时协调依赖方：更新 ACR 固定密码和 GitHub `ACR_PASSWORD`，再在受保护 tag 上验证；必要时更新其他使用者。不要未经评估重置共享凭据。

## 4. 是否还需要额外授权

创建访问凭据负责认证，不会自动增加仓库权限。ACR 登录后仍按凭据所属账号的 RAM 策略鉴权。

- 已可向目标两个仓库 push/pull：无需新增授权。
- 仅 docker login 成功：还需确认仓库推送、拉取权限。
- RAM 用户出现 push denied/403：让管理员补充对应两个仓库的权限；不要重新创建 AccessKey 或 OIDC 提供商。

仅在现有 RAM 用户缺少权限时，管理员可参考下列同账号 ACR 企业版策略，替换占位符并绑定到该凭据所属用户。直接使用固定用户名/密码时不需要额外授予 cr:GetAuthorizationToken 来换临时凭据。

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["cr:PushRepository", "cr:PullRepository"],
      "Resource": [
        "acs:cr:<REGION>:<ACCOUNT_ID>:repository/<INSTANCE_ID>/<NAMESPACE>/idaas-akless-sidecar",
        "acs:cr:<REGION>:<ACCOUNT_ID>:repository/<INSTANCE_ID>/<NAMESPACE>/idaas-akless-business-example"
      ]
    }
  ]
}
```

不授予管理员、CreateRepository 或 DeleteRepository。跨账号权限需另行核对，不通过创建凭据绕过显式 Deny。

## 5. 配置来源：3 个 Variables + 2 个 Secrets

非敏感参数在 Settings → Secrets and variables → Actions → Variables 中创建；凭据在同一位置的 Secrets 页签创建。

| 名称 | 类型 | 获取位置 |
| --- | --- | --- |
| `RELEASE_REPOSITORY` | Variable | `cloud-idaas/idaas-akless-sidecar`，与 github.repository 完全一致 |
| `ACR_REGISTRY` | Variable | ACR 访问凭证页 docker login 的 Registry 主机名，不含协议、路径、末尾斜杠；不是 OpenAPI 地址 |
| `ACR_NS` | Variable | ACR 两个目标仓库实际所在的命名空间；不是 Kubernetes Namespace |
| `ACR_USERNAME` | Secret | 同一访问凭证页面 docker login 命令里的完整用户名，保留 `@` 等字符 |
| `ACR_PASSWORD` | Secret | 该身份在 ACR 中设置的 Registry 固定密码 |

工作流不再要求 ACR_REGION、ACR_INSTANCE_ID、RAM AccessKey、角色 ARN、Provider ARN、GitHub 数字 ID 或 subject。旧配置若无其他使用方，可按组织流程清理；不因此删除共享云资源或重置凭据。

第 4 节仅在缺权限时需要替换的策略值：`<ACCOUNT_ID>` 是 ACR 所属云账号 ID，控制台头像处可查；`<REGION>` 和 `<INSTANCE_ID>` 从目标实例概览/实例信息取得；`<NAMESPACE>` 是两个仓库所在的 ACR 命名空间。这些不是当前工作流必填变量。

配置顺序：确认最终 GitHub 仓库和两个 ACR 仓库 → 获取现成用户名/固定密码并确认权限 → 填 3 个 Variables 和 2 个 Secrets → 保护 v* tag 和工作流修改权限 → 按第 6 节发布。仅允许发行维护者创建、修改、删除和绕过 tag 规则。

工作流只需 contents: read，使用四个完整 SHA 固定的 Actions；不响应 PR。fork 默认没有匹配的 RELEASE_REPOSITORY 和发布 Secrets。分支手工执行会被跳过。

## 6. 准备并发布一个版本

1. 把发布 workflow 和配套文档纳入发行源码，核对源码、文档、镜像来自同一个 commit。将复核后的候选内容同步到 `https://github.com/cloud-idaas/idaas-akless-sidecar.git`；推送内部 GitLab 不会触发 GitHub Actions。手工触发还要求该 workflow 已存在于 GitHub 默认分支。
2. 完成 make check、make check-linux 及本版本适用的 SDK/真实环境验收。make check 不包含完整的 13 项 SDK 契约执行；需要时按 verification/sdk-contracts/README.zh-CN.md 准备依赖，再执行 make check-sdk-contracts。
3. 在干净的 GitHub 发行检出中确认 origin 指向最终公开仓库，确认当前 HEAD 是要发布的提交。
4. 创建并推送一个从未发布的新版本 tag，例如：

```bash
git status --short
git remote get-url --push origin
git rev-parse HEAD
git tag -a v1.2.3 -m "Release v1.2.3"
git push origin refs/tags/v1.2.3
```

v1.2.3 只是操作示例，不代表已经发行。允许 vMAJOR.MINOR.PATCH 及 -rc.1 等不含 + 的预发布后缀；工作流不更新 latest。创建 tag 前务必核对目标 commit，受保护 tag 不应移动到另一个提交。

5. 在 Actions → Publish multi-platform images 中查看对应 tag 的运行。
6. Sidecar 将 tag 名作为 `VERSION`、checkout 后的完整 commit 作为 `REVISION` 嵌入产物；每个镜像使用一次 `--platform linux/amd64,linux/arm64 --push`，首期关闭 provenance/SBOM 以保持两平台索引与最小依赖。

## 7. 成功标准与混合集群使用

只有完整 job 成功，并生成两个镜像的验证摘要后才算本轮发布通过：

- ACR 中两个 tag 均指向包含 amd64、arm64 的多架构索引。
- 远端 tag digest 必须匹配各自 Buildx 构建记录的 containerimage.digest；tag 被覆盖时校验失败。从该不可变索引读取平台描述符，再按各平台的子 manifest digest 运行检查，避免经典 Docker 存储在同一索引引用下切换架构时发生本地 digest 冲突。交付部署时仍使用索引 digest。
- 两种平台的 Sidecar --version 与 tag/完整 commit 一致。
- 两种平台的 Node 架构、业务模块加载和本地 HTTP /healthz 检查均成功。
- 通过镜像索引 digest 引用，不能用单个平台子 manifest 的 digest 作为混合集群的统一镜像。

GitHub job 摘要会给出两个 name@sha256:... 的索引引用。混合集群使用同一个索引引用，容器运行时根据节点架构选择相应镜像。

运行验证在 QEMU 支持下执行，属于跨架构基础冒烟，不证明 ARM 原生节点上的全部业务行为，也不替代真实 IDaaS/云资源权限、投射 Token 轮换或 STS 跨期运行验收。业务示例启动时会记录取证错误后继续运行，进程存活不能当作真实取证成功。

## 8. 失败、重跑和回退

| 现象 | 检查与处理 |
| --- | --- |
| 发布 job 被跳过 | RELEASE_REPOSITORY 是否匹配；是否选中了 tag；是否为删除 tag 事件 |
| Secret 缺失或凭据无效 | ACR_USERNAME/ACR_PASSWORD 是否填在 Secrets，是否为同一身份的 Registry 凭据 |
| ACR 登录失败 | 复制控制台的完整用户名，核对固定密码与 Registry；不使用 AccessKey 或控制台密码 |
| push denied | 两个仓库是否预建，RAM 仓库 ARN 是否正确，网络访问条件是否满足 |
| exec format error | QEMU 是否初始化；远端两平台 manifest 与目标二进制是否正确 |
| 切换运行验证架构时出现 `cannot overwrite digest` | 从已核对的不可变索引选择各平台子 manifest digest 运行；经典 Docker 无法让同一本地索引引用关联两个平台的镜像 ID |
| 双架构缺一项 | 不得把同一个 tag 分别推两次单架构镜像；使用一次双架构 push |
| Remote tag no longer matches this build | 检查其他发布者是否覆盖 tag；核对本次构建 digest 或发布新版本，不把被替换的镜像当作本轮产物 |
| 固定密码被重置或撤销 | 同步更新 ACR_PASSWORD，确认依赖方后重跑；不适用临时密码 1 小时限制 |
| 一个镜像成功，另一个失败 | 两仓库发布不是原子事务；不要把部分产物当作已验收发行物 |

手工重跑应选择同一个版本 tag，可在 GitHub Actions 对该 tag 的运行执行 Re-run jobs，或用 workflow_dispatch 选择该 tag。分支手工执行会被跳过，应在已保护的版本 tag 上重跑。

如 ACR 开启版本不可变，已有 tag 可能拒绝重推；保留已知 digest，按实际失败阶段处理。若不能在不覆盖已有发行物的前提下完成重试，发布新的修订版本；不要关闭不可变策略或强推源码 tag。重新构建涉及包源等输入，不能保证每次产生相同 digest。

回退部署时使用此前已验收的多架构索引 digest。workflow 不修改 ACK，不创建或回滚集群资源。

## 9. 当前证据与后续责任

2026-11-05 的 ALinux 迁移验证已通过四个镜像的双架构构建、八组镜像/平台运行检查、六项 Linux 投射卷检查和 `make check`。独立 amd64 ACK 测试通过认证模式 15 项、匿名模式 13 项、多云矩阵 9/9、阿里云原生路径 2/2、原生 SDK 四种模式/认证组合，以及真实 kubelet Token 轮换和旧 Token 宽限期结束后的拒绝检查。两个候选运行镜像索引已推送 ACR，并使用其不可变 digest 在本地实跑通过工作流中的远端索引和运行检查步骤。本次使用存在修改的工作区候选，正式验收仍须采用经过复核的干净发行 tag。

上述结果不代表已完成 GitHub 托管 runner 的 Secrets 用户名/密码 → ACR 工作流验收；尚未覆盖 ARM 实机集成和跨真实 STS 到期的运行。三个固定输入镜像索引均提供 amd64/arm64，后续输入或运行时变更仍需重新验收。

仓库改动完成后，仍需管理员配置本手册的外部资源，并在最终 GitHub 仓库进行首个受保护 tag 的端到端验收。首期不要求新增独立 PR CI、真实云测试工作流、签名、SBOM、原生 ARM runner 或自动部署。

## 10. 官方参考

- ACR 访问凭证：https://help.aliyun.com/zh/acr/user-guide/configure-access-credentials
- ACR RAM 权限：https://help.aliyun.com/zh/acr/user-guide/ram-authorization-information
- GitHub Secrets：https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets
- 官方 ACR 登录 Action：https://github.com/aliyun/acr-login
- Docker 双架构发布：https://docs.docker.com/build/ci/github-actions/multi-platform/
- Buildx 构建元数据与 digest：https://docs.docker.com/reference/cli/docker/buildx/build/#metadata-file
- GitHub 手工触发条件：https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_dispatch
