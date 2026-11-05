# Node.js HTTP 业务示例

[English](README.md) | **简体中文**

使用 Node.js 内置模块从同 Pod 的 Sidecar 获取阿里云临时凭据，无需安装依赖或集成 IDaaS SDK。

## 运行

需要 Node.js 24 或更高版本。先在同一主机启动 Sidecar，从本目录执行：

```bash
export IDAAS_AKLESS_CREDENTIALS_URI=http://127.0.0.1:61765/credentials
# 可选；未设置时使用 URI 中的角色或 Sidecar 默认角色。
export IDAAS_AKLESS_CLOUD_ROLE_ARN='<authorized-alibaba-cloud-role>'
npm start
```

默认开启认证，必须只读挂载本地认证 Token，默认路径 `/run/secrets/idaas-akless/credential-auth/token`，路径可由 `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` 覆盖。不要挂载IDaaS引导Token。示例在启动时取证一次，401仅重读文件并重试一次；成功只输出过期时间，失败只输出错误信息。`/healthz` 和首页不依赖取证结果。

若服务端已显式关闭本地认证，业务也设置 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false`。仅接受 `true/false`，默认 `true`；关闭后不读取认证文件、不发送 Authorization，401不重试。任何文件或HTTP错误都不会让开启模式自动转为匿名。生产推荐保持认证开启；关闭会失去这层SSRF凭据保护，Sidecar仍需IDaaS引导身份。默认模板由 ConfigMap 的 `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` 同时配置两端，保持字符串 `"true"`；修改后需重建 Pod，见 [配置契约](../deploy/CONFIGURATION.zh-CN.md#显式关闭本地认证)。

成功日志示例：

```text
[ok] got STS credentials from sidecar, expires at <future-time>
```

另开一个本机终端运行 `curl -fsS http://127.0.0.1:3000/healthz`，预期输出 `ok`。若日志只有 `[err]`，先检查 Sidecar 是否已启动、默认或请求角色是否配置，再按需重启示例重试。HTTP 存活检查成功不能代替凭据获取成功。

## 业务如何使用

复用 [credentials-client.js](credentials-client.js) 导出的 helper，完整函数示例见 [SDK 接入方案](../docs/SDK-INTEGRATION.zh-CN.md)。[app.js](app.js) 是可执行的演示入口，加载时会构造 URI／角色并启动 HTTP 服务；其中的 `fetchCredential()` 是内部函数，不是库导出。

长期业务需在到期前刷新，并处理失败与重试；启动时取一次不能保证长期有效。示例按阿里云响应读取，AWS/腾讯云字段和 SDK 提供器接入见 [SDK 接入方案](../docs/SDK-INTEGRATION.zh-CN.md)。

| 配置 | 默认值 |
| --- | --- |
| `IDAAS_AKLESS_CREDENTIALS_URI` | 显式 URI 优先；未设置时按共享端口构造，端口也省略时为 `http://127.0.0.1:61765/credentials`，仅支持 HTTP 默认响应 |
| `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | 可选共享端口，缺失或空值为 `61765`；仅在未设置完整 URI 时生效 |
| `IDAAS_AKLESS_CLOUD_ROLE_ARN` | 空；非空时覆盖 URI 的 `roleArn`，值为云角色外部 ID；其余情况使用 URI 角色或 Sidecar 默认角色 |
| `IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS` | `15000`，单位毫秒 |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | `true`，与Sidecar模式一致，仅接受 `true/false` |
| `IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE` | `/run/secrets/idaas-akless/credential-auth/token`，每次取证重读、裸JWT认证 |
| `PORT` | `3000` |

完整规则见 [配置契约](../deploy/CONFIGURATION.zh-CN.md#参数命名与跨层映射)。`app.js` 通过 `configuredCredentialsURI()` 处理完整 URI、共享端口和默认值，再读取角色及超时并传给 `fetchJson`；`fetchJson` 本身只自动读取本地认证相关环境变量。`IDAAS_AKLESS_CLOUD_ROLE_ARN` 选择本次请求角色，Sidecar 的 `IDAAS_AKLESS_DEFAULT_ROLE_ARN` 提供未指定角色时的默认值，两者用途不同。不要将临时 AK、SK、Token 写入日志。

本地认证 Token 文件环境变量未设置或为空时使用默认路径，与 Sidecar 一致；显式调用 `fetchJson` 并传入空文件路径仍会报错。

## 构建与验证

从仓库根目录构建 amd64 镜像：

```bash
docker buildx build --platform linux/amd64 --load \
  -f business-example/Dockerfile -t idaas-akless-business-example:local business-example/
npm test --prefix business-example
```

同 Pod 容器共享 loopback；两个独立 Docker 容器的 loopback 不互通。业务只读挂载独立受众的本地认证 Token，不挂载IDaaS配置或引导Token。客户端仅向数字loopback的/credentials发送Token，禁代理、禁重定向，正文有界。部署和真实云身份验证见 [部署指南](../deploy/README.zh-CN.md)。

接入自己的业务时，复用 HTTP 客户端与字段映射，并实现业务所需的刷新/重试；不必复制用于演示的首页和 `/healthz`。如果选择 SDK 原生提供器，应按 [SDK 接入方案](../docs/SDK-INTEGRATION.zh-CN.md) 初始化客户端，不能只为本示例设置 SDK URI 变量就认为它已使用该提供器。
