# SDK response verification

**English** | [简体中文](README.zh-CN.md)

Feed fake credentials emitted by the Go handler into real SDKs to check current three-cloud field mapping. Dependencies are isolated from the Sidecar and business example.

| Cloud | Checks |
| --- | --- |
| Alibaba Cloud | Explicit URI providers and default chains in Go, Python, Java and Node.js: 8 cases |
| AWS | Container providers in Go, Python and Node.js: 3 cases |
| Tencent Cloud | DTO-to-Credential mapping in Python and Node.js: 2 cases |

Probes use in-memory HTTP/metadata fetcher substitutes, without listening ports or cloud calls. Python AWS feeds JSON directly to its fetcher, checking fields only. Tencent cases do not verify automatic refresh. These tests establish neither real network/TLS behavior, continuous refresh nor cloud API authorization.

For real Alibaba Cloud Node.js native URI acquisition, cloud identity and optional refresh, use the separate [live integration tests](../native-uri/README.md). Do not treat offline results as cluster results.

## Preparation

Requires Python 3.11+, Node.js 24, Go 1.24+, JDK 11+ and Maven. Node/Python transitive dependencies are locked in `node/package-lock.json` and `requirements.lock`; Go uses `go.mod/go.sum`; the Maven dependency plugin version is pinned in the POM. Run from this directory:

```bash
python3 -m venv venv
venv/bin/python -m pip install --require-hashes -r requirements.lock
npm ci --prefix node --ignore-scripts --no-audit --no-fund
(cd go && go build -o ../go-probe .)
mvn -f java/pom.xml dependency:build-classpath -Dmdep.outputFile="$PWD/java-classpath.txt"
javac --release 11 -cp "$(cat java-classpath.txt)" JavaProbe.java
```

If PyPI downloads time out on your network, retry installation with an accessible public mirror while retaining hash verification. For example, the [Alibaba Cloud PyPI mirror](https://developer.aliyun.com/mirror/pypi) can be selected for this command only:

```bash
venv/bin/python -m pip install --index-url https://mirrors.aliyun.com/pypi/simple/ \
  --require-hashes -r requirements.lock
```

## Run

From the repository root:

```bash
python3 verification/sdk-contracts/check_endpoint_responses.py \
  --output /tmp/idaas-sdk-results.json
```

Alternatively run `make check-sdk-contracts` at the root. All 13 cases must receive complete fake credentials for a successful exit. Results include execution time, cases, response/lock hashes, installed Node/Python/Go versions and Java JAR hashes, without registry URLs or local paths. Java's actual resolution is recorded; a manifest hash is not a hash of the whole installed dependency tree. Applications must still verify real identity, permissions and expiry refresh using [SDK integration](../../docs/SDK-INTEGRATION.md).

For Python upgrades, regenerate and review the hash-locked file from `requirements.txt`:

```bash
uv pip compile requirements.txt --universal --python-version 3.11 --generate-hashes \
  --no-annotate --no-header --index-url https://pypi.org/simple --output-file requirements.lock
```

For Node upgrades, commit the regenerated `node/package-lock.json`; do not manually remove/change integrity hashes. Prepare dependencies again, run the 13 cases and inspect actual version changes.

## Known dependency maintenance item

Review dated 2026-11-05: pinned `tencentcloud-sdk-nodejs-common@4.1.220` introduces `uuid@9.0.1`. The latest upstream version checked that day still used that dependency range. `npm audit` associates [GHSA-w5hq-g745-h8pq](https://github.com/uuidjs/uuid/security/advisories/GHSA-w5hq-g745-h8pq) with this tree. The advisory concerns v3/v5/v6 with external buffer/offset arguments. This probe only constructs `BasicCredential`; the inspected SDK common code calls v4. No affected invocation was found in the current verification path. This dependency is not linked into the Go Sidecar.

Retain this as a maintenance item pending a compatible upstream upgrade. Do not label it a production Sidecar vulnerability or clear scan findings by removing locks, forcing an SDK downgrade or applying an unverified major-version override. After upgrading, recheck call paths, the installed tree and all 13 contracts. Record scan findings separately from current-path reachability.
