# Kubernetes deployment

**English** | [简体中文](README.zh-CN.md)

Run commands from the repository root. The manifests deploy a Sidecar and a Node.js
business example in one Pod; the example obtains Alibaba Cloud credentials over loopback.

All tenant, role, bootstrap audience and image values are placeholders. Replace
them before validation or deployment; real configuration should stay outside Git.
Reusable integration tests are listed in [testing](../docs/TESTING.md).

The default ConfigMap contains the client JSON, default role, role allowlist,
local authentication switch (`"true"`) and shared port (`"61765"`). The port key is optional; both
containers use `61765` when it is absent or empty. The Node.js client resolves an
explicit URI first, then the shared port, then the default URI. Exec probes
inherit the Sidecar environment. The listener
IP is fixed to `127.0.0.1`; the upstream refresh timeout defaults to `10s`.
Before upgrading a deployment that uses port `8080`, explicitly retain that port
or migrate the listener, credential URI and probes together. The port key may be omitted. Use matching business code and templates, and
recreate Pods after ConfigMap changes.
See the [configuration contract](CONFIGURATION.md) for adjustments.

## 1. Prerequisites

- Kubernetes 1.33 or later with native Sidecar containers and projected ServiceAccount tokens; kubectl targets the intended cluster.
- IDaaS M2M application, OIDC federation, cloud roles and application permissions.
- IDaaS can verify token issuer, audience, subject and signature; discovery/JWKS must be reachable from IDaaS when used.
- Docker/Buildx and registry repositories with push/pull permissions.

```bash
kubectl config current-context
kubectl get nodes -o custom-columns=NAME:.metadata.name,ARCH:.status.nodeInfo.architecture
```

## 2. Build images

```bash
export ACR_REGISTRY='<your-registry>'
export ACR_NS='<your-namespace>'
export IMG_TAG='<your-version>'
export IMG_ARCH=linux/amd64
docker login "$ACR_REGISTRY" --username='<your-user>'
./deploy/build-images.sh
```

The script builds, checks architecture and pushes `idaas-akless-sidecar` and
`idaas-akless-business-example`. Use `IMG_ARCH=linux/arm64` for arm64 nodes. Each
run builds one architecture; the Buildx driver must export outputs into Docker's image store.

`--no-push` builds and checks local `:build-linux-amd64` tags (`:build-linux-arm64` for
arm64) without pushing. Dockerfiles own the pinned defaults: Alibaba Cloud Linux 3.13.4 runtime layers, Node.js 24.22.2 from a glibc artifact stage, and a separate Go compiler stage. A running Colima VM can supply Buildx when it is unavailable locally.

For approved image mirrors, set complete `BUILD_BASE_IMAGE` (Go compiler), `RUNTIME_BASE_IMAGE` (ALinux runtime for both images) and `NODE_BASE_IMAGE` (Node glibc artifact) references, retaining the version and multi-platform index digest. Unset values preserve Dockerfile defaults. A nonempty legacy `BASE_MIRROR` prefix fails early to prevent accidentally restoring an Alpine runtime.

`--help` (or `-h`) prints usage without building or pushing. Unknown, repeated or extra
arguments fail before any Docker calls. With no arguments, the script builds and pushes.

## 3. Configure manifests

| File | Configuration |
| --- | --- |
| [configmap.yaml](configmap.yaml) | Replace the `idaas-akless-client-config.json` value with a console export; set the default role and allowlist, and keep authentication enabled |
| [deployment.yaml](deployment.yaml) | Replace both images, token audience and pull credentials; use the configured ServiceAccount |
| [serviceaccount.yaml](serviceaccount.yaml) | Dedicated ServiceAccount matching the IDaaS federated subject |
| [namespace.yaml](namespace.yaml) | Namespace shared by the manifests |

Leave SDK JSON `oidcTokenFilePath` empty and set `oidcTokenFilePathEnvVarName` to
`OIDC_TOKEN_FILE_PATH`. The Deployment supplies that variable with the projected
path `/run/secrets/oidc-federated/token`. Replace a literal `${oidcTokenFilePath}`
placeholder with an empty string. JSON does not support comments.

The Developer API must be reachable inside the Pod. Authorize the default role
for the IDaaS application. Set `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` to include the default
role and any other required roles. See the
[advanced configuration](CONFIGURATION.md#advanced-configuration-port-timeouts-and-multiple-roles).

Before applying, check the subject is
`system:serviceaccount:idaas-akless:idaas-akless-demo` (or your chosen namespace/SA),
the token audience matches IDaaS trust, and every role/image placeholder is replaced.
YAML does not expand host shell variables in image references. The default role
must be an authorized Alibaba Cloud role for the included business example.

Keep the separate `credential-auth-token` projected volume with the fixed `idaas.akless.sidecar` audience, 0440 mode, fsGroup and Downward API context. Mount only this token in the business container. Verify that it cannot authenticate to IDaaS or the Kubernetes API and test at least two real kubelet rotations. Restrict workload access to node metadata and other credential proxies to prevent alternate SSRF paths. Arbitrary file reads, RCE and node compromise are outside this protection.

## 4. Apply

```bash
kubectl apply -f deploy/namespace.yaml
```

Configure `acr-secret` for a private registry, or use the ACK credential helper
and remove `imagePullSecrets`. If Docker configuration contains the intended
registry login, import it:

```bash
kubectl -n idaas-akless create secret generic acr-secret \
  --type=kubernetes.io/dockerconfigjson \
  --from-file=.dockerconfigjson="$HOME/.docker/config.json" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Import only the required registry credentials. Local `credsStore`/`credHelpers`
references cannot supply credentials on nodes; use dedicated credentials or the ACK helper.

```bash
bash deploy/validate-manifests.sh
kubectl apply -f deploy/serviceaccount.yaml -f deploy/configmap.yaml -f deploy/deployment.yaml
kubectl -n idaas-akless rollout status deployment/akless-demo --timeout=180s
```

Validation is server-side dry run and requires the namespace to exist. The Sidecar
binds loopback, so readiness uses an in-container `exec --healthcheck`. Its 4 s
probe deadline exceeds the binary's 2 s self-check timeout.

Expect dry-run validation and rollout to complete without errors. If they fail,
inspect `kubectl -n idaas-akless describe pod <pod-name>` for pull, configuration
or probe errors.

## 5. Verify

```bash
kubectl -n idaas-akless get pods
kubectl -n idaas-akless logs deployment/akless-demo -c idaas-akless-sidecar --tail=20
kubectl -n idaas-akless logs deployment/akless-demo -c business --tail=10
```

Expect `2/2 Running`. The example logs expiration after a successful credential
fetch. Native Sidecar startup waits for the local listener, not successful upstream
authentication. The example makes one initial fetch, with at most one token reread
and retry on HTTP 401; it does not retry in the background after final failure.
Use the verification commands below if needed. During rollout, use the new Pod name
instead of `deployment/akless-demo` in verification commands.

Run the [README health and credential checks](../README.md#5-verify-local-health-and-valid-credentials):
expect a silent health-check exit code 0, followed by `credentialValid: true` and
future expiration from the business container. These checks do not print keys.

With local Python 3 and the Alibaba Cloud CLI installed, verify default-role identity:

```bash
(
  set -euo pipefail
  set +x
  kubectl -n idaas-akless exec deployment/akless-demo -c business -- \
    node -e 'const client=require("/app/credentials-client");client.fetchJson(client.configuredCredentialsURI(),15000).then(c=>process.stdout.write(JSON.stringify(c))).catch(()=>{console.error("credential request failed");process.exitCode=1})' |
    python3 -c '
import json, sys
c = json.load(sys.stdin)
p = {"name": "sts-verification", "mode": "StsToken", "region_id": "cn-hangzhou",
     "access_key_id": c["AccessKeyId"], "access_key_secret": c["AccessKeySecret"],
     "sts_token": c["SecurityToken"]}
print(json.dumps({"current": p["name"], "profiles": [p]}))
' |
    aliyun sts GetCallerIdentity --config-path /dev/stdin \
      --profile sts-verification --region cn-hangzhou --secure
)
```

Check the assumed-role identity, then invoke a cloud API required by the business.
Credentials pass through stdin without a file; keep tracing off and do not log
the credential body. Health and Pod readiness do not verify cloud permissions.

Expect an `Arn` resembling `acs:ram::<account-id>:assumed-role/<role>/<session>`
whose account and role match your authorization. Then verify a required business
operation, such as reading an expected object, rather than accepting Pod readiness alone.

## Update and troubleshoot

Update images and matching configuration, then validate and apply. A ConfigMap-only
change requires `kubectl -n idaas-akless rollout restart deployment/akless-demo`.
Pinned digests do not change when tags are overwritten. Deployment rollback does
not restore ConfigMap or Secret data.

| Symptom | Check |
| --- | --- |
| Pull failure or `exec format error` | Registry permissions, pull Secret and target architecture |
| `400` / `403` | Role, query encoding and local allowlist |
| `401` / `503` | Local token mount, read permissions, audience, Pod context, expiry and rotation; never retry anonymously |
| `502` | Sidecar logs, Pod connectivity, IDaaS permissions, expiry and cache capacity |
| `invalid_client_assertion` | Token `iss/aud/sub/kid` against IDaaS trust |
| Configuration unchanged | Pod recreation and CLI overrides |

Use `kubectl delete namespace idaas-akless` only when all namespace resources are disposable demo resources.
