# Local and CI command runner. Install: https://github.com/casey/just
# Recipes that talk to a cluster or RouterOS need a Linux shell (CI, WSL, or Git Bash).

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]
set script-interpreter := ["bash", "-euo", "pipefail"]
set default-list

image_repository := env("IMAGE_REPOSITORY", "ghcr.io/zeljkobenovic/mikrotik-operator")
image_tag := env("IMAGE_TAG", "v0.2.0")
k3s_version := env("K3S_VERSION", "v1.36.4+k3s1")
k3s_channel := env("K3S_CHANNEL", "stable")
k3s_url := env("K3S_URL", "https://get.k3s.io")
k3s_kubeconfig_mode := env("K3S_KUBECONFIG_MODE", "640")
k3s_namespace := env("K3S_NAMESPACE", "mikrotik-operator-system")
k3s_kubeconfig := env("K3S_KUBECONFIG", "/etc/rancher/k3s/k3s.yaml")
helm_release := env("HELM_RELEASE", "mikrotik-operator")
helm_chart := env("HELM_CHART", "./charts/mikrotik-operator")

# Run unit tests with race detection, matching CI.
[group('dev')]
[script]
test:
    mapfile -t packages < <(go list ./... | grep -v /node_modules/ || true)
    go test -race -shuffle=on "${packages[@]}"

# Vet the same Go packages CI checks.
[group('dev')]
[script]
vet:
    mapfile -t packages < <(go list ./... | grep -v /node_modules/ || true)
    go vet "${packages[@]}"

# Format Go sources in the paths CI checks.
[group('dev')]
fmt:
    gofmt -w cmd internal api hack

# Fail if Go sources need gofmt.
[group('dev')]
fmt-check:
    test -z "$(gofmt -l cmd internal api hack)"

# Build the operator and admin UI backends.
[group('dev')]
build:
    go build -trimpath -o bin/manager ./cmd/manager
    go build -trimpath -o bin/ui-backend ./cmd/ui-backend

# Run admin UI unit tests.
[group('ui')]
ui-test:
    npm --prefix web/ui test

# Lint the Helm chart with and without the admin UI.
[group('packaging')]
helm-lint:
    helm lint "{{ helm_chart }}"
    helm lint "{{ helm_chart }}" --set ui.enabled=true

# Apply CRDs from config/crd/bases.
[group('packaging')]
install:
    kubectl apply -f config/crd/bases

# Apply raw namespace, RBAC, manager, and IngressClass manifests.
[group('packaging')]
deploy:
    kubectl apply -f config/namespace.yaml
    kubectl apply -f config/rbac
    kubectl apply -f config/manager
    kubectl apply -f config/ingressclass.yaml

# Install a single-node K3s server. Requires root on Linux.
[group('cluster')]
[script]
k3s-install:
    group="${K3S_KUBECONFIG_GROUP:-$(id -gn)}"
    curl -sfL "{{ k3s_url }}" | INSTALL_K3S_CHANNEL="{{ k3s_channel }}" INSTALL_K3S_VERSION="{{ k3s_version }}" INSTALL_K3S_EXEC="server --write-kubeconfig-mode={{ k3s_kubeconfig_mode }} --write-kubeconfig-group=${group}" sh -
    bash hack/k3s-wsl-docker-desktop.sh

# Uninstall K3s and the WSL Docker Desktop kubelet workaround.
[group('cluster')]
k3s-uninstall:
    /usr/local/bin/k3s-uninstall.sh
    sudo rm -f /etc/systemd/system/k3s.service.d/wsl-docker-desktop.conf /usr/local/sbin/k3s-wsl-wrap

# Install k3d from the upstream install script.
[group('cluster')]
k3d-install:
    curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash

# Install the operator chart onto the local K3s kubeconfig.
[group('cluster')]
[script]
install-operator ui="false":
    export KUBECONFIG="{{ k3s_kubeconfig }}"
    helm upgrade --install "{{ helm_release }}" "{{ helm_chart }}" \
        --namespace "{{ k3s_namespace }}" \
        --create-namespace \
        --set image.repository="{{ image_repository }}" \
        --set image.tag="{{ image_tag }}" \
        --set ui.enabled="{{ ui }}" \
        --set ui.image.tag="{{ image_tag }}"
    if [ "{{ ui }}" = "true" ]; then
        kubectl --namespace "{{ k3s_namespace }}" rollout status deployment -l app.kubernetes.io/component=ui --timeout=180s
    fi

# Install K3s, wait for the API, then install the chart. Pass `true` to enable the UI.
[group('cluster')]
[script]
test-install ui="false": k3s-install
    until KUBECONFIG="{{ k3s_kubeconfig }}" kubectl get nodes >/dev/null 2>&1; do sleep 2; done
    just install-operator "{{ ui }}"

# Install K3s and the chart with the unauthenticated admin UI enabled.
[group('cluster')]
test-install-ui: (test-install "true")

# RouterOS-backed E2E on k3d. Override E2E_ROUTER_IMAGE or E2E_OPERATOR_IMAGE as needed.
[group('e2e')]
e2e-test:
    bash hack/e2e/run.sh

# Admin UI HTTP CRUD E2E on k3d. Does not start RouterOS.
[group('e2e')]
e2e-ui-test:
    bash hack/e2e-ui/run.sh
