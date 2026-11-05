# Documentation

**English** | [简体中文](README.zh-CN.md)

All current public documentation is available in English and Simplified Chinese. Choose a guide below; each page links to its counterpart at the top.

## Guides

| Document | Coverage |
| --- | --- |
| [Overview and first run](../README.md) | Project overview and first deployment |
| [Customer integration](CUSTOMER-INTEGRATION.md) | Console setup, customer deployment, business integration and acceptance |
| [Kubernetes / ACK deployment](../deploy/README.md) | Kubernetes / ACK templates and deployment commands |
| [Configuration contract](../deploy/CONFIGURATION.md) | CLI, environment variables, defaults, upgrades and timeout policy |
| [Business SDK integration](SDK-INTEGRATION.md) | Authenticated HTTP, cloud SDK providers and refresh responsibilities |
| [HTTP response contract](CREDENTIALS-RESPONSE.md) | Three-cloud fields, raw output, validation, caching and errors |
| [Security boundaries](SECURITY-HARDENING.md) | Token trust, isolation, logging and runtime boundaries |
| [Node.js business example](../business-example/README.md) | Dependency-free Node.js HTTP example |
| [Three-cloud integration tests](../business-example/multicloud/README.md) | Three-cloud STS and read-only cloud-resource checks |
| [Live native URI tests](../verification/native-uri/README.md) | Pinned Alibaba Cloud native URI authentication and live refresh checks |
| [Offline SDK contracts](../verification/sdk-contracts/README.md) | Thirteen offline handler-to-SDK field-mapping checks |
| [Testing and verification](TESTING.md) | Reusable regressions and the limits of each verification suite |
| [Open-source release checks](RELEASING.md) | Source/image preparation, sanitization, versions and license checks |
| [GitHub Actions image publishing](PUBLISHING-IMAGES.md) | ACR username/password Secrets, amd64/arm64 publishing, verification and retry |
| [Binary dependency licenses](../licenses/README.md) | Pinned Go binary dependency licenses and notices |

## Implementation references

Use these sources when reviewing documentation and comments; test expectations do not replace the implementation contract.

| Contract | Implementation |
| --- | --- |
| CLI defaults and precedence | [options.go](../cmd/idaas-akless-sidecar/options.go); the executable's `--help` |
| HTTP parameters, errors and cloud fields | [handler.go](../internal/credentials/handler.go), [contract.go](../internal/credentials/contract.go), [types.go](../internal/credentials/types.go) |
| Shared refresh, cache validity and backoff | [developerapi.go](../internal/credentials/developerapi.go), [cache.go](../internal/credentials/cache.go), [constants.go](../internal/credentials/constants.go) |
| Local-token trust and rotation | [auth.go](../internal/projectedauth/auth.go), [projected.go](../internal/projectedauth/projected.go); Linux mount verification in `projected_linux.go` |
| Business HTTP authentication and deadlines | [credentials-client.js](../business-example/credentials-client.js); one-shot usage in [app.js](../business-example/app.js) |
| Runtime images and publishing | The four Dockerfiles; [publishing workflow](../.github/workflows/publish-images.yml) and [local build script](../deploy/build-images.sh) |
| Test entry points and coverage gates | [Makefile](../Makefile) and each test directory's package/lock files; [verification boundaries](TESTING.md) |

## Language and maintenance conventions

English uses the unsuffixed `.md` filename; Simplified Chinese uses `.zh-CN.md`. Links within each language lead to the same-language page. Keep both versions aligned when changing configuration names/defaults, permissions, API contracts, examples, acceptance limits or release behavior. Commands, field names and identifiers retain their protocol spelling; translate explanatory text, comments and descriptive placeholders.

Use documentation, templates and images from the same release. Legal license/NOTICE texts retain their original wording; the license guide is bilingual. Ignored historical archives and local execution reports are not current customer documentation.

Comments should explain trust assumptions, ownership, timing and failure behavior where the code alone is insufficient. Keep operator procedures in the relevant guide and avoid duplicating version/default lists in multiple comments. Date acceptance evidence and distinguish offline fixtures, container smoke checks, real cloud integration and long-running expiry tests.
