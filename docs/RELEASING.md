# Open-source release checks

**English** | [简体中文](RELEASING.zh-CN.md)

## Release contents

Retain runtime code, build files, placeholder configuration, current integration/security documentation and [reusable tests](TESTING.md). Exclude real tenant configuration, cloud account roles, private image addresses, cluster contexts, temporary probes, execution reports and obsolete design snapshots. Keep configuration in a protected directory outside the repository. `.gitignore` prevents accidental additions; it cannot remove tracked files or historical commits.

Public examples use explicit placeholders or fictional fixtures. Live tests read roles from operator configuration. Docker contexts use allowlists, excluding local configuration and test reports from images.

The delivered Sidecar, business example and two SDK test images must report `ID=alinux` in `/etc/os-release`. They use Alibaba Cloud Linux 3.13.4, retaining Sidecar UID 10001 and Node UID/GID 1000. Go/Alpine and the official Node glibc image are build/artifact stages only. When upgrading pinned inputs, review both amd64/arm64 descriptors, runtime libraries, CA trust and licenses; retain Node's `/usr/share/licenses/nodejs/LICENSE` and npm's bundled license.

## Verify a release candidate

1. Run [local regressions and applicable integration tests](TESTING.md). Do not delete long-term tests or substitute fake-credential results for cloud verification.
2. Scan the candidate and every Git history intended for publication with Gitleaks and mandatory redaction. Review each finding; do not exclude entire documentation/test directories just to pass.
3. Manually review instance/application identifiers, accounts/roles, internal domains, private registries, resource names, personal paths and operational contexts. Secret scanners may not recognize these.
4. Check documentation links, CLI/environment names, permission scopes, token mounts and SDK version boundaries against this implementation.
5. Confirm the public source URL, version tag, public image address and digest. Verify source downloads, anonymous image pulls and claimed architectures without internal credentials.
6. Build from release source. In an isolated environment, follow the customer guide to verify identity, business permissions, rotation and operation across STS expiry. Source, templates, docs and images must belong to one version.
7. `/usr/share/licenses/idaas-akless-sidecar/` must contain the project license and [binary dependency licenses](../licenses/README.md). Review applicable copyright/notices on dependency upgrades and retain these materials in source distributions. Verify base-image/full-distribution licenses, SBOM and delivered materials for the actual candidate.

Gitleaks 8.30.1 examples below scan all local Git refs and a prepared external candidate directory excluding `.git`, dependency caches and real local configuration:

```bash
gitleaks git . --log-opts=--all --redact --no-banner
gitleaks dir '<release-candidate-directory>' --redact --no-banner
```

Working-tree cleanup does not remove historical objects. Import only the cleaned, reviewed candidate into the public repository, without copying private commit history. Review every commit, branch and tag intended for publication.

The target GitHub repository is `https://github.com/cloud-idaas/idaas-akless-sidecar.git`. Review its actual contents and push URL before publication; an internal GitLab `origin` does not publish to GitHub. When importing a sanitized candidate with new history, copy only reviewed source files into the separate checkout, excluding `.git`, ignored local files, caches and reports. Do not mirror the private repository or push all old branches/tags into the public repository. Put the workflow on the GitHub default branch before relying on manual dispatch, and create release tags only at reviewed GitHub commits.

If real keys, private keys or tokens are found, have their owner revoke/rotate them before cleaning content/history. Deleting files does not invalidate leaked credentials. Never copy original values into issues, PRs, test logs or scan reports.

Fill all `<...>` release information before formal publication. Placeholders do not establish that a public source address or image exists.

## Release identity and image licenses

For automated amd64/arm64 publishing to ACR Enterprise, follow [GitHub Actions image publishing](PUBLISHING-IMAGES.md). It covers Registry username/password Secrets and the tag-triggered workflow. Existing sufficient repository access needs no additional grant. The local `deploy/build-images.sh` remains single-platform; do not push two architectures to the same tag separately. Finish the candidate checks above before creating a release tag.

Build from a reviewed, clean checkout with `make build VERSION=<release> REVISION=<full-source-commit>`. Direct Sidecar Docker builds accept `--build-arg VERSION=<release> --build-arg REVISION=<full-source-commit>`; both are embedded in the executable and OCI version/revision labels. `deploy/build-images.sh` uses `IMG_TAG` as version and a Git description as revision unless `REVISION` is supplied. Formal releases should explicitly use a verified full commit. Defaults such as `dev`, `unknown` and `-dirty` descriptions are not formal provenance evidence.

Check image `/usr/local/bin/idaas-akless-sidecar --version` and OCI labels against source tags, image digests and acceptance records. Version strings are build declarations, not source verification or image signatures.

The Sidecar, Node.js business example and both Node integration images should include `/usr/share/licenses/idaas-akless-sidecar/LICENSE`. Isolated contexts keep matching root-license copies in `business-example/LICENSE` and `verification/native-uri/LICENSE`. Update both when the root license changes; `make check` rejects drift. The Sidecar also includes Go dependency licenses. Review full Node dependency/base-image licenses and SBOM for each actual distribution.
