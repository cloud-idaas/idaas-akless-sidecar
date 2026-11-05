#!/usr/bin/env bash
# Build and verify both images; push unless --no-push is given.
# Required: ACR_REGISTRY, ACR_NS. Optional: IMG_TAG, IMG_ARCH, REVISION,
# BUILD_BASE_IMAGE, RUNTIME_BASE_IMAGE, NODE_BASE_IMAGE (complete image references).
# Uses local Buildx or a running Colima VM.
set -euo pipefail

PUSH=1
case "$#:${1:-}" in
  0:) ;;
  1:--no-push) PUSH=0 ;;
  1:--help|1:-h)
    echo "Usage: $0 [--no-push | --help]"
    echo "Build and verify both images; push by default. Requires ACR_REGISTRY and ACR_NS."
    exit 0
    ;;
  *) echo "Invalid arguments. Usage: $0 [--no-push | --help]" >&2; exit 2 ;;
esac
if [[ -n "${BASE_MIRROR:-}" ]]; then
  echo "BASE_MIRROR is no longer supported; use complete BUILD_BASE_IMAGE, RUNTIME_BASE_IMAGE and NODE_BASE_IMAGE references." >&2
  exit 2
fi
: "${ACR_REGISTRY:?ACR_REGISTRY not set, e.g. registry.cn-hangzhou.aliyuncs.com}"
: "${ACR_NS:?ACR_NS (registry namespace) not set}"
IMG_TAG="${IMG_TAG:-dev}"
ARCH="${IMG_ARCH:-linux/amd64}"

# Use the platform as a local image tag suffix.
SFX="${ARCH//\//-}"                                          # linux/amd64 -> linux-amd64
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
REVISION="${REVISION:-$(git describe --always --dirty --abbrev=12 2>/dev/null || echo unknown)}"
IMGS=(idaas-akless-sidecar idaas-akless-business-example)

# Dockerfiles own the pinned defaults; a mirror must supply a full reference.
B_GOLANG="${BUILD_BASE_IMAGE:-}"
B_RUNTIME="${RUNTIME_BASE_IMAGE:-}"
B_NODE="${NODE_BASE_IMAGE:-}"

# Select the Docker execution environment.
MODE="local"
if ! docker buildx version >/dev/null 2>&1; then
  if command -v colima >/dev/null 2>&1 && colima status >/dev/null 2>&1; then
    MODE="colima-vm"
    echo "[env] 本机无 buildx → 进 colima VM 构建（VM 自带 buildx + qemu binfmt）"
  else
    echo "[env] 本机没有 buildx，colima 也没在运行：先 colima start，或装 buildx。" >&2
    exit 1
  fi
else
  echo "[env] 使用本机 docker/buildx：$(docker buildx version | head -1)"
fi

# run_docker：从 stdin 收一段 bash 脚本并在选定环境执行。
run_docker() (
  # Colima shares the host home path; BSD mktemp requires a trailing X template.
  TMPSCRIPT="$(mktemp "${HOME}/.akless-build.XXXXXX")"
  trap 'rm -f "$TMPSCRIPT"' EXIT
  cat > "$TMPSCRIPT"
  if [[ "$MODE" == "colima-vm" ]]; then
    colima ssh -- env DOCKER_HOST=unix:///var/run/docker.sock \
      DOCKER_CONFIG="$HOME/.docker" bash "$TMPSCRIPT"
  else
    bash "$TMPSCRIPT"
  fi
)

echo "[1/4] 构建两个镜像（platform=$ARCH，运行底座 Alibaba Cloud Linux）"
{
  printf 'set -euo pipefail\nREPO=%q; ARCH=%q; SFX=%q; BG=%q; BA=%q; BN=%q; VERSION=%q; REVISION=%q\ncd "$REPO"\n' \
    "$REPO_ROOT" "$ARCH" "$SFX" "$B_GOLANG" "$B_RUNTIME" "$B_NODE" "$IMG_TAG" "$REVISION"
  cat <<'EOS'
echo "  → sidecar（宿主架构 Go 编译器交叉编译目标平台）"
sidecar_args=(--build-arg "VERSION=$VERSION" --build-arg "REVISION=$REVISION")
if [[ -n "$BG" ]]; then sidecar_args+=(--build-arg "BUILD_BASE_IMAGE=$BG"); fi
if [[ -n "$BA" ]]; then sidecar_args+=(--build-arg "RUNTIME_BASE_IMAGE=$BA"); fi
docker buildx build --load --platform "$ARCH" \
  "${sidecar_args[@]}" \
  -f Dockerfile -t "idaas-akless-sidecar:build-$SFX" . 2>&1 | tail -4
echo "  → business-example"
business_args=(-f business-example/Dockerfile -t "idaas-akless-business-example:build-$SFX")
if [[ -n "$BA" ]]; then business_args+=(--build-arg "BASE_IMAGE=$BA"); fi
if [[ -n "$BN" ]]; then business_args+=(--build-arg "NODE_BASE_IMAGE=$BN"); fi
docker buildx build --load --platform "$ARCH" \
  "${business_args[@]}" business-example/ 2>&1 | tail -4
EOS
} | run_docker

echo "[2/4] 校验产物架构（不对就绝不推送）"
{
  printf 'set -euo pipefail\nR=(%q %q); ARCH=%q; SFX=%q\n' "${IMGS[0]}" "${IMGS[1]}" "$ARCH" "$SFX"
  cat <<'EOS'
bad=0
for r in "${R[@]}"; do
  a=$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$r:build-$SFX" 2>/dev/null || echo none)
  if [[ "$a" != "$ARCH" ]]; then echo "  ✗ $r = $a（期望 $ARCH）" >&2; bad=1; else echo "  ✓ $r = $a"; fi
done
exit $bad
EOS
} | run_docker

if [[ "$PUSH" == "1" ]]; then
  echo "[3/4] 打 ACR 限定名 tag 并推送"
  {
    printf 'set -euo pipefail\nR=(%q %q); REG=%q; NS=%q; TAG=%q; SFX=%q\n' \
      "${IMGS[0]}" "${IMGS[1]}" "$ACR_REGISTRY" "$ACR_NS" "$IMG_TAG" "$SFX"
    cat <<'EOS'
for r in "${R[@]}"; do
  ref="$REG/$NS/$r:$TAG"
  docker tag "$r:build-$SFX" "$ref"
  echo "  docker push $ref"
  docker push "$ref" 2>&1 | tail -3
done
EOS
  } | run_docker
else
  echo "[3/4] 跳过推送（--no-push）"
fi

echo "[4/4] 完成。deploy/deployment.yaml 的两个 image: 字段应为："
for r in "${IMGS[@]}"; do echo "  ${ACR_REGISTRY}/${ACR_NS}/${r}:${IMG_TAG}"; done
cat <<'NOTE'
注意：
  · ACR 企业版命名空间若未开「自动创建仓库」，新仓库首次 push 会报
    push access denied / insufficient_scope —— 先在控制台建仓库或开启自动创建。
  · ACK 节点在 VPC 内拉取建议把 image 域名换成内网域名（同一份镜像、免公网流量）：
    <实例>-registry-vpc.<region>.cr.aliyuncs.com/<ns>/<repo>:<tag>
  · 私有仓库需 imagePullSecrets: acr-secret（见 deploy/README.md），
    或在 ACK 装 aliyun-acr-credential-helper 走免密拉取。
  · buildx 默认还会附带 provenance attestation（远端 manifest 里那条
    unknown/unknown）。个别老 registry/组件会因此挑刺，需要纯净单架构清单时
    给 docker buildx build 追加 --provenance=false --sbom=false。
NOTE
