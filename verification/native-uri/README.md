# Live Alibaba Cloud SDK native Credentials URI tests

**English** | [简体中文](README.zh-CN.md)

These tests use Node.js `@alicloud/credentials` 2.4.7 and `@alicloud/sts20150401` 1.2.0. The test image is separate from the business demo; dependencies are pinned by `package-lock.json`. They exercise real network credential acquisition and cloud identity without replacing HTTP transport, SDK provider, clocks or responses.

Four combinations:

| SDK configuration | Sidecar authentication | Expected |
| --- | --- | --- |
| Explicit `type: credentials_uri` | Enabled | SDK fails with HTTP 401 |
| Default chain and `ALIBABA_CLOUD_CREDENTIALS_URI` | Enabled | SDK fails with HTTP 401 |
| Explicit `type: credentials_uri` | Disabled | Native acquisition, STS fields and product SDK identity call succeed |
| Default chain and `ALIBABA_CLOUD_CREDENTIALS_URI` | Disabled | Native acquisition, STS fields and product SDK identity call succeed |

The native provider does not read this project's local authentication token. Enabled mode expects rejection, not successful acquisition; this is no reason to weaken production authentication. Use a file-authenticated adapter for enabled mode as described in [SDK integration](../../docs/SDK-INTEGRATION.md).

## Run live cluster tests

Prerequisites: a working `akless-demo` Deployment, ConfigMap, ServiceAccount and image-pull access. The default port is 61765; explicit alternatives are supported. The default role must be the Alibaba Cloud role being checked. Install Python 3, kubectl and Docker Buildx and log into the target registry. In the target namespace, the operator needs to read the source Deployment/port ConfigMap, create/get/watch/delete test Pods and execute commands in their business container.

The source Sidecar `command` must be unset, empty, or `['/usr/local/bin/idaas-akless-sidecar']`. Put all startup options in `args`. Shell wrappers, custom launchers and arguments embedded in `command` are rejected before Pod creation. Adjust source deployments to direct execution first. Test Pods explicitly execute the binary so image entrypoints cannot swallow test options.

Before creating Pods, the runner resolves the source Sidecar port from `--listen-port` first, then literal environment values or `configMapKeyRef`, otherwise `61765`. Ports must be in `1–65535`. Optional missing ConfigMaps/keys and empty values use the default. Missing required keys, unsupported `valueFrom` references and invalid ports fail early. Test CLI, environment, self-check and SDK URI all use the resolved port, including an explicitly retained legacy `8080`. For supported source deployments, each authentication scenario normalizes both CLI and environment switches, preserves configuration paths/other `args` and cannot be overridden by the source authentication setting. The main Deployment is unchanged.

Build from the repository root, replacing registry placeholders:

```bash
docker buildx build --platform linux/amd64 --push \
  -t '<registry>/<namespace>/idaas-akless-sdk-test:<tag>' \
  -f verification/native-uri/Dockerfile verification/native-uri
```

Use the resulting digest. The image reference must end in `@sha256:` followed by exactly 64 lowercase hexadecimal characters; malformed references fail before any cluster access:

```bash
python3 verification/native-uri/run.py \
  --context '<ACK-context>' \
  --namespace idaas-akless \
  --deployment akless-demo \
  --image '<registry>/<namespace>/idaas-akless-sdk-test:<tag>@sha256:<digest>' \
  --expected-role-arn 'acs:ram::<account-id>:role/<role-name>'
```

The runner copies the Deployment Pod spec into two independently labelled Pods, leaving the original Deployment/ConfigMap unchanged. It replaces the business image and explicit `env` list while retaining the real Sidecar. The disabled Pod removes the volume/mount named `credential-auth-token` from both containers. The runner disables node-metadata/CLI sources and points the SDK credential file at a nonexistent path.

Use the repository demo or an equivalently isolated source. Other `initContainers`, business `envFrom` and remaining volume mounts are inherited; the runner does not strip every possible identity source. Check that they provide no static SDK credentials, alternate credential files or business access to the IDaaS configuration/bootstrap token. Custom local-auth volume names require adaptation rather than assuming the default-name removal covers them.

Success requires real STS acquisition, cache reuse, and `GetCallerIdentity` matching the expected account/role using the same Credential instance injected into the product SDK. Invalid credentials/identity or any scenario failure exits nonzero. Parsing fields alone is not cloud API success. The runner attempts Pod deletion in `finally`. If interrupted or cleanup fails, inspect Pods labelled `test-purpose=native-credentials-uri` and delete only names belonging to your run. An active deadline stops execution but does not delete the Pod object. Logs print check names, not tokens, AK, SK or SDK error bodies.

## Live automatic refresh long tests

Add `--wait-for-refresh` to retain the same native Credential instance in each of the two disabled-authentication scenarios. Calls run every 5 seconds until the access-key ID or security token changes. The test then compares the current Sidecar access-key ID with the provider result, requires expiry to advance and verifies cloud identity again. The snapshot comparison does not compare every field of the triple. Failure to refresh by initial STS expiry + 120 seconds fails the test. It does not modify clocks, expiry or internal caches.

The two SDK modes run sequentially and often take one to two hours while STS naturally approaches expiry. The test ends after replacement and identity verification, without waiting for the first STS to actually expire. It therefore verifies near-expiry native-provider refresh, not continued business operation across initial expiry. Default mode checks acquisition, valid cache reuse and identity only. It does not prove resource-specific permissions or support for every language SDK.

Each refresh window, initial expiry + 120 seconds, is capped at 7200 seconds; each process gets 7300 seconds. Long-test Pods get 15000 seconds for both runs, startup and headroom. Longer-lived credentials can make total execution approach four hours. Ordinary mode retains a 900-second Pod deadline.

## Local regression

```bash
npm ci --prefix verification/native-uri --ignore-scripts --no-audit --no-fund
node --test --experimental-test-coverage \
  --test-coverage-include='verification/native-uri/integration.cjs' \
  verification/native-uri/integration.test.cjs
python3 -m unittest discover -s verification/native-uri -p run_test.py
```

Local tests substitute dependencies to verify pass/fail decisions, identity checks, refresh timeouts and cleanup. They do not replace live cluster tests.
