# Testing and verification

**English** | [简体中文](TESTING.zh-CN.md)

This repository retains reusable unit tests, offline SDK contract checks and integration tests requiring an operator-provided environment. Real tenant configuration, cluster contexts, private images and execution reports are not test source. Run all commands from the repository root.

## Local regression

Requires Go 1.24+, Node.js 24+ and Python 3. First install dependencies in both pinned SDK test directories. These commands require no Kubernetes/cloud account and build/push no images.

`make check` runs the offline checks below and installs locked dependencies with `npm ci`. `make test` runs only Go tests.

If npm's default cache fails with `EACCES`/permission errors, use a disposable cache for this invocation:

```bash
npm_config_cache="$(mktemp -d "${TMPDIR:-/tmp}/idaas-npm-cache.XXXXXX")" make check
```

Go may reuse successful test results for unchanged inputs. To force fresh execution, run `go test -race -count=1 ./...`. `make check` runs SDK-runner regressions; the separate 13 actual-handler-to-SDK cases require prepared dependencies and `make check-sdk-contracts`, as described below.

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

Go tests cover startup options, local authentication, Pod binding, rotation, cache concurrency, timeouts and three-cloud response contracts. Background-refresh regressions block a controlled upstream and verify old credentials return before refresh completion in all default/raw contracts, only one concurrent refresh, waiting at the lifetime margin, cancellation, failure backoff and expiry rejection. Node.js/Python tests use substitutes for clients, runners and image scripts without real cloud access. First-time `make lint` downloads pinned tools.

## Lifecycle and aggregate gates

Lifecycle tests use real HTTP and SIGTERM in an independent subprocess, checking completion of active responses within budget, connection closure/request cancellation at timeout, and version queries without SDK initialization. `deploy/test_check.py` uses command substitutes to verify the aggregate command invokes the coverage gate and propagates failure. It also prevents LICENSE copies in isolated build contexts from drifting. No cloud access is required. `make check` invokes the authoritative multicloud npm test script with actual 80% line and branch thresholds for runtime files.

## Linux read-only mount verification

`make check-linux` needs Docker Engine and Go, without Kubernetes or a cloud account. The runner cross-compiles current tests, creates fake JWT/permission fixtures in temporary volumes, and runs `TestProjectedMountRuntime` as UID 10001/GID 20001 without capabilities. It covers accepted read-only projections, rejection of writable mounts and non-root file/directory owners, and kubelet-style directory permissions. Fixtures no longer require manual preparation.

Test containers have no network and read-only root filesystems. Only the fixture-creation container temporarily gets CHOWN/FOWNER; dedicated volumes are cleaned afterward. The default runtime image uses the root Dockerfile's fixed digest. First use may pull a public image; `python3 verification/projected-auth/run.py --image <prepared-compatible-image>` selects a local one. The executable is streamed over stdin, supporting Docker/Colima engines without shared host temporary directories. Ordinary `go test` skips real mount verification; this is not real kubelet rotation acceptance.

## Offline SDK contract checks

[verification/sdk-contracts](../verification/sdk-contracts/README.md) feeds fake credentials from the actual handler into pinned SDKs for 13 field-mapping checks. See its preparation instructions. It opens no listening ports and calls no cloud APIs; it proves neither real network acquisition nor automatic refresh.

## Live integration tests

First complete [deployment](../deploy/README.md) using your own configuration and verify the target cluster, namespace, roles and images.

| Entry point | Prerequisites and coverage |
| --- | --- |
| `deploy/integration-test.js` | Alibaba Cloud default role, authentication enabled; authentication rejection, cache, raw, query and projected-volume isolation |
| `deploy/anonymous-integration-test.js` | Independent Pod, explicitly disabled authentication, no local token mount, nonempty allowlist; anonymous acquisition and remaining query/permission boundaries |
| `deploy/rotation-integration-test.js` | Independent authenticated Pod with a 600-second local projection; observes one real kubelet rotation and previous-token grace ending |
| [native-uri](../verification/native-uri/README.md) | Alibaba Cloud Node.js native URI, authentication switch, real identity and optional near-expiry refresh |
| [multicloud](../business-example/multicloud/README.md) | Three-cloud STS, URI adapters and read-only resource queries |

The multicloud directory also has field/summary unit tests, pinned native-provider boundary tests and local HTTP component tests. `npm test --prefix business-example/multicloud` enforces at least 80% runtime line and branch coverage. Its case matrix describes remaining live identity, permission and cross-expiry acceptance.

The three deployment scripts require a Node.js business container containing the same version of `/app/credentials-client.js`. They prefer explicit `IDAAS_AKLESS_CREDENTIALS_URI`, otherwise construct the URI from the shared port, defaulting to `61765`. For example, run this in the authenticated demo Pod:

```bash
export POD_NAME='<your-test-pod>'
kubectl -n idaas-akless exec -i "$POD_NAME" -c business -- node < deploy/integration-test.js
```

Disabled-mode and rotation checks use separate Pods and run their corresponding scripts in the same way. Do not change authentication mode or token lifetime directly in a production Pod. Scripts assume an Alibaba Cloud default role and default local token path; adapt tests when customizing configuration.

Success requires process exit codes and expected assertions; Pod Ready or PASS messages alone are insufficient. Remove credentials, resource names, real tenant and cluster information before saving reports. Single rotation, near-expiry refresh and sustained operation beyond initial STS expiry are separate acceptance tasks. Long-running customer applications also require at least two real kubelet rotations and actual business API calls.
