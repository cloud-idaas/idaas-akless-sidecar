# SDK 响应验证

[English](README.md) | **简体中文**

将 Go 处理器输出的假凭据交给真实 SDK，检查当前三云响应的字段映射。依赖与 Sidecar、业务示例隔离。

| 云 | 检查项 |
| --- | --- |
| 阿里云 | Go、Python、Java、Node.js 的显式 URI 提供器和默认链，共 8 项 |
| AWS | Go、Python、Node.js 容器提供器，共 3 项 |
| 腾讯云 | Python、Node.js DTO 到 Credential 的映射，共 2 项 |

探针使用内存 HTTP 或 metadata fetcher 替身，不监听端口、不调用云 API。Python AWS 项直接向 fetcher 返回 JSON，只验证字段映射；腾讯云两项不验证自动刷新。测试不证明真实网络、TLS、连续刷新或云 API 授权。

阿里云 Node.js 原生 Credentials URI 的真实网络取证、身份调用及可选刷新长测，使用独立的 [真实集成测试](../native-uri/README.zh-CN.md)。不要将这里的离线结果与真实集群结果混用。

## 准备

需要 Python 3.11+、Node.js 24、Go 1.24 或更高版本、JDK 11 或更高版本及 Maven。Node 和 Python 的传递依赖分别锁定在 `node/package-lock.json`、`requirements.lock`；Go 使用 `go.mod/go.sum`，Maven 依赖插件版本固定在 POM 中。以下命令从本目录执行：

```bash
python3 -m venv venv
venv/bin/python -m pip install --require-hashes -r requirements.lock
npm ci --prefix node --ignore-scripts --no-audit --no-fund
(cd go && go build -o ../go-probe .)
mvn -f java/pom.xml dependency:build-classpath -Dmdep.outputFile="$PWD/java-classpath.txt"
javac --release 11 -cp "$(cat java-classpath.txt)" JavaProbe.java
```

若当前网络下载 PyPI 包超时，可单次指定可达的公开镜像源，并保留哈希校验。例如使用[阿里云 PyPI 镜像](https://developer.aliyun.com/mirror/pypi)，不修改全局配置：

```bash
venv/bin/python -m pip install --index-url https://mirrors.aliyun.com/pypi/simple/ \
  --require-hashes -r requirements.lock
```

## 执行

从仓库根目录执行：

```bash
python3 verification/sdk-contracts/check_endpoint_responses.py \
  --output /tmp/idaas-sdk-results.json
```

也可从仓库根目录运行 `make check-sdk-contracts`。13 项均取得完整假凭据才成功退出。结果包含运行时间、用例、响应/锁文件哈希，以及实际安装的 Node/Python/Go 依赖版本和 Java JAR 哈希；不记录 registry URL 或本机目录。Java 实际解析结果也会记录，不能将清单哈希视为完整安装树哈希。业务侧仍需按 [接入方案](../../docs/SDK-INTEGRATION.zh-CN.md) 验证真实身份、权限及过期刷新。

升级 Python 依赖时，以 `requirements.txt` 为输入重新生成并评审锁文件：

```bash
uv pip compile requirements.txt --universal --python-version 3.11 --generate-hashes \
  --no-annotate --no-header --index-url https://pypi.org/simple --output-file requirements.lock
```

Node 升级后提交新的 `node/package-lock.json`；不要手工删改完整性哈希。重新准备依赖后运行 13 项检查，并核对报告中的实际版本变化。

## 已知依赖维护事项

2026-11-05 复核：当前锁定的 `tencentcloud-sdk-nodejs-common@4.1.220` 引入 `uuid@9.0.1`；同日查询的上游最新版仍使用该版本范围。`npm audit` 会将 [GHSA-w5hq-g745-h8pq](https://github.com/uuidjs/uuid/security/advisories/GHSA-w5hq-g745-h8pq) 关联到该依赖树。公告涉及 v3/v5/v6 的外部 buffer/offset 路径；本目录探针只构造 `BasicCredential`，所检 SDK common 代码的 UUID 调用为 v4，未发现当前验证路径触发公告条件。该依赖不进入 Go Sidecar 二进制。

保留为待上游兼容升级的维护事项；不将其写成生产 Sidecar 漏洞，也不以移除锁文件、强制降级 SDK 或未经验证的跨主版本覆盖来清零扫描结果。升级后复核调用路径、实际依赖树和 13 项字段契约；扫描结果与当前不可达性应分别记录。
