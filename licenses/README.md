# Sidecar binary dependency licenses

**English** | [简体中文](README.zh-CN.md)

The runtime image includes the project's root `LICENSE` at
`/usr/share/licenses/idaas-akless-sidecar/LICENSE` and this directory under
`/usr/share/licenses/idaas-akless-sidecar/third-party/`.

| Component | Version | License copy |
| --- | --- | --- |
| IDaaS Go Core SDK | v0.1.0-beta.1 | [Apache-2.0](idaas-go-core-sdk.LICENSE), [NOTICE](idaas-go-core-sdk.NOTICE) |
| golang-jwt/jwt/v4 | v4.5.2 | [MIT](golang-jwt.LICENSE) |
| google/uuid | v1.6.0 | [BSD-3-Clause](google-uuid.LICENSE) |
| Go runtime and standard library | Compiler version used to build the binary | [BSD-3-Clause](go.LICENSE) |

License texts are copied from the pinned Go module sources and the Go distribution.
When changing dependencies, review and update these copies and any applicable
third-party notices together with `go.mod`/`go.sum`. The base image's OS packages
are separate components; these files are not a complete image SBOM. Node/Python/Java
verification SDKs are not linked into the Sidecar binary.
