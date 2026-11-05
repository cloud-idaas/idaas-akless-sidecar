# IDaaS Akless Sidecar

**English** | [简体中文](README.zh-CN.md)

Browse all guides in the [documentation index](docs/README.md).

All delivered runtime images use digest-pinned Alibaba Cloud Linux 3.13.4. The Node.js example and test images retain Node.js 24.22.2 using a pinned glibc artifact stage. The Go build stage remains separate from the delivered operating system.

For canonical CLI, environment variable, ConfigMap and SDK field names, see the
[configuration naming map](deploy/CONFIGURATION.md#parameter-names-and-cross-layer-mapping).

For console setup, image acquisition, application changes and step-by-step verification, see the [customer integration guide](docs/CUSTOMER-INTEGRATION.md) and its dedicated deployment template. Deployment files contain placeholders: supply your own configuration, roles, audience and images before use.

IDaaS Akless Sidecar is a temporary credential proxy for Kubernetes workloads.
It authenticates to IDaaS with a projected ServiceAccount token and serves cloud-role
credentials to business containers in the same Pod over local HTTP. Applications
need neither an IDaaS SDK nor long-lived cloud access keys.

Alibaba Cloud, AWS and Tencent Cloud are supported. The Sidecar uses the IDaaS
Go Core SDK; applications use their own product SDKs for cloud API calls.

This repository provides the Go Sidecar, image build files, Kubernetes templates,
a runnable Node.js example and SDK response checks. IDaaS manages identity and
cloud-role permissions; the Sidecar fetches, caches and converts credentials;
the application calls cloud APIs. Any language with an HTTP client can use it.

```text
Projected SA token → IDaaS Core SDK → IDaaS Access Token
    → Developer API → temporary cloud credentials
    → http://127.0.0.1:61765/credentials → business container → cloud API
```

## Connect an application

1. Configure an IDaaS M2M application, Kubernetes OIDC federation, cloud roles and application permissions.
2. Deploy the Sidecar in the application's Pod, mounting IDaaS configuration and a projected ServiceAccount token.
3. Fetch credentials over HTTP or configure an SDK credential provider.
4. Verify cloud identity, required permissions and credential refresh.

| Cloud | Integration | Business container configuration |
| --- | --- | --- |
| Alibaba Cloud | File-authenticated credential adapter | Use an authenticated HTTP adapter; a URI-only provider cannot access the endpoint |
| AWS | Container provider with Token File support | Set both the credentials URI and `AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`; verify each SDK version |
| Tencent Cloud | HTTP fetch with a refreshing Credential/Provider | Map `tmpSecretId/tmpSecretKey/token`; refresh before expiry |

Confirm provider support in your SDK version and ensure other credential sources
do not take precedence. With direct HTTP integration, update the credentials used
by the product SDK; a credential fetched once does not renew itself.

See the [SDK integration guide](docs/SDK-INTEGRATION.md) and runnable
[Node.js HTTP example](business-example/README.md).

## First run: deploy and verify the example

Run commands from the repository root. This walkthrough uses an authorized
Alibaba Cloud default role and the included Node.js container. You need
Docker/Buildx, a registry, Kubernetes 1.33+ and configured kubectl. Image builds
do not require Go on the host. Prepare the IDaaS M2M application, OIDC federation
and an Alibaba Cloud role authorized for that application.

### 1. Check the cluster and federated identity

```bash
kubectl config current-context
kubectl get nodes -o custom-columns=NAME:.metadata.name,ARCH:.status.nodeInfo.architecture
```

Confirm the target cluster and node architecture. The example namespace and
ServiceAccount are `idaas-akless` and `idaas-akless-demo`, giving the federated
subject `system:serviceaccount:idaas-akless:idaas-akless-demo`. The projected token
audience must match IDaaS trust configuration; replace the bootstrap audience placeholder.

### 2. Build and push images

For releases and mixed amd64/arm64 clusters, use [GitHub Actions image publishing](docs/PUBLISHING-IMAGES.md), which pushes both architectures in one index and verifies both runtimes. The commands below build one platform for local verification; do not run them twice with different architectures against the same tag, because the second push replaces the first platform.

```bash
export ACR_REGISTRY='<your-registry>'
export ACR_NS='<your-namespace>'
export IMG_TAG='<your-version>'
export IMG_ARCH=linux/amd64
docker login "$ACR_REGISTRY" --username='<your-user>'
./deploy/build-images.sh
```

For an arm64-only deployment, use `IMG_ARCH=linux/arm64`. On success, the script prints:

```text
<registry>/<namespace>/idaas-akless-sidecar:<version>
<registry>/<namespace>/idaas-akless-business-example:<version>
```

### 3. Fill configuration and image references

| Location | Required value |
| --- | --- |
| `deploy/configmap.yaml`, key `idaas-akless-client-config.json` under `data` | Complete console export for your tenant, instance, M2M application, federation and endpoints |
| JSON `oidcTokenFilePath` | Empty; `oidcTokenFilePathEnvVarName` is `OIDC_TOKEN_FILE_PATH`, matching `/run/secrets/oidc-federated/token` |
| ConfigMap `IDAAS_AKLESS_DEFAULT_ROLE_ARN` | Authorized Alibaba Cloud role external ID |
| ConfigMap `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` | Local role allowlist; the example includes the default role |
| ConfigMap `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` | Keep `"true"` for local token authentication; shared by both containers |
| ConfigMap `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` | Template explicitly sets `"61765"`; optional override shared by the listener, probes and business client, with the same default when omitted |
| Both `image` entries in `deploy/deployment.yaml` | Full image references printed by the build script |
| Deployment `oidc-federated-token` projection audience | The exact audience trusted by IDaaS; keep the separate local-auth audience `idaas.akless.sidecar` unchanged |

YAML does not expand host shell variables such as `$ACR_REGISTRY`; write actual
image values. Retain token/config mounts, loopback binding and the exec probe.
See the [configuration contract](deploy/CONFIGURATION.md) for options and precedence.

### 4. Apply manifests

```bash
kubectl apply -f deploy/namespace.yaml
```

For a private registry, configure `acr-secret` in `idaas-akless` using the commands
in the [deployment guide](deploy/README.md#4-apply). If using an ACK credential
helper or a public repository, adjust `imagePullSecrets` accordingly. Then run:

```bash
bash deploy/validate-manifests.sh
kubectl apply -f deploy/serviceaccount.yaml -f deploy/configmap.yaml -f deploy/deployment.yaml
kubectl -n idaas-akless rollout status deployment/akless-demo --timeout=180s
kubectl -n idaas-akless get pods -l app=akless-demo
```

Expect validation and rollout to succeed, with a `2/2 Running` Pod. On failure,
inspect `kubectl -n idaas-akless describe pod <pod-name>` for image, configuration
or probe errors.

### 5. Verify local health and valid credentials

Set `POD_NAME` to the newly deployed Pod to avoid selecting another version during rollout:

```bash
export POD_NAME='<new-pod-name>'
kubectl -n idaas-akless exec "$POD_NAME" -c idaas-akless-sidecar -- \
  /usr/local/bin/idaas-akless-sidecar --healthcheck
kubectl -n idaas-akless logs "$POD_NAME" -c business --tail=10
```

The health check succeeds silently with exit code 0. A successful example logs
`[ok] got STS credentials from sidecar, expires at ...`. The example fetches only
once. Native Sidecar startup waits for the local listener, but upstream IDaaS
failures can still occur; verify again with the command below if needed.

This checks HTTP status, the Alibaba Cloud credential triple and future expiry,
printing only a validation flag and expiration:

```bash
kubectl -n idaas-akless exec -i "$POD_NAME" -c business -- node --input-type=module <<'JS'
try {
  const { fetchJson, configuredCredentialsURI } = await import('/app/credentials-client.js');
  const uri = configuredCredentialsURI();
  const c = await fetchJson(uri, 15000);
  const complete = c.Code === 'Success' &&
    [c.AccessKeyId, c.AccessKeySecret, c.SecurityToken].every(v => typeof v === 'string' && v.length > 0);
  if (!complete || !(Date.parse(c.Expiration) > Date.now())) throw new Error('Invalid credential');
  console.log(JSON.stringify({ credentialValid: true, expiration: c.Expiration }));
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
JS
```

Expect `{"credentialValid":true,"expiration":"<future-time>"}` and exit code 0.
For `400`, check role and query parameters; for `403`, check the local allowlist;
for `502`, inspect Sidecar logs for authentication, connectivity or role-permission errors.
For `401` or `503`, check the local token mount, permissions, Pod binding, expiry
and rotation; never bypass a failure by retrying anonymously.

### 6. Verify cloud identity and permissions

Use the [deployment verification command](deploy/README.md#5-verify) to call
`aliyun sts GetCallerIdentity` with the returned STS. Check the assumed-role
account and role, then invoke a cloud API required by the application and verify
its result. Local health, valid credentials and cloud permissions are separate checks.

## Connect your own application

Replace the Deployment's `business` container with your application, retaining
the Sidecar, configuration volume, both separate token volumes and read-only mounts. Replace the
example's port 3000, Node user settings and `/healthz` liveness probe with your
application's ports, security settings and health checks.

- Direct HTTP: configure `IDAAS_AKLESS_CREDENTIALS_URI`, fetch explicitly and update
  product SDK credentials using the response fields.
- SDK provider: initialize a refreshing credential chain and set its URI variable.
  The included Node.js example uses direct HTTP and does not read SDK provider variables.

For example, an AWS business container uses:

```yaml
env:
  - name: AWS_CONTAINER_CREDENTIALS_FULL_URI
    value: http://127.0.0.1:61765/credentials
  - name: AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE
    value: /run/secrets/idaas-akless/credential-auth/token
```

Set the Sidecar default to an authorized AWS role as well. Alibaba Cloud uses
a file-authenticated adapter, rather than a URI-only provider; setting only
`ALIBABA_CLOUD_CREDENTIALS_URI` does not satisfy local file authentication.
Tencent Cloud uses authenticated HTTP mapping with refresh.
See [SDK integration](docs/SDK-INTEGRATION.md) for examples and validation.

Local authentication is enabled by default. Every credential request, including raw output and cache hits, requires `Authorization: <local-auth-JWT>` without a Bearer prefix. The separate kubelet-projected file defaults to `/run/secrets/idaas-akless/credential-auth/token`. Missing/wrong authentication returns 401, malformed headers 400, and invalid reference files 503. Authentication failures never trigger anonymous fallback.

For compatibility or controlled testing, explicitly set `--credential-auth-enabled=false`
or `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=false` (only `true/false` are accepted).
The Node.js example uses the same environment variable. Both containers read the
ConfigMap key `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED`, set to `"true"` in the template;
recreate the Pod after changing it. Disabled mode skips local token reads but retains
IDaaS bootstrap authentication, role authorization and loopback binding. Startup logs
a WARN because local token-based SSRF credential protection is removed. Keep authentication
enabled in production; see the [configuration contract](deploy/CONFIGURATION.md#explicitly-disabling-local-authentication).

## HTTP API

| Endpoint | Purpose |
| --- | --- |
| `GET /credentials` | Default-role credentials |
| `GET /credentials?roleArn=<URL-encoded-external-ID>` | Specified-role credentials |
| `GET /credentials?format=raw` | Developer API fields declared by the Sidecar |
| `GET /healthz` | Return `ok` when local HTTP responds |

An omitted or empty `roleArn` uses the default role. Role identifiers are opaque;
the upstream response selects the vendor and IDaaS authorizes the role. See the
[response contract](docs/CREDENTIALS-RESPONSE.md) for fields, raw output and errors.

## Cache and timeouts

- Per-role cache and shared refresh, triggered by requests. The refresh point is
  4/5 of the remaining lifetime at fetch completion, or earlier to reserve the
  configured usable-cache margin plus one refresh budget when the lifetime allows.
- Once refresh is due or running, cached credentials with more than the minimum
  remaining validity return immediately while one background refresh runs per role.
  Cold, expired and near-expiry requests wait for the shared result. The margin
  defaults to 5 minutes; configure `--credential-refresh-min-validity` or
  `IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY` for your SDK's refresh window.
- Failed refreshes may return unexpired cached credentials. Expired credentials
  are never returned. Backoff lasts 10 s from failure completion. The margin only
  controls waiting during refresh; it does not impose a minimum lifetime on fresh
  upstream results or existing failure fallback. See the [configuration contract](deploy/CONFIGURATION.md#sidecar-startup-options).
- Up to 64 roles are retained; idle entries without valid credentials can be
  reclaimed at capacity when they are outside backoff.
- The default upstream refresh budget is 10 s. Client cancellation stops that
  request's wait while the shared refresh continues within its own budget;
  set a total deadline in the application.
- HTTP shutdown defaults to the refresh budget plus 5 s (15 s by default).
  `--shutdown-timeout` / `IDAAS_AKLESS_SHUTDOWN_TIMEOUT` accepts `auto` or a longer
  duration. Coordinate the Pod termination grace period with business shutdown
  and Sidecar draining; exceeding the budget closes remaining connections.
- HTTP headers and request reads have a 5 s timeout, idle keep-alive connections expire after 30 s,
  and response writes have the refresh budget plus 5 s. Shared refresh failures are logged
  once per attempt; waiting requests and backoff responses do not repeat the failure log.
  Panics additionally record sanitized code locations; other request errors, such as cache admission failure, are logged separately.

## Security and verification

The listener IP is fixed to `127.0.0.1`; its port defaults to `61765` and can be
changed with `--listen-port` or `IDAAS_AKLESS_SIDECAR_LISTEN_PORT`. Keep business URIs and health
checks in sync when changing the port. The templates reference one optional ConfigMap port key; both the Sidecar and
business client use `61765` if it is absent or empty. An explicit business URI
takes precedence over the client port fallback. Existing deployments using `8080` must
explicitly retain it or migrate all endpoints together; see the
[upgrade notes](deploy/CONFIGURATION.md#default-port-and-upgrades). Pod containers share an identity
and must be mutually trusted. Use a dedicated ServiceAccount, least-privilege cloud
roles and, optionally, a local allowlist. No Kubernetes API permissions are needed.
The business container mounts only the separate local authentication token, never IDaaS configuration or the bootstrap token.

When changing container UIDs, retain root ownership of the projected authentication file.
Setting the same explicit `runAsUser` for every container, including the native Sidecar,
changes token ownership and is unsupported. See the [UID constraint](deploy/CONFIGURATION.md#kubernetes-configuration-mapping).

Keep temporary access keys and tokens out of logs and configuration files. Health
and Pod readiness checks establish basic availability only; credential requests
can use the cache. Verify cloud identity and an API operation required by the application.

## Development

Local compilation requires Go 1.24+; the Node.js example requires Node.js 24+.
`make build` produces `bin/idaas-akless-sidecar`. With authentication enabled, runtime
requires a trusted projected local token and Pod context from the Downward API.
Explicitly disabling local authentication does not disable the IDaaS bootstrap identity requirement.

`bin/idaas-akless-sidecar --version` prints version/revision without runtime configuration.
Use `make build VERSION=<release> REVISION=<commit>` for release builds; local builds
default to `dev` and a Git description (including tracked changes marked `-dirty`).
Docker builds accept `VERSION` and `REVISION` build arguments, defaulting to `dev`/`unknown`.
The image build script supplies `IMG_TAG` and the Git description or explicit `REVISION`.

```bash
go test -race ./...
go vet ./...
make lint
python3 -m unittest discover -s deploy -p 'test_*.py'
node --test deploy/rotation-integration-test.test.js
npm test --prefix business-example
```

`make lint` downloads and uses golangci-lint v2.6.2 with Go 1.24.6 to check the
minimum supported Go release line, independently
of the locally installed linter and Go version. The image-script test uses a
Docker command stub and does not build or push images.

`cmd/` handles startup; `internal/credentials/` handles fetching, caching and conversion;
`internal/health/` handles health checks; `internal/projectedauth/` enforces the trusted local
token projection, Pod binding and rotation grace. See the [implementation map](docs/README.md#implementation-references)
and [SDK verification](verification/sdk-contracts/README.md)
for offline checks of current responses.
The separate [native Credentials URI tests](verification/native-uri/README.md)
cover real Alibaba Cloud Node.js SDK access, including optional refresh checks.
See [testing](docs/TESTING.md) for all retained suites and their validation limits.

`make check` runs the complete offline regression commands; `make check-linux`
also verifies real Linux read-only mount permissions in disposable local containers.

See the [business integration tests](business-example/multicloud/README.md) for
three-cloud STS, Credentials URI integration and real resource queries.

See the [release checklist](docs/RELEASING.md) for candidate scans, Git history
sanitization and public source/image verification.

License: Apache-2.0. See [LICENSE](LICENSE).
The runtime image bundles the project license and [binary dependency licenses](licenses/README.md)
under `/usr/share/licenses/idaas-akless-sidecar/`.
