# Multi-platform image publishing with GitHub Actions

**English** | [简体中文](PUBLISHING-IMAGES.zh-CN.md)

Use the Registry username and fixed password configured on the ACR console's Access Credentials page to publish both images for `linux/amd64` and `linux/arm64`. This password is neither a RAM AccessKey Secret nor an Alibaba Cloud console password.

## 1. Minimal publishing path

Version tag → checkout → QEMU → Buildx → direct Registry login using GitHub Secrets → build/push both multi-platform images → verify remote index digests and both platform runtimes.

[publish-images.yml](../.github/workflows/publish-images.yml) uses four official Actions pinned to full SHAs and the existing Dockerfiles. [build-images.sh](../deploy/build-images.sh) remains single-platform. No RAM AccessKey, OIDC provider, publishing role or GetAuthorizationToken exchange is required. No additional host project Go/Node/Java toolchain is needed.

This workflow does not deploy to Kubernetes, run live cloud tests, update `latest`, publish test images or generate SBOM/signatures. Complete release-candidate checks before tagging.

All four Dockerfiles deliver Alibaba Cloud Linux 3.13.4 runtime layers. The Go compiler stage remains Go/Alpine; Node.js 24.22.2 and npm are copied from a digest-pinned official glibc image into ALinux, with its runtime libraries and numeric non-root users. These artifact stages are not delivered operating-system layers. ALinux and toolchain defaults are pinned by multi-platform index digest in the Dockerfiles. The publishing checks verify `/etc/os-release`, UIDs 10001/1000 and Node 24 on both platforms.

## 2. Prerequisites

1. Sanitize candidate contents and publication history using [release checks](RELEASING.md); do not expose private history containing real environment information.
2. The publishing GitHub repository is `cloud-idaas/idaas-akless-sidecar`; pre-create the two ACR repositories `idaas-akless-sidecar` and `idaas-akless-business-example`.
3. Use an approved Registry endpoint reachable from the runner. This workflow changes no access controls; GitHub-hosted runners have no inherent VPC connectivity.
4. Verify that the credential's owning identity can push/pull both repositories. Existing sufficient permissions need no additional grant. A successful login alone does not establish push permission; see section 4 if access is denied.
5. Configure image visibility and consumer pull access separately from source visibility.
6. The build network must reach the pinned input registries, Go module sources and `https://mirrors.aliyun.com/` for ALinux OS/updates RPMs. SDK test-image builds also use the npm registry.

## 3. Obtain the ACR username and fixed password

### 3.1 Reuse existing access credentials

Reuse the credentials already created in ACR. Do not create a separate RAM AccessKey or OIDC provider. Prefer credentials associated with a RAM identity whose permissions fit publishing, without broadening them to account-wide administration.

Open **ACR → target region → instance list → target instance → Instance Management → Access Credentials**. Copy the complete username and Registry hostname from the displayed `docker login` command. Use the Registry fixed password configured for that identity.

A RAM username may include `@` and the account's default domain. Copy it exactly; do not replace it with a numeric account ID, display name or AccessKey ID. Different RAM users have independent fixed passwords.

### 3.2 Set or recover a fixed password

Set the password on the target identity's ACR Access Credentials page if none exists. Skip this step when existing credentials work; do not reset them unnecessarily. RAM users need console access to configure their own credentials. Ask the account administrator for assistance if access is unavailable; OIDC creation is unnecessary.

If the password is forgotten, follow the console reset process after assessing other image-publishing/pulling consumers. Fixed passwords have no fixed expiry and remain valid until reset or revoked. Job-end `docker logout` does not revoke the cloud-side password.

### 3.3 Store GitHub Secrets

Open **Settings → Secrets and variables → Actions → Secrets → New repository secret**:

| Secret | Source |
| --- | --- |
| `ACR_USERNAME` | Complete username in the ACR login instructions |
| `ACR_PASSWORD` | Registry fixed password configured for the same identity |

Enter the password exactly, without adding quotes, JSON, whitespace or line breaks. Never put actual credentials into documents, source, Variables, images, build arguments or logs. Do not enable `set -x`. GitHub does not display saved secret plaintext; update the value to replace it.

Coordinate password changes with other consumers: update the ACR password and GitHub `ACR_PASSWORD`, verify on a protected tag, and update other users as needed. Do not reset shared credentials without assessing dependencies.

## 4. Whether additional authorization is needed

Creating credentials authenticates an identity; it does not add repository permissions. ACR still checks the owning identity's RAM policy.

- Already able to push/pull both targets: no new authorization is needed.
- Login succeeds: still confirm repository push/pull access.
- RAM user receives push denied/403: ask an administrator to grant the two repository permissions, rather than creating an AccessKey or OIDC provider.

Only if a RAM user lacks access, an administrator can adapt this same-account ACR Enterprise policy and attach it to the credential's owning user. Direct fixed-password login needs no additional `cr:GetAuthorizationToken` grant for an API credential exchange.

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

Do not grant administrator, `CreateRepository` or `DeleteRepository`. Validate cross-account access separately; credentials do not bypass explicit Deny policies.

## 5. Configuration sources: three Variables and two Secrets

Create non-sensitive values under **Settings → Secrets and variables → Actions → Variables**, and credentials under **Secrets**.

| Name | Type | Source |
| --- | --- | --- |
| `RELEASE_REPOSITORY` | Variable | `cloud-idaas/idaas-akless-sidecar`, exactly matching `github.repository` |
| `ACR_REGISTRY` | Variable | Registry hostname in ACR login instructions; no scheme, path or trailing slash; not the OpenAPI endpoint |
| `ACR_NS` | Variable | ACR namespace containing both repositories, not a Kubernetes namespace |
| `ACR_USERNAME` | Secret | Complete username from the same credentials page, preserving `@` and other characters |
| `ACR_PASSWORD` | Secret | Registry fixed password for that identity |

The workflow no longer requires ACR region/instance variables, RAM AccessKeys, role/provider ARNs, GitHub numeric IDs or subjects. Remove obsolete configuration only after checking other consumers; do not delete shared cloud resources or reset credentials as part of cleanup.

Section 4's optional policy placeholders come from the owning cloud account ID under the console avatar, target instance region/ID in its overview, and the repositories' ACR namespace. These values are not mandatory workflow variables.

Setup order: confirm GitHub/ACR targets → obtain existing username/password and confirm access → fill three Variables/two Secrets → restrict `v*` tag creation, modification, deletion and bypass to release maintainers and protect workflow changes → publish using section 6.

The workflow needs only `contents: read` and four SHA-pinned Actions. It does not handle PRs. Forks lack matching release configuration and Secrets. Ordinary branch dispatch is skipped; `main` additionally supports verification of existing images without building or pushing.

## 6. Publish a version

1. Include the workflow and documentation in the release source. Sync the reviewed candidate to `https://github.com/cloud-idaas/idaas-akless-sidecar.git`; pushing to an internal GitLab remote does not trigger GitHub Actions. Manual dispatch additionally requires this workflow to exist on GitHub's default branch.
2. Complete `make check`, `make check-linux` and applicable SDK/live acceptance. `make check` does not execute all thirteen SDK contracts. Prepare their dependencies using [the SDK guide](../verification/sdk-contracts/README.md), then run `make check-sdk-contracts` when required.
3. From a clean release checkout, verify the final GitHub remote and exact commit.
4. Create a new, unused version tag:

```bash
git status --short
git remote get-url --push origin
git rev-parse HEAD
git tag -a v1.2.3 -m "Release v1.2.3"
git push origin refs/tags/v1.2.3
```

This version is an example, not an existing release. Accepted tags have the form `vMAJOR.MINOR.PATCH`, optionally followed by a prerelease suffix such as `-rc.1`, without `+` build metadata. The workflow never updates `latest`. Do not move protected release tags to another commit.

5. Watch **Actions → Publish multi-platform images** for that tag.
6. The Sidecar embeds the tag as `VERSION` and the checked-out full commit as `REVISION`. Each image is built once with `--platform linux/amd64,linux/arm64 --push`. Provenance/SBOM are explicitly disabled in this first publishing path.

## 7. Acceptance and mixed-architecture deployment

Publishing passes only when the complete job succeeds and the summary contains both verified index references:

- Both remote indexes contain amd64 and arm64.
- Each remote tag digest matches `containerimage.digest` from its own Buildx build; a changed tag fails verification. Platform descriptors are read from that immutable index, then each runtime check uses its platform's child manifest digest. This avoids classic Docker's local digest conflict when running two architectures from the same index reference. The published deployment references remain index digests.
- Both Sidecar platform variants report the expected version and full source commit.
- Both business variants pass Node architecture, module-loading and local HTTP `/healthz` checks.
- Deployment references use the multi-platform **index digest**, not one platform's child manifest digest.

The job summary provides two `name@sha256:...` references. Nodes in a mixed cluster use the same index reference and select the matching platform variant.

ARM smoke checks use QEMU; they do not establish full native-ARM application behavior. They also do not verify live IDaaS/cloud permissions, projected-token rotation or operation across STS expiry. The business example catches credential-acquisition errors and remains running, so process survival alone cannot establish successful credential acquisition.

## 8. Failure handling, retry and rollback

| Symptom | Check |
| --- | --- |
| Job skipped | Repository variable match, selected tag, deleted-tag event |
| Secret missing or credentials invalid | ACR_USERNAME/ACR_PASSWORD saved under Secrets, matching Registry identity |
| ACR login fails | Exact console username, Registry fixed password and target hostname; not AccessKeys or console passwords |
| Push denied | Pre-created repositories, exact repository resource ARNs, network conditions |
| Exec format error | QEMU initialization, remote platform descriptors, target binaries |
| `cannot overwrite digest` while switching runtime-test architectures | Run each platform's child manifest digest selected from the verified immutable index; classic Docker cannot associate one local index reference with two platform image IDs |
| One platform missing | Use one multi-platform push; do not overwrite a shared tag with separate single-platform pushes |
| Remote tag no longer matches this build | Another publisher may have overwritten the tag; investigate and use the intended build digest or a new version, without accepting the replacement as this release |
| Fixed password reset or revoked | Update ACR_PASSWORD and coordinate consumers before retrying; the one-hour temporary-password limit does not apply |
| One image succeeds and the other fails | Cross-repository publishing is not atomic; partial results are not a verified release |

Use **Re-run jobs** on the same tag run, or select that tag for `workflow_dispatch`, only when the existing workflow and a rebuild are appropriate. Re-runs retain the original source ref and workflow revision; they do not pick up a fix from `main`.

### Verify already-published immutable images

When both images already exist and only acceptance failed, run the corrected verifier from `main` without rebuilding, pushing, moving a source tag or changing ACR tag immutability:

1. Open **Actions → Publish multi-platform images → Run workflow** and select branch **main**.
2. Enable **verify_existing** and set **release_tag** to the existing source/image tag.
3. Set **sidecar_digest** and **business_digest** to the complete index digests recorded by the original build/run. Do not substitute child manifest digests or accept arbitrary current tags as evidence of the original build.
4. Run the workflow. It checks out the existing source tag separately to resolve its original commit, skips Build and push, checks both remote tags against the supplied index digests, and runs both platforms using their child manifests. The Sidecar version/revision must match the original release tag/commit, not the newer `main` commit containing the verifier.
5. Accept the new verification run only after the whole job succeeds and the summary reports verification only. The original failed run remains historical evidence; this does not retroactively turn it green.

Missing/mismatched digests, missing source tags or runtime failures stop verification. Fixing the verifier does not authorize replacing existing image content.

If ACR tag immutability rejects an existing tag, preserve known digests and assess the failed stage. If retry cannot finish without replacing an existing release, issue a new revision version. Do not disable immutability or force-move the source tag. Rebuilds may produce different digests because build inputs include package repositories.

Roll back deployments using a previously accepted multi-platform index digest. The workflow changes no Kubernetes resources.

## 9. Verification status

On November 5, 2026, ALinux migration validation passed all four dual-platform builds and eight image/platform runtime checks, six Linux projected-mount cases and `make check`. An isolated amd64 ACK run passed authenticated checks (15), anonymous checks (13), the multi-cloud matrix (9/9), native Alibaba checks (2/2), all four native SDK mode/authentication combinations and real kubelet token rotation through the old-token grace cutoff. Both candidate runtime-image indexes were pushed to ACR and the workflow's remote-index/runtime verification step passed locally against their immutable digests. Validation used a modified working-tree candidate; formal acceptance must use a clean, reviewed release tag.

This does not establish a completed GitHub-hosted Secrets username/password → ACR workflow run. Native ARM node integration and operation beyond real STS expiry were not covered. The three pinned input indexes provide amd64/arm64; repeat candidate acceptance after future input or runtime changes.

After repository changes, administrators must configure the external prerequisites and accept the first protected-tag run in the final GitHub repository. Separate PR CI, live cloud workflows, signing/SBOM, native ARM runners and automatic deployment are subsequent extensions.

## 10. Official references

- [ACR access credentials](https://help.aliyun.com/zh/acr/user-guide/configure-access-credentials)
- [ACR RAM permissions](https://help.aliyun.com/zh/acr/user-guide/ram-authorization-information)
- [GitHub Secrets](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets)
- [Official ACR login Action](https://github.com/aliyun/acr-login)
- [Docker multi-platform publishing](https://docs.docker.com/build/ci/github-actions/multi-platform/)
- [Buildx build metadata and digest](https://docs.docker.com/reference/cli/docker/buildx/build/#metadata-file)
- [GitHub manual dispatch requirements](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_dispatch)
