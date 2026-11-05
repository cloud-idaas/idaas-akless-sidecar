# syntax=docker/dockerfile:1
# Build from the repository root; cross-compile on the builder platform.
ARG BUILD_BASE_IMAGE=golang:1.26.10-alpine3.23@sha256:34414731924cf2263160f0021f5cf6e1807382d85248f7cb419d4187b96d5e43
ARG RUNTIME_BASE_IMAGE=alibaba-cloud-linux-3-registry.cn-hangzhou.cr.aliyuncs.com/alinux3/alinux3:3.13.4@sha256:fd25a109fcbd1ff33692ac32e1890c986fe7b4b656ab54a27a69aba04b8360c7
FROM --platform=$BUILDPLATFORM ${BUILD_BASE_IMAGE} AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
# Cache downloaded module dependencies separately from source.
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" -o /out/idaas-akless-sidecar ./cmd/idaas-akless-sidecar

FROM ${RUNTIME_BASE_IMAGE}
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.version=${VERSION} \
      org.opencontainers.image.revision=${REVISION}
# The pinned ALinux image already supplies ca-certificates and shadow-utils.
RUN . /etc/os-release && test "$ID" = alinux && \
    test -s /etc/pki/tls/certs/ca-bundle.crt && \
    groupadd --gid 10001 sidecar && \
    useradd --no-log-init --uid 10001 --gid 10001 --no-create-home --shell /sbin/nologin sidecar
USER 10001:10001
COPY --from=builder /out/idaas-akless-sidecar /usr/local/bin/idaas-akless-sidecar
COPY LICENSE /usr/share/licenses/idaas-akless-sidecar/LICENSE
COPY licenses/ /usr/share/licenses/idaas-akless-sidecar/third-party/
# 61765 仅 Pod 内 loopback，EXPOSE 仅作声明，不发布 Service。
EXPOSE 61765
ENTRYPOINT ["/usr/local/bin/idaas-akless-sidecar"]
# Let the binary apply CLI > environment > default configuration precedence.
CMD []
