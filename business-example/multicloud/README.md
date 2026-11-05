# Three-cloud STS and resource-query integration tests

**English** | [简体中文](README.zh-CN.md)

This directory retains repeatable business-container integration tests. Using operator-provided IDaaS configuration and three-cloud roles, tests obtain STS from the Sidecar and query bucket information in OSS, COS and AWS China S3 through pinned SDKs. Queries are read-only; empty lists can pass. Permission errors (403), timeouts and credential failures cause nonzero exit.

## Integration paths and boundaries

| Cloud | Local authentication enabled | Native Credentials URI |
| --- | --- | --- |
| Alibaba Cloud | Authenticated URI adapter → `ali-oss` | Pinned `@alicloud/credentials` 2.4.7 sends no local token and is expected to get 401; a separate disabled-authentication Pod verifies native acquisition and OSS queries |
| Tencent Cloud | Authenticated URI adapter → `cos-nodejs-sdk-v5` | Uses an adapter; no claim of native COS SDK URI support |
| AWS | Authenticated URI adapter → `S3Client` | `fromHttp` reads the authentication file, accesses the URI and supplies temporary credentials to `S3Client` |

Adapters access the URI on every credential resolution and retain AK, SK, session token and expiry from the same generation. Local authentication reuses `credentials-client.js`. OSS/COS clients are recreated for each query; long-running applications should connect acquisition to SDK refresh. AWS uses an asynchronous provider. These tests do not replace long-running acceptance across STS expiry and at least two kubelet rotations.

Queries use OSS Hangzhou, COS GetService and S3 `cn-north-1`. Roles need `oss:ListBuckets`, `cos:GetService` and `s3:ListAllMyBuckets` respectively. Other regions/AWS partitions need endpoint changes and separate verification. Do not automatically expand permissions to make tests pass.

## Local regression

Run from the repository root without real tenant configuration:

```bash
npm ci --prefix business-example/multicloud --ignore-scripts --no-audit --no-fund
npm test --prefix business-example/multicloud
```

Three layers use fictional credentials without IDaaS/cloud access:

- `integration.test.cjs`: fields, roles/URIs, expiry boundaries, resource queries and summaries; product SDKs are substituted.
- `sdk-providers.test.cjs`: real locked AWS/Alibaba Cloud URI SDKs with only file/HTTP boundaries replaced; verifies source precedence, token rereads, failures and Alibaba Cloud refresh under a simulated clock.
- `http-chain.test.cjs`: local HTTP simulates the Sidecar, joining real file reads, authenticated HTTP, three-cloud mapping and product-SDK boundaries to check rotation retries, same-generation triples and failure propagation.

Both runtime files must meet 80% line and branch coverage. Tests and third-party SDKs are excluded from the denominator. Coverage does not replace live cloud acceptance.

## Scenario coverage and remaining boundaries

| Scenario | Current automation | Remaining live acceptance |
| --- | --- | --- |
| Roles and credential sources | URL encoding, missing configuration, remote URI rejection; AWS explicitly suppresses RELATIVE_URI and static-token fallback | Expected tenant/account/role; no other injected identity sources |
| Triple and validity | Missing/blank/wrong-type fields, invalid/expired times and 60-second lifetime boundary for all clouds; Alibaba `Code=Success`; consistent Tencent expirations | Actual issued lifetimes, clock synchronization and cloud API acceptance |
| Authentication and rotation | All three HTTP paths reread once on 401, never retry 403/502; real AWS provider rereads files and does not fall back when missing | At least two real kubelet rotations, Pod/audience isolation and read-only projection; see [testing](../../docs/TESTING.md) |
| Native URI | Pinned AWS provider parsing/errors/source limits; pinned Alibaba provider's expected 401, cache and simulated refresh; Tencent adapter only | Real network behavior of the same SDK version; Alibaba's public Credential model exposes no expiry, so this directory's native positive tests check triples/resources only; extra expiry/identity checks are in [native-uri](../../verification/native-uri/README.md) |
| Product SDK injection and resources | Complete OSS/COS/S3 STS parameters, HTTPS/region/timeout, empty lists, permission failure, AWS cleanup and no bucket-name logging | Run real queries with your roles; successful listing proves neither object permissions nor expected identity |
| Failure decisions and logs | One failure does not stop other checks; missing-config CLI exits nonzero; no raw SDK bodies/credentials; fake 401 text in a body is not authentication rejection | Real cloud rejection, upstream interruption and recovery |
| Long-term refresh | Same-generation replacement for all adapters; AWS file rereads; real Alibaba SDK refresh with simulated time | Same business process and SDK client operating beyond the first STS expiry; the one-shot resource program does not provide this long test |

Before claiming accepted integration for a cloud, complete these tasks and retain redacted evidence:

1. Run the positive matrix below and check every result and business process exit code. Judge Alibaba native authentication rejection separately from disabled-mode positive tests.
2. Add expected-account/role identity assertions for each cloud before credential/resource checks. The current resource matrix does not compare identity automatically. Separate Alibaba native-uri identity checks do not cover other clouds or adapter paths.
3. Use dedicated rejection roles to verify local allowlist rejection, IDaaS unauthorized-role rejection, and acquired STS lacking cloud listing permission. The latter must show STS success, resource failure and nonzero overall exit; real 403 is not success.
4. Add a dedicated long-test entry retaining one business process and SDK client across initial STS expiry and at least two kubelet rotations. Verify atomic triple replacement, bounded acquisition failures and recovery. Repeated short tests or simulated-clock results do not replace this.

## Prepare live cluster tests

Follow [deployment](../../deploy/README.md) to create the namespace, ServiceAccount and pull credentials. Configure IDaaS federation for that ServiceAccount and authorize all three roles.

Copy root `cloud_idaas.json.example` into a protected external work directory. Fill instance, application, endpoints and federated credential name. Keep `oidcTokenFilePath` empty and set `oidcTokenFilePathEnvVarName` to `OIDC_TOKEN_FILE_PATH`. Never save real configuration in Git.

```bash
export AKLESS_TEST_CONTEXT='<your-kubectl-context>'
export AKLESS_TEST_CONFIG='<absolute-path-outside-repository>/client-config.json'
export IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN='<authorized-alibaba-role-arn>'
export IDAAS_AKLESS_TEST_AWS_ROLE_ARN='<authorized-aws-china-role-arn>'
export IDAAS_AKLESS_TEST_TENCENT_ROLE_ARN='<authorized-tencent-role-identifier>'
export AKLESS_TEST_ROLES="$IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN,$IDAAS_AKLESS_TEST_AWS_ROLE_ARN,$IDAAS_AKLESS_TEST_TENCENT_ROLE_ARN"
kubectl --context "$AKLESS_TEST_CONTEXT" -n idaas-akless create configmap idaas-akless-multicloud-config \
  --from-file=idaas-akless-client-config.json="$AKLESS_TEST_CONFIG" \
  --from-literal="IDAAS_AKLESS_DEFAULT_ROLE_ARN=$IDAAS_AKLESS_TEST_ALIBABA_ROLE_ARN" \
  --from-literal="IDAAS_AKLESS_ALLOWED_ROLE_ARNS=$AKLESS_TEST_ROLES" \
  --from-literal=IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=true \
  --from-literal=IDAAS_AKLESS_SIDECAR_LISTEN_PORT=61765 \
  --dry-run=client -o yaml | kubectl --context "$AKLESS_TEST_CONTEXT" apply -f -
```

Build/push the test image with `business-example` as context:

```bash
docker buildx build --platform linux/amd64 --push \
  -f business-example/multicloud/Dockerfile \
  -t '<registry>/<namespace>/idaas-akless-multicloud-test:<version>' business-example
```

Copy `deploy/multicloud-test.yaml` outside the repository. Replace placeholders for both Pods: Sidecar image, business test image, bootstrap audience and three `IDAAS_AKLESS_TEST_*_ROLE_ARN` variables. Roles must match those above; YAML does not expand host variables. Prefer image digests; match the bootstrap audience to IDaaS trust and retain local audience `idaas.akless.sidecar`.

```bash
export AKLESS_TEST_MANIFEST='<absolute-path-outside-repository>/multicloud-test.yaml'
kubectl --context "$AKLESS_TEST_CONTEXT" apply --dry-run=server --validate=strict -f "$AKLESS_TEST_MANIFEST"
kubectl --context "$AKLESS_TEST_CONTEXT" apply -f "$AKLESS_TEST_MANIFEST"
kubectl --context "$AKLESS_TEST_CONTEXT" -n idaas-akless logs -f akless-multicloud-test -c business
kubectl --context "$AKLESS_TEST_CONTEXT" -n idaas-akless logs -f akless-multicloud-native-ali-test -c business
```

Both Pods use dedicated ConfigMap/labels and leave `akless-demo` unchanged. The first stays authenticated and runs 9 checks. The second explicitly disables authentication, mounts no local token and runs only 2 positive Alibaba native URI checks. They create no Service, have 900-second deadlines and require native Sidecar support.

JSON output includes cloud, mode, success, expiry or returned bucket count. Errors include only constrained error codes/HTTP status, never names, credentials or raw SDK exceptions. Bucket count is the number returned, not necessarily the account total. Success requires summaries of 9/9 and 2/2 respectively, plus business-container termination with exit code 0:

```bash
kubectl --context "$AKLESS_TEST_CONTEXT" -n idaas-akless get pods -l app=akless-multicloud-test \
  -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.containerStatuses[?(@.name=="business")].state.terminated.exitCode}{"\n"}{end}'
```

A running container or missing exit code is not success. Expected Alibaba 401 checks only the rejection boundary. Delete the two test Pods before rerunning apply; afterward clean them and their dedicated ConfigMap, retaining resources used by other workloads.
