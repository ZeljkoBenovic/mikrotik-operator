#!/usr/bin/env bash
# Cloud Agent environment bootstrap for the MikroTik Kubernetes operator.
#
# Installs the system tooling that is not part of Cursor's default base image
# and primes the Go module / npm caches so subsequent builds are fast. The
# script is idempotent: it can run repeatedly and against a warm cache.
#
# Tool versions are kept in sync with .github/workflows/ci.yml.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

KUBECTL_VERSION="v1.37.0"
GOLANGCI_LINT_VERSION="2.13.2"
KUBECONFORM_VERSION="v0.6.7"
ACTIONLINT_VERSION="v1.7.7"
GOVULNCHECK_VERSION="v1.7.0"
ENVTEST_K8S_VERSION="1.31.0"
# Build Go-based tools with the same toolchain the module targets so the
# resulting binaries can analyse this repository (go.mod pins go 1.27.0).
GO_TOOLCHAIN="go1.27.0"

log() { printf '\n=== %s ===\n' "$*"; }

# The default base image ships Go and Node; fail early with a clear message
# if that assumption ever breaks instead of producing confusing errors later.
command -v go >/dev/null 2>&1 || { echo "go toolchain missing from base image" >&2; exit 1; }
command -v node >/dev/null 2>&1 || { echo "node runtime missing from base image" >&2; exit 1; }

log "System packages"
export DEBIAN_FRONTEND=noninteractive
sudo apt-get update -y
sudo apt-get install -y --no-install-recommends \
  -o Dpkg::Options::=--force-confold \
  ca-certificates curl git make python3 fuse-overlayfs

log "Docker Engine"
if ! command -v docker >/dev/null 2>&1; then
  curl -fsSL https://get.docker.com | sudo sh
fi
sudo groupadd -f docker
sudo usermod -aG docker "$(id -un)" || true
# Docker's rootfs lives on an overlay filesystem inside the Cloud Agent VM, so
# the overlay snapshotter cannot mount overlay-on-overlay. fuse-overlayfs works
# in that nested layout and keeps image builds functional.
sudo mkdir -p /etc/docker
sudo tee /etc/docker/daemon.json >/dev/null <<'JSON'
{
  "storage-driver": "fuse-overlayfs",
  "features": { "containerd-snapshotter": false }
}
JSON

log "kubectl ${KUBECTL_VERSION}"
if [ "$(kubectl version --client -o json 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin)["clientVersion"]["gitVersion"])' 2>/dev/null || true)" != "${KUBECTL_VERSION}" ]; then
  tmp="$(mktemp -d)"
  curl -fsSL -o "${tmp}/kubectl" "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl"
  sudo install -m 0755 "${tmp}/kubectl" /usr/local/bin/kubectl
  rm -rf "${tmp}"
fi

log "Helm"
if ! command -v helm >/dev/null 2>&1; then
  curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | sudo bash
fi

log "k3d"
if ! command -v k3d >/dev/null 2>&1; then
  curl -fsSL https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | sudo bash
fi

log "golangci-lint ${GOLANGCI_LINT_VERSION}"
if [ "$(golangci-lint version 2>/dev/null | grep -o "version ${GOLANGCI_LINT_VERSION}" || true)" = "" ]; then
  tmp="$(mktemp -d)"
  base="golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64"
  curl -fsSL -o "${tmp}/glci.tar.gz" \
    "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/${base}.tar.gz"
  curl -fsSL -o "${tmp}/checksums.txt" \
    "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-checksums.txt"
  expected="$(grep "  ${base}.tar.gz$" "${tmp}/checksums.txt" | awk '{print $1}')"
  actual="$(sha256sum "${tmp}/glci.tar.gz" | awk '{print $1}')"
  [ "${expected}" = "${actual}" ] || { echo "golangci-lint checksum mismatch" >&2; exit 1; }
  tar -C "${tmp}" -xzf "${tmp}/glci.tar.gz"
  sudo install -m 0755 "${tmp}/${base}/golangci-lint" /usr/local/bin/golangci-lint
  rm -rf "${tmp}"
fi

log "Go-based tools"
# Install into the user GOBIN, then publish onto the system PATH.
GOBIN_DIR="$(go env GOPATH)/bin"
mkdir -p "${GOBIN_DIR}"
install_go_tool() {
  local pkg="$1" bin="$2"
  GOTOOLCHAIN="${GO_TOOLCHAIN}" GOBIN="${GOBIN_DIR}" go install "${pkg}"
  sudo install -m 0755 "${GOBIN_DIR}/${bin}" "/usr/local/bin/${bin}"
}
install_go_tool "github.com/yannh/kubeconform/cmd/kubeconform@${KUBECONFORM_VERSION}" kubeconform
install_go_tool "github.com/rhysd/actionlint/cmd/actionlint@${ACTIONLINT_VERSION}" actionlint
install_go_tool "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" govulncheck
install_go_tool "sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.24" setup-envtest

log "envtest control-plane binaries (${ENVTEST_K8S_VERSION})"
# Fetch etcd + kube-apiserver so the operator and admin UI can run against a
# real API server without a container runtime.
setup-envtest use "${ENVTEST_K8S_VERSION}" >/dev/null

log "Go modules"
go mod download

log "Web UI dependencies and build"
(cd web/ui && npm ci && npm run build)

log "Build operator and admin UI binaries"
go build -trimpath -o bin/manager ./cmd/manager
go build -trimpath -o bin/ui-backend ./cmd/ui-backend

log "Environment bootstrap complete"
