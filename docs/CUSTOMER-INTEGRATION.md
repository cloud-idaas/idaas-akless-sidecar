# Obtain temporary cloud credentials for Kubernetes applications with IDaaS Akless Sidecar

**English** | [简体中文](CUSTOMER-INTEGRATION.zh-CN.md)

IDaaS Akless Sidecar runs in the same Pod as the business container. It authenticates to IDaaS using a Kubernetes ServiceAccount OIDC token and obtains temporary credentials for authorized cloud roles. The application fetches them over local HTTP and calls cloud resources through its product SDK. It stores no long-lived AccessKey or IDaaS Client Secret and does not integrate an IDaaS SDK.

This guide first verifies an Alibaba Cloud role with an example that requires no business SDK, then explains integration with an existing application. The Sidecar currently supports Alibaba Cloud, AWS and Tencent Cloud temporary credentials. Authentication and refresh support must be verified per language/SDK version; configuring a credentials URI alone is not acceptance.

Companion file: [customer deployment template](examples/customer-deployment.yaml). Use the guide, template and image from the same release.

Source repository: [cloud-idaas/idaas-akless-sidecar](https://github.com/cloud-idaas/idaas-akless-sidecar). Version tags and public image references await release confirmation; `<...>` marks information to fill in. Building from source can be used for verification; `deploy/` contains placeholder templates, not ready-to-use tenant configuration or released images. See [release checks](RELEASING.md).

## Integration flow and application changes

```text
Kubernetes projected bootstrap token → Sidecar → IDaaS M2M authentication → PAM cloud-role STS
Business container + local authentication token → Sidecar /credentials → temporary AK + SK + security token
Business container + temporary credentials → cloud product API
```

| Owner | Required work | Completion criteria |
| --- | --- | --- |
| IDaaS / cloud account administrator | Manage cloud accounts/roles; create M2M client, OIDC trust provider and federated credential; grant PAM functional and role-data permissions | Account/role available; correct application-to-role authorization |
| Kubernetes administrator | Obtain cluster Issuer/JWKS; configure dedicated ServiceAccount, two projections, Sidecar and read-only mounts | New Pod starts; application can authenticate and acquire credentials |
| Application developer | Use a sustainable SDK provider or implement authenticated HTTP, field mapping and refresh | Actual business API calls use the target role and continue beyond credential expiry |

Sidecar configuration does not replace cloud-role permission policies or automatically change SDK credential sources. Run commands in a local Bash terminal; explicitly marked code runs inside containers through `kubectl exec`. Replace every `<...>` placeholder.

## 1. Prepare the environment

Prerequisites:

- An IDaaS EIAM instance with M2M and PAM capabilities enabled, plus the corresponding administrative permissions.
- A Linux Kubernetes cluster. This template requires Kubernetes 1.33+ native Sidecars and supporting nodes. Do not apply this procedure unchanged to older clusters. See [Kubernetes Sidecar containers](https://kubernetes.io/docs/concepts/workloads/pods/sidecar-containers/).
- Local `kubectl`, Git and Python 3. Building images also needs Docker Engine/Buildx. Pulling public images does not require Go.
- Pod DNS and HTTPS access to the instance's IDaaS token endpoint/Developer API; business access to target cloud endpoints; node access to image registries.
- A dedicated verification namespace and ServiceAccount. This guide uses `akless-example` / `akless-business`. Do not run these commands directly against existing business resources with those names.

Confirm cluster, version and node architecture:

```bash
kubectl config current-context
kubectl version -o yaml
kubectl get nodes -o custom-columns=NAME:.metadata.name,VERSION:.status.nodeInfo.kubeletVersion,ARCH:.status.nodeInfo.architecture,OS:.status.nodeInfo.operatingSystem
```

**Expected:** the selected context is the target cluster; control plane/nodes satisfy requirements and run Linux. Record `amd64` or `arm64`. Mixed-architecture clusters need images covering every scheduling target or an architecture `nodeSelector`.

### Get versioned source and documentation

Obtain the GitHub HTTPS repository address, source tag, public Sidecar image and digest from the release page. Variables below hold those release values. Public ACR and Docker Hub are alternative image sources; neither address can be inferred from the other.

```bash
export AKLESS_GIT_URL='https://github.com/cloud-idaas/idaas-akless-sidecar.git'
export AKLESS_VERSION='<source-version-tag>'
git clone --branch "$AKLESS_VERSION" --depth 1 "$AKLESS_GIT_URL" idaas-akless-sidecar
cd idaas-akless-sidecar
export AKLESS_WORK="$(mktemp -d "${TMPDIR:-/tmp}/idaas-akless.XXXXXX")"
export AKLESS_NAMESPACE=akless-example
```

**Expected:** the checkout contains `Dockerfile`, `cloud_idaas.json.example`, `business-example/credentials-client.js` and `docs/examples/customer-deployment.yaml`. The external work directory uses a system temporary location restricted to the current user. Run subsequent commands from this repository root. Restore variables after closing the terminal; temporary directories may be cleaned, so recreate configuration when starting over.

Keep the example namespace/ServiceAccount for the initial check. Changing them requires matching changes in the IDaaS federated credential's namespace/account and Deployment `serviceAccountName`. Changing `AKLESS_NAMESPACE` alone does not change trust conditions.

## 2. Configure IDaaS and the target cloud role

### 2.1 Manage the cloud account and business role

Open the target EIAM instance in the [IDaaS management console](https://yundun.console.aliyun.com/?p=idaas).

1. Under **Asset Management > Cloud Identity**, choose **Add Cloud Account** and the appropriate current/other-account onboarding method.
2. Follow the console instructions for the RAM identity provider, system role and permission policies, then run its checks.
3. Under that account's **Cloud Role Management**, add the required business RAM role, configure trust and grant only required operations on target resources.
4. Record the role's **external ID**. For Alibaba Cloud this is commonly `acs:ram::<account-id>:role/<role-name>`, later supplied as `IDAAS_AKLESS_DEFAULT_ROLE_ARN`. Do not use IDaaS's internal role ID.

**Expected:** the cloud account/system role is available and the business role is enabled and usable with the intended permissions. Use the target business role, not the account-management system role. See [cloud identity management](https://help.aliyun.com/zh/idaas/eiam/user-guide/cloud-identity-management).

### 2.2 Obtain the Kubernetes Issuer and verification keys

Run from a terminal authorized to read the cluster's relevant endpoints:

```bash
kubectl get --raw /.well-known/openid-configuration > "$AKLESS_WORK/k8s-discovery.json"
kubectl get --raw /openid/v1/jwks > "$AKLESS_WORK/k8s-jwks.json"
python3 - "$AKLESS_WORK" <<'PY'
import json, pathlib, sys
p = pathlib.Path(sys.argv[1])
d = json.loads((p / "k8s-discovery.json").read_text())
j = json.loads((p / "k8s-jwks.json").read_text())
assert d["issuer"].startswith("https://"), "IDaaS OIDC trust requires an HTTPS Issuer"
assert j.get("keys"), "JWKS must not be empty"
print("Kubernetes Issuer:", d["issuer"])
print("JWKS key count:", len(j["keys"]))
PY
```

**Expected:** both kubectl commands and Python checks succeed. Record the Kubernetes Issuer. If access returns 403, ask the cluster administrator to export the same data; do not disable authentication or expose the API server.

### 2.3 Create an OIDC federated trust provider

In the instance console, open **Login > Authentication Configuration > Federated Trust Providers** and add an OIDC provider. This guide uses static JWKS:

| Field | Value |
| --- | --- |
| Provider name | For example, `akless-k8s` |
| Trust source | Static configuration |
| Issuer | Exact `issuer` from cluster Discovery above |
| Audience | `urn:example:idaas:bootstrap`, exactly matching the template bootstrap audience |
| Verification public keys | Complete local `k8s-jwks.json` contents |

**Expected:** provider saved/enabled. This Issuer is **Kubernetes's issuer**; later SDK JSON `issuer` identifies the **IDaaS authorization server**. They have different purposes. If changing bootstrap audience, update both this provider and Deployment `oidc-federated-token`.

Static JWKS does not require IDaaS access to the API server, but cluster signing-key rotation requires synchronized public-key updates and verification before switching. Automatic discovery/JWKS URI can also be used where appropriate; see [federated provider management](https://help.aliyun.com/zh/idaas/eiam/user-guide/federated-credential-provider-management).

### 2.4 Create the M2M client and application federated credential

1. In **Application Management > M2M Applications**, add the business client. If application types are offered, choose one supporting OIDC/M2M.
2. Record `client_id`, IDaaS authorization-server Issuer, token endpoint and instance ID. Use actual application configuration/Discovery values.
3. Under **General Configuration > Authentication Management > OIDC**, add a federated credential.
4. Select the provider above, name it `akless-k8s-credential`, choose **kubernetes mode**, namespace `akless-example` and ServiceAccount `akless-business`.

**Expected:** credential exists for subject `system:serviceaccount:akless-example:akless-business`. Its name matches later `applicationFederatedCredentialName`. This solution needs no Client Secret. See [create an application federated credential](https://help.aliyun.com/zh/idaas/eiam/user-guide/create-application-federated-credential).

Deployment `oidc-federated-token` works with this credential: Kubernetes issues/rotates the token, the Sidecar reads `/run/secrets/oidc-federated/token` and submits it to IDaaS. IDaaS checks the provider's issuer, keys, audience and the credential's ServiceAccount conditions. Volume names/paths are container-local conventions, not console values; download no token from the console.

### 2.5 Grant PAM functional and target-role data permissions

In **Permission Center > Functional Permissions**, find `Privileged Access Management`. Under **Authorization Management > Application Authorization**, grant cloud-role credential acquisition (`cloud_account_role:obtain_access_credential`) to the M2M client. Request only this corresponding SDK scope:

```text
urn:cloud:idaas:pam|cloud_account_role:obtain_access_credential
```

Then create an authorization rule in **Permission Center > Data Permissions**. Under its **Permission Management**:

1. Add the M2M client in **Associated Identities**, with authorization valid for the application's runtime.
2. Add the target business cloud role from section 2.1 in **Assets**.

**Expected:** the same client has both functional permission and target-role data permission. Either one alone is insufficient. Changing the local allowlist does not grant IDaaS permissions. See [data permissions](https://www.alibabacloud.com/help/zh/idaas/eiam/user-guide/data-permission-management) and [native Kubernetes integration](https://help.aliyun.com/zh/idaas/eiam/use-cases/credential-free-k8s-sts-token).

## 3. Obtain the Sidecar and example images

Choose one of these methods. The image must match the source/template version.

### Option A: use a public image

Copy the complete release-page reference, preferably pinned by digest:

```bash
export SIDECAR_IMAGE='<public-ACR-or-Docker-Hub-image>@sha256:<published-digest>'
export BUSINESS_IMAGE='<same-release-ALinux-business-example-image>@sha256:<published-digest>'
```

With Docker installed, inspect architecture and try pulling in advance:

```bash
docker buildx imagetools inspect "$SIDECAR_IMAGE"
docker buildx imagetools inspect "$BUSINESS_IMAGE"
docker pull --platform linux/amd64 "$SIDECAR_IMAGE"
docker pull --platform linux/amd64 "$BUSINESS_IMAGE"
```

Use `--platform linux/arm64` for arm64. Without Docker, check Pod events and `imageID` in section 5. Public images need no example `imagePullSecrets`, but nodes must be able to reach the registry.

### Option B: build from source

From the repository root checked out at the selected tag, use a registry you can push to and nodes can pull from:

```bash
export SIDECAR_IMAGE="<your-registry>/<namespace>/idaas-akless-sidecar:$AKLESS_VERSION"
export BUSINESS_IMAGE="<your-registry>/<namespace>/idaas-akless-business-example:$AKLESS_VERSION"
docker login '<your-registry>'
docker buildx build --platform linux/amd64 \
  --build-arg "VERSION=$AKLESS_VERSION" --build-arg "REVISION=$(git rev-parse HEAD)" \
  -f Dockerfile -t "$SIDECAR_IMAGE" --push .
docker buildx build --platform linux/amd64 \
  -f business-example/Dockerfile -t "$BUSINESS_IMAGE" --push business-example
docker buildx imagetools inspect "$SIDECAR_IMAGE"
docker buildx imagetools inspect "$BUSINESS_IMAGE"
```

For arm64 use `linux/arm64`. For multiple architectures use `--platform linux/amd64,linux/arm64` on a supporting builder. Use `AKLESS_VERSION` as both images' version tag. The Sidecar build arguments embed that source version and the checked-out commit; omitting them produces `dev`/`unknown`. The Dockerfile downloads Go modules and compiles during the build; the runtime image needs no Go installation.

**Expected:** build/push succeeds and both remote manifests cover target node architectures. Record both digests and change `SIDECAR_IMAGE` and `BUSINESS_IMAGE` to `<registry-reference>@sha256:<digest>` before continuing. A successful local build does not prove nodes can pull the images.

For private registries, create the namespace in section 4, then configure pull credentials under 4.4. Never commit long-lived pull passwords. The example defaults to public images and references no private test-environment Secret.

## 4. Prepare Sidecar configuration and the example

### 4.1 Create the SDK configuration file

```bash
cp cloud_idaas.json.example "$AKLESS_WORK/client-config.json"
```

Edit the copied file. This is the complete structure for this setup; replace every `<...>`, retain token-path conventions and use the minimum scope shown:

```json
{
  "idaasInstanceId": "<IDaaS-instance-ID>",
  "clientId": "<M2M-client-id>",
  "issuer": "<IDaaS-authorization-server-Issuer-HTTPS-URL>",
  "tokenEndpoint": "<IDaaS-token-endpoint-HTTPS-URL>",
  "scope": "urn:cloud:idaas:pam|cloud_account_role:obtain_access_credential",
  "developerApiEndpoint": "<regional-Developer-API-domain-without-path>",
  "authnConfiguration": {
    "identityType": "CLIENT",
    "authnMethod": "OIDC",
    "applicationFederatedCredentialName": "akless-k8s-credential",
    "clientDeployEnvironment": "KUBERNETES",
    "oidcTokenFilePath": "",
    "oidcTokenFilePathEnvVarName": "OIDC_TOKEN_FILE_PATH"
  },
  "httpConfiguration": {
    "connectTimeout": 5000,
    "readTimeout": 10000
  }
}
```

Use the actual regional Developer API domain, for example Hangzhou `eiam-developerapi.cn-hangzhou.aliyuncs.com`. The SDK normalizes it to HTTPS. `issuer` and `tokenEndpoint` come from the IDaaS application, not Kubernetes. Do not retain literal `${...}` placeholders.

Nonempty `oidcTokenFilePath` wins over the environment. Leave it empty here and supply bootstrap identity through `OIDC_TOKEN_FILE_PATH=/run/secrets/oidc-federated/token`. `connectTimeout` / `readTimeout` are milliseconds. The shared Sidecar refresh budget for token acquisition and Developer API access defaults to `10s`; no override is needed initially.

```bash
python3 - "$AKLESS_WORK/client-config.json" <<'PY'
import json, pathlib, sys
s = pathlib.Path(sys.argv[1]).read_text()
c = json.loads(s)
assert "<" not in s and "${" not in s, "Replace every placeholder"
assert c["issuer"].startswith("https://") and c["tokenEndpoint"].startswith("https://")
a = c["authnConfiguration"]
assert (a["identityType"], a["authnMethod"], a["clientDeployEnvironment"]) == ("CLIENT", "OIDC", "KUBERNETES")
assert a.get("oidcTokenFilePath", "") == "" and a["oidcTokenFilePathEnvVarName"] == "OIDC_TOKEN_FILE_PATH"
assert c["scope"] == "urn:cloud:idaas:pam|cloud_account_role:obtain_access_credential"
print("Configuration format checked; verify identity and authorization through real credential acquisition")
PY
```

### 4.2 Create dedicated resources and the ConfigMap

Set the role external ID and `IDAAS_AKLESS_ALLOWED_ROLE_ARNS` allowlist; this example allows the same default role. Keep local authentication string `"true"`. Port `61765` is explicit but optional; both ends default to it when absent. See [advanced configuration](../deploy/CONFIGURATION.md#advanced-configuration-port-timeouts-and-multiple-roles) for multiple roles.

```bash
export AKLESS_ROLE='<Alibaba-Cloud-role-external-ID-from-section-2.1>'
python3 - <<'PY'
import os
r = os.environ["AKLESS_ROLE"]
assert r.startswith("acs:ram::") and ":role/" in r and "<" not in r, "Set the actual Alibaba Cloud role external ID"
print("Role external ID format checked")
PY
kubectl create namespace "$AKLESS_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$AKLESS_NAMESPACE" create serviceaccount akless-business \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$AKLESS_NAMESPACE" create configmap akless-settings \
  --from-file=idaas-akless-client-config.json="$AKLESS_WORK/client-config.json" \
  --from-literal=IDAAS_AKLESS_DEFAULT_ROLE_ARN="$AKLESS_ROLE" \
  --from-literal=IDAAS_AKLESS_ALLOWED_ROLE_ARNS="$AKLESS_ROLE" \
  --from-literal=IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=true \
  --from-literal=IDAAS_AKLESS_SIDECAR_LISTEN_PORT=61765 \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$AKLESS_NAMESPACE" create configmap akless-demo-code \
  --from-file=business-example/app.js \
  --from-file=business-example/runtime.js \
  --from-file=business-example/credentials-client.js \
  --dry-run=client -o yaml | kubectl apply -f -
```

**Expected:** commands succeed and all resources share one namespace. This ConfigMap contains only OIDC configuration and role identifiers, no Client Secret, long-lived AK or tokens. Demo Node.js code is mounted through a ConfigMap to allow verification without a business-image build. Package production code into your own image.

### 4.3 Generate the customer deployment manifest

```bash
kubectl set image --local -f docs/examples/customer-deployment.yaml \
  idaas-akless-sidecar="$SIDECAR_IMAGE" business="$BUSINESS_IMAGE" \
  -o yaml > "$AKLESS_WORK/deployment.yaml"
```

Open generated `deployment.yaml` and verify these conventions. This template is separate from the repository's test deployment and contains no test tenant/private image references.

| Item | Example and purpose |
| --- | --- |
| ServiceAccount | `akless-business`; matches federation; no Kubernetes API Role/ClusterRole binding is required for the Sidecar |
| Bootstrap token | Separate `oidc-federated-token` volume, audience `urn:example:idaas:bootstrap`; only Sidecar mounts `/run/secrets/oidc-federated` |
| Local authentication token | Separate `credential-auth-token` volume, fixed audience `idaas.akless.sidecar`; both containers mount `/run/secrets/idaas-akless/credential-auth` read-only |
| Token lifetime | Both default to `expirationSeconds: 3600`; local auth supports only 600–3600 seconds and caps current/old tokens at one hour after issue; configure bootstrap lifetime independently for IDaaS. Kubelet projects/rotates files; never copy them manually or use `subPath` |
| Local authentication | Both ends share ConfigMap `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED`, string `"true"`; requests must carry the local token |
| Role restrictions | Default `IDAAS_AKLESS_DEFAULT_ROLE_ARN` and allowlist `IDAAS_AKLESS_ALLOWED_ROLE_ARNS`; example allows only the default |
| File permissions | `defaultMode: 0440`, Pod `fsGroup: 20001` and non-root containers; reconcile existing fsGroup settings and verify reads |
| Pod context | Downward API supplies Sidecar `POD_UID`, `POD_NAMESPACE` and `POD_SERVICE_ACCOUNT`; no fixed values |
| Listener/probes | `127.0.0.1:61765` and exec self-check; no credential Service, Ingress or host port |
| Startup ordering | Sidecar in `initContainers` with `restartPolicy: Always`; business starts after its startup probe |

Enabled authentication requires a root-owned, read-only projected directory and valid Pod-bound token. A normal writable directory or manually created token is unsuitable. Templates avoid `hostNetwork` and include mutually trusted containers only. IP is fixed to `127.0.0.1`. For conflicts, change shared ConfigMap `IDAAS_AKLESS_SIDECAR_LISTEN_PORT` and recreate the Pod. Both ends optionally reference that key; the client constructs its URI from it. Missing/empty uses `61765`; a full explicit URI wins. The exec self-check reads the same variable; no Sidecar `containerPort` is needed. See [advanced configuration](../deploy/CONFIGURATION.md#advanced-configuration-port-timeouts-and-multiple-roles).

### 4.4 Private registry pull credentials (skip for public images)

If a platform credential helper exists for this registry, verify it applies to this namespace/ServiceAccount. Otherwise, use Docker to create a separate login configuration for only the target registry and import it as a Secret in the same namespace:

```bash
export AKLESS_DOCKER_CONFIG="$AKLESS_WORK/registry-login"
mkdir -m 700 -p "$AKLESS_DOCKER_CONFIG"
docker --config "$AKLESS_DOCKER_CONFIG" login '<your-registry>'
kubectl -n "$AKLESS_NAMESPACE" create secret generic akless-registry-pull \
  --type=kubernetes.io/dockerconfigjson \
  --from-file=.dockerconfigjson="$AKLESS_DOCKER_CONFIG/config.json" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Add this to `spec.template.spec` in generated `deployment.yaml`, then proceed to section 5:

```yaml
imagePullSecrets:
  - name: akless-registry-pull
```

**Expected:** the Secret exists and the Deployment references the same name. Imported configuration must contain usable registry login data; local `credsStore` / `credHelpers` references alone cannot authenticate node pulls. Use a platform helper if policy prohibits local login storage. This directory contains registry credentials; protect it under organizational policy, clean it after import and never commit it. Pod events in section 5 establish actual pull success.

## 5. Deploy and verify the basic chain

### 5.1 Validate and deploy

```bash
kubectl -n "$AKLESS_NAMESPACE" apply --dry-run=server --validate=strict \
  -f "$AKLESS_WORK/deployment.yaml"
kubectl -n "$AKLESS_NAMESPACE" apply -f "$AKLESS_WORK/deployment.yaml"
kubectl -n "$AKLESS_NAMESPACE" rollout status deployment/akless-example --timeout=180s
kubectl -n "$AKLESS_NAMESPACE" get pods -l app=akless-customer-example
```

**Expected:** server-side validation and rollout succeed, usually with `2/2 Running`. Validation checks API/admission constraints, not image access or upstream permissions. Both Sidecar and example images use Alibaba Cloud Linux; the example provides Node.js 24.22.2. Nodes must be able to pull both configured references. Record both image digests; a private business image needs the same pull-credential setup as the Sidecar.

Select the new Pod from this deployment and retain its name for later commands to avoid inspecting an old replica during rollout:

```bash
export POD_NAME='<new-Pod-name-from-this-deployment>'
kubectl -n "$AKLESS_NAMESPACE" get pod "$POD_NAME" \
  -o jsonpath='{range .status.initContainerStatuses[*]}{.name}{" "}{.imageID}{"\n"}{end}'
kubectl -n "$AKLESS_NAMESPACE" exec "$POD_NAME" -c idaas-akless-sidecar -- \
  /usr/local/bin/idaas-akless-sidecar --healthcheck
kubectl -n "$AKLESS_NAMESPACE" logs "$POD_NAME" -c business --tail=10
```

**Expected:** Sidecar `imageID` matches the selected image/platform manifest; self-check exits 0 silently; business logs contain `[ok] got STS credentials from sidecar, expires at ...`. Native Sidecar state is in `initContainerStatuses`.

`/healthz`, Pod Ready and business `/healthz` check basic availability only, not upstream authentication/cloud authorization. The example acquires once at startup, with at most one token reread/retry on 401; after final failure it logs an error without background retries. Continue with explicit verification below.

### 5.2 Verify authenticated acquisition in the business container

This command uses the guide's Node.js example to validate the complete triple, expiry and anonymous rejection without printing credentials:

```bash
kubectl -n "$AKLESS_NAMESPACE" exec -i "$POD_NAME" -c business -- node <<'JS'
const http = require('node:http');
const { fetchJson, configuredCredentialsURI } = require('/app/credentials-client');
(async () => {
  const uri = configuredCredentialsURI();
  const c = await fetchJson(uri, 15000);
  const valid = c.Code === 'Success' &&
    [c.AccessKeyId, c.AccessKeySecret, c.SecurityToken].every(v => typeof v === 'string' && v.length > 0) &&
    Date.parse(c.Expiration) > Date.now();
  if (!valid) throw new Error('credential validation failed');
  for (const url of [uri, `${uri}?format=raw`]) {
    await new Promise((resolve, reject) => {
      const req = http.get(url, { agent: false, timeout: 5000 }, res => {
        res.resume();
        if (res.statusCode === 401) resolve();
        else reject(new Error(`expected anonymous 401, got ${res.statusCode}`));
      });
      req.on('timeout', () => req.destroy(new Error('anonymous check timeout')));
      req.on('error', reject);
    });
  }
  console.log(JSON.stringify({credentialValid: true, expiration: c.Expiration, anonymousDenied: true}));
})().catch(e => { console.error(e.message); process.exitCode = 1; });
JS
```

**Expected:** exit 0 with `credentialValid: true`, a future expiry and `anonymousDenied: true`. On 401, check the local token and client header rather than disabling authentication.

### 5.3 Verify actual cloud identity

Install the local [Alibaba Cloud CLI](https://help.aliyun.com/zh/cli/) and Python 3. Temporary credentials are piped to the CLI and stay in processes/pipes. Do not enable shell tracing, SDK debug logging or recording of intermediate credentials.

```bash
(
  set -euo pipefail
  set +x
  kubectl -n "$AKLESS_NAMESPACE" exec "$POD_NAME" -c business -- \
    node -e 'const client=require("/app/credentials-client");client.fetchJson(client.configuredCredentialsURI(),15000).then(c=>process.stdout.write(JSON.stringify(c))).catch(()=>{console.error("credential request failed");process.exitCode=1})' |
    python3 -c '
import json, sys
c = json.load(sys.stdin)
p = {"name": "akless-verification", "mode": "StsToken", "region_id": "cn-hangzhou",
     "access_key_id": c["AccessKeyId"], "access_key_secret": c["AccessKeySecret"],
     "sts_token": c["SecurityToken"]}
print(json.dumps({"current": p["name"], "profiles": [p]}))
' |
    aliyun sts GetCallerIdentity --config-path /dev/stdin \
      --profile akless-verification --region cn-hangzhou --secure
)
```

**Expected:** command succeeds, account matches the target and `Arn` identifies `assumed-role/<role>/<session>`, not an administrator/node role. This verifies cloud identity from a local CLI; **business-container cloud networking and application permissions remain unverified**. Complete those in your application in the next section.

## 6. Add the Sidecar to an existing business Pod

After demo verification, merge the settings into your Deployment's `spec.template.spec`. Do not overwrite an existing application with the entire demo Deployment or use `127.0.0.1` to communicate between different Pods.

1. Create `akless-settings` in the application's namespace and configure IDaaS federation for its actual ServiceAccount. ConfigMaps cannot be mounted across namespaces. You can retain an existing ServiceAccount if IDaaS trust matches.
2. Merge `idaas-akless-client-config`, `oidc-federated-token`, `credential-auth-token` and the complete native Sidecar definition. Retain Downward API, role restrictions, authentication switch, read-only mounts, security context, resources and exec probes. Keep authentication string `"true"`.
3. Add only the local-auth mount and credential variables to the business container. Retain your image, command, ports, resources and probes. Remove demo-only `demo-code` and `/app` mounts. Never mount IDaaS configuration/bootstrap identity in the business container.
4. Reconcile Pod `fsGroup`, verify token reads by the business UID and retain root ownership. Do not give all containers, including the native Sidecar, the same `runAsUser`: kubelet would change token ownership and authentication would fail at startup. Defaults use distinct non-root UIDs `10001` / `1000`; see [UID constraints](../deploy/CONFIGURATION.md#kubernetes-configuration-mapping). Before setting `automountServiceAccountToken: false`, check existing Kubernetes API token dependencies. The platform should provide a separate appropriately authorized identity where API access is needed.
5. Check `hostNetwork`, port conflicts, admission, service-mesh proxies and inter-container trust. This guide uses non-hostNetwork Pods with credentials reachable only over Pod loopback. Add no credential Service.

Merge this business-container fragment; it cannot be applied on its own:

```yaml
env:
  - name: IDAAS_AKLESS_CREDENTIALS_URI
    value: http://127.0.0.1:61765/credentials
  - name: IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE
    value: /run/secrets/idaas-akless/credential-auth/token
volumeMounts:
  - name: credential-auth-token
    mountPath: /run/secrets/idaas-akless/credential-auth
    readOnly: true
```

These variables are conventions for application adapters; setting them does not automatically integrate every SDK. Build your application image, validate its final manifest with `kubectl apply --dry-run=server --validate=strict`, deploy through your release process and use a new Pod for acceptance.

### 6.1 General HTTP integration: application responsibilities

On each actual acquisition, reread the local token file and send to the fixed loopback endpoint:

```http
GET /credentials HTTP/1.1
Host: 127.0.0.1:61765
Authorization: <bare-JWT-from-local-authentication-token-file>
Accept: application/json
```

Do not prepend `Bearer `. This is neither the IDaaS Access Token nor bootstrap identity. Reread after kubelet rotation rather than permanently copying startup contents into an environment variable. On 401, reread/retry at most once. Never fall back automatically to anonymous access or higher-privilege credentials.

| Cloud credentials | Default JSON fields | SDK injection |
| --- | --- | --- |
| Alibaba Cloud | `AccessKeyId`, `AccessKeySecret`, `SecurityToken`, `Expiration`; `Code=Success` | Set AK, SK and STS security token together |
| AWS | `AccessKeyId`, `SecretAccessKey`, `Token`, `Expiration` | Set AK, SK and session token together |
| Tencent Cloud | `tmpSecretId`, `tmpSecretKey`, `token`, `expiration`, `expiredTime` | Set temporary SecretId, SecretKey and token together; expiredTime is Unix seconds |

The role's upstream cloud selects the vendor; no vendor URL parameter is used. Supplying two keys without the security token fails cloud requests.

Two application refresh strategies:

- **Acquire before each cloud operation:** request the Sidecar, validate fields/lifetime and inject the complete triple from one response into that call. The Sidecar caches upstream credentials; the application does not keep a static SDK client indefinitely. This is useful for initial correct integration; evaluate overhead and concurrency for your workload.
- **Refreshing provider:** implement SDK provider acquisition/early refresh, cache per role and atomically replace the complete triple. Allow time for network retries and operations before expiry. Failure fallback must still have sufficient remaining validity; expired credentials must fail. Do not permanently reuse a static Credential created from startup credentials.

A request at the refresh point triggers Sidecar refresh: normally after 4/5 of the lifetime remaining at caching, or earlier to reserve the usable-cache margin plus one refresh budget when the lifetime permits. See [refresh policy](../deploy/CONFIGURATION.md#sidecar-startup-options). This does not update credentials already in application memory. Set a total acquisition deadline (example `15s`) and bounded backoff for recoverable errors. Applications decide retry safety for non-idempotent cloud operations.

#### Node.js Alibaba Cloud example

Copy the same version of `business-example/credentials-client.js` into your application. It requires Node.js 24+, rereads tokens on each acquisition, restricts requests to numeric-loopback `/credentials`, disables redirects and limits responses. Do not change it to accept user-supplied URLs.

This “acquire before each operation” example can serve as an application self-check. Install the pinned product SDK in your own Node.js project, save `cloud-identity.cjs` beside `credentials-client.js` and include both in the business image:

```bash
npm install --save-exact @alicloud/sts20150401@1.2.0 @alicloud/openapi-core@1.0.8
```

```javascript
'use strict';
const Sts = require('@alicloud/sts20150401');
const { $OpenApiUtil } = require('@alicloud/openapi-core');
const { fetchJson, configuredCredentialsURI } = require('./credentials-client');

async function getCloudIdentity() {
  // Acquire before every cloud operation; the Sidecar caches upstream credentials.
  const c = await fetchJson(configuredCredentialsURI(), 15000);
  if (c.Code !== 'Success' ||
      ![c.AccessKeyId, c.AccessKeySecret, c.SecurityToken].every(v => typeof v === 'string' && v.length > 0) ||
      !(Date.parse(c.Expiration) > Date.now() + 30000)) {
    throw new Error('credential incomplete or less than 30 seconds remaining');
  }
  const client = new Sts.default(new $OpenApiUtil.Config({
    type: 'sts',
    accessKeyId: c.AccessKeyId,
    accessKeySecret: c.AccessKeySecret,
    securityToken: c.SecurityToken,
    endpoint: 'sts.cn-hangzhou.aliyuncs.com',
    connectTimeout: 5000,
    readTimeout: 10000
  }));
  const result = await client.getCallerIdentity();
  return { accountId: result.body.accountId, arn: result.body.arn };
}

module.exports = { getCloudIdentity };
if (require.main === module) {
  getCloudIdentity().then(x => console.log(JSON.stringify(x))).catch(() => {
    // Do not log SDK requests, responses or raw errors containing temporary credentials.
    console.error('cloud identity verification failed');
    process.exitCode = 1;
  });
}
```

Run `node <business-code-directory>/cloud-identity.cjs` inside the updated container and expect the target account/role. See the [Alibaba Cloud STS Node.js SDK](https://github.com/aliyun/alibabacloud-typescript-sdk/tree/master/sts-20150401). Apply the same acquisition/injection pattern to actual business APIs, such as reading a specifically authorized OSS object and checking its contents. Set endpoint, permissions and timeouts for that product. Do not install dependencies temporarily inside a business container instead of building a release.

### 6.2 AWS container credential provider

Use an AWS SDK version with Token File support, initialize a refreshing default chain and configure:

```yaml
env:
  - name: AWS_CONTAINER_CREDENTIALS_FULL_URI
    value: http://127.0.0.1:61765/credentials
  - name: AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE
    value: /run/secrets/idaas-akless/credential-auth/token
```

Retain the local token mount. Change Sidecar default role, allowlist and IDaaS asset authorization to the target AWS role. Exclude source overrides from static AK, shared files, `AWS_CONTAINER_CREDENTIALS_RELATIVE_URI` and other identity injection. See [AWS container credentials](https://docs.aws.amazon.com/sdkref/latest/guide/feature-container-credentials.html) for variable/version support.

Not all AWS versions are guaranteed safe here. Before production, verify bare JWT headers, rereading rotated files, sustainable refresh and no token forwarding to proxies/redirects. Then check `GetCallerIdentity` and required APIs in the same process. Use a controlled HTTP adapter if these requirements are not met.

### 6.3 Alibaba Cloud URI mode and Tencent Cloud SDKs

With local authentication enabled, `ALIBABA_CLOUD_CREDENTIALS_URI` alone does not guarantee file-token headers. Prefer the authenticated HTTP adapter above or a custom provider. Tencent Cloud needs authenticated HTTP acquisition, triple mapping into its Credential/Provider and expiry refresh. Use official documentation for the specific language/product SDK version. This repository does not supply complete providers for all languages.

Authentication can be explicitly disabled, allowing any local caller to acquire without a token and weakening SSRF protection. This guide keeps it enabled for integration/acceptance. Compatibility settings are in `deploy/CONFIGURATION.md`; disabled mode is not the default integration method.

See [separate integration tests](../verification/native-uri/README.md) for a pinned Node.js native URI provider, authentication rejection and cloud identity. Optional long tests end on refresh, without waiting for initial STS expiry, so they do not replace the next section's cross-expiry acceptance.

## 7. Production acceptance

Complete these checks in the actual modified application and record results. Demo logs alone are insufficient.

| Check | Action and pass criteria |
| --- | --- |
| Identity/business permissions | From the business container, use Sidecar credentials for cloud identity and one required business API; expected account/role, resource and response content |
| Credential source | Remove static keys/unneeded providers; verify the role in cloud audit records to establish actual Sidecar identity use |
| Local isolation | Requests without a token to default/raw return 401; another Pod cannot reach this Pod's 61765 via Pod IP; business cannot read bootstrap identity |
| Token rotation | Same business process acquires and calls APIs through at least two real kubelet local-token rotations; record counts/successes/errors, never token contents or hashes |
| Cloud credential refresh | Same process runs beyond initial STS `Expiration`, recording later expirations and successful cloud calls after first-credential expiry |
| Transient recovery | In a test environment, make the Sidecar temporarily unavailable; acquisition has a total timeout/bounded backoff and recovers afterward, without anonymous/admin fallback |
| Insufficient validity | In a test environment, insufficiently valid/expired credentials stop being used and cause errors; no expired cloud calls |

The demo acquires once at startup and cannot replace sustained business tests. A dedicated test Pod can temporarily use `expirationSeconds: 600` for faster rotation observation, then restore production settings. Do not alter production cluster clocks. See same-version `deploy/rotation-integration-test.js` and confirm its target/prerequisites before use.

Kubernetes file rotation, Sidecar STS refresh and SDK in-memory updates are independent. Revoking IDaaS authorization may not instantly invalidate issued STS. Permission-change verification must cover new acquisition and remaining lifetime of previously issued credentials.

## 8. Troubleshooting

Inspect current Pod events and Sidecar logs without printing token files or credential bodies:

```bash
kubectl -n "$AKLESS_NAMESPACE" describe pod "$POD_NAME"
kubectl -n "$AKLESS_NAMESPACE" logs "$POD_NAME" -c idaas-akless-sidecar --tail=50
```

| Symptom | Diagnosis and correction |
| --- | --- |
| `ImagePullBackOff` | Check actual reference, node connectivity, registry visibility and same-namespace Secret; public references come from the release page |
| `exec format error` | Image/node architecture mismatch; rebuild or change scheduling |
| Native Sidecar manifest rejected | Check control-plane/node versions and admission; do not remove `restartPolicy: Always` and continue unchanged |
| `initialize local credential authentication failed` | Check read-only projection, root ownership, non-root user, fsGroup, Downward API, fixed local audience and Pod binding; common UIDs may cause kubelet to change ownership; never copy tokens or chmod projections |
| Connection refused | Check same-Pod placement, Sidecar startup, port conflict and listener/business URI agreement |
| HTTP 400 | Read code; for `InvalidAuthentication` check Bearer/duplicate headers; otherwise inspect roleArn/format |
| HTTP 401 | Missing/mismatched local token; reread this Pod's local token and retry once at most; ensure it is not bootstrap identity |
| HTTP 403 `RoleNotAllowed` | Role outside local allowlist; confirm intended authorization before updating/recreating |
| HTTP 503 `AuthenticationUnavailable` | Invalid/expired/unreadable reference; inspect kubelet projection and clock synchronization; no anonymous fallback |
| HTTP 502 | Inspect upstream authentication/Developer API logs, DNS, HTTPS, configuration, role state, functional/data permission, total timeout and returned expiry |
| `invalid_client_assertion` | Bootstrap iss/aud/sub/kid mismatch with trust, JWKS or federation; check signing-key rotation |
| STS acquired but cloud API denied | Check actual identity, full triple injection, role/resource policies, region and endpoint; IDaaS authorization is not product authorization |
| Initial success, later failure | Look for cached token contents/static SDK clients; check token rotation, STS expiry and SDK refresh separately |
| ConfigMap change ineffective | Startup-only settings; recreate Pods and check CLI overrides |

The Developer API client ignores environment HTTP proxies and redirects. If egress requires an explicit HTTP proxy, setting `HTTPS_PROXY` alone does not enable this path. Confirm supported networking with the platform.

## 9. Configuration updates, upgrades and cleanup

Precedence is **CLI > nonempty environment > defaults**. The example shares ConfigMap `IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED` between both ends, retaining string `"true"`. Default role and allowlist come from `IDAAS_AKLESS_DEFAULT_ROLE_ARN` and `IDAAS_AKLESS_ALLOWED_ROLE_ARNS`. See the same-version [configuration contract](../deploy/CONFIGURATION.md) for ports, timeouts and multiple roles.

For old `8080` deployments, first explicitly retain the old port or migrate all endpoints to `61765` together. The port key is optional, but code/templates must match; see [upgrade notes](../deploy/CONFIGURATION.md#default-port-and-upgrades).

After changing JSON, role, allowlist, port or authentication, update the ConfigMap and recreate Pods. Recheck the allowlist with default-role changes. Section 4 commands reset the allowlist to the default role and enable authentication; preserve your intended values when customizing. If advanced configuration changes the Deployment, revalidate/apply it too.

```bash
kubectl -n "$AKLESS_NAMESPACE" rollout restart deployment/akless-example
kubectl -n "$AKLESS_NAMESPACE" rollout status deployment/akless-example --timeout=180s
```

For image upgrades, update `SIDECAR_IMAGE` and `BUSINESS_IMAGE` with matching docs/templates, regenerate/validate/apply manifests and recheck identity, permissions and rotation on a new Pod. Retain both previous digests, the manifest and configuration snapshot. Deployment rollback does not restore ConfigMaps, Secrets or IDaaS authorization. Same-name ConfigMap changes require restoring old values and recreating Pods for configuration rollback.

To clean only this guide's demo resources:

```bash
kubectl -n "$AKLESS_NAMESPACE" delete deployment akless-example
kubectl -n "$AKLESS_NAMESPACE" delete configmap akless-settings akless-demo-code
kubectl -n "$AKLESS_NAMESPACE" delete serviceaccount akless-business
```

First confirm no other workloads reuse them. Revoke demo functional/data grants and remove demo federation in IDaaS as needed; do not delete roles/providers still serving other applications.

## Appendix: credential HTTP API

| Request | Purpose |
| --- | --- |
| `GET /credentials` | Default role, vendor default fields |
| `GET /credentials?roleArn=<URL-encoded-role-external-ID>` | Explicit role, requiring local allowlist and IDaaS authorization |
| `GET /credentials?format=raw` | Declared Developer API fields, not original bytes or unknown-field passthrough |
| `GET /healthz` | Unauthenticated local HTTP health, without upstream acquisition |

All `/credentials` requests, including cache/raw, require local authentication in this guide. Omitted/empty `roleArn` uses the default; encode explicit IDs through standard URL tools instead of concatenating unencoded strings. `format` can only be omitted or `raw`, not `format=aws` or other vendor selection. See same-version `docs/CREDENTIALS-RESPONSE.md`.
