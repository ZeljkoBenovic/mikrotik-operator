IMG ?= ghcr.io/zeljkobenovic/mikrotik-operator:latest
IMAGE_REPOSITORY ?= ghcr.io/zeljkobenovic/mikrotik-operator
IMAGE_TAG ?= v0.1.0
K3S_VERSION ?= v1.36.4+k3s1
K3S_CHANNEL ?= stable
K3S_URL ?= https://get.k3s.io
K3S_KUBECONFIG_MODE ?= 640
K3S_KUBECONFIG_GROUP ?= $(shell id -gn)
K3S_NAMESPACE ?= mikrotik-operator-system
K3S_KUBECONFIG ?= /etc/rancher/k3s/k3s.yaml
HELM_RELEASE ?= mikrotik-operator
HELM_CHART ?= ./charts/mikrotik-operator
UI_ENABLED ?= false

.PHONY: all test ui-test fmt vet build manifests install deploy helm-lint k3s-install k3s-uninstall k3d-install install-operator test-install test-install-ui e2e-test e2e-ui-test
all: test build

test:
	go test $$(go list ./... | grep -v /node_modules/)

ui-test:
	npm --prefix web/ui test

# Starts k3d, installs the chart with the admin UI enabled, and CRUDs each CRD via HTTP.
# Requires Docker, k3d, kubectl, Helm, and Go. Does not start RouterOS.
E2E_UI_IMAGE ?= mikrotik-operator-ui:e2e
e2e-ui-test:
	E2E_UI_IMAGE=$(E2E_UI_IMAGE) bash hack/e2e-ui/run.sh

fmt:
	@files=$$(find . -name '*.go' -not -path './vendor/*'); test -z "$$files" || gofmt -w $$files

vet:
	go vet ./...

build:
	go build ./cmd/manager

manifests:
	@echo "CRDs are maintained in config/crd/bases; update them when API types change."

install:
	kubectl apply -f config/crd/bases

install-operator:
	KUBECONFIG=$(K3S_KUBECONFIG) helm upgrade --install $(HELM_RELEASE) $(HELM_CHART) --namespace $(K3S_NAMESPACE) --create-namespace --set image.repository=$(IMAGE_REPOSITORY) --set image.tag=$(IMAGE_TAG) --set ui.enabled=$(UI_ENABLED) --set ui.image.tag=$(IMAGE_TAG)
ifeq ($(UI_ENABLED),true)
	KUBECONFIG=$(K3S_KUBECONFIG) kubectl --namespace $(K3S_NAMESPACE) rollout status deployment -l app.kubernetes.io/component=ui --timeout=180s
endif

deploy:
	kubectl apply -f config/namespace.yaml
	kubectl apply -f config/rbac
	kubectl apply -f config/manager
	kubectl apply -f config/ingressclass.yaml

helm-lint:
	helm lint charts/mikrotik-operator

# Installs a single-node K3s server on a Linux host. Requires root privileges.
# Override K3S_VERSION or K3S_URL when testing a different K3s release/source.
# On WSL2 with Docker Desktop, kubelet crashes on the /Docker/host 9p mount;
# hack/k3s-wsl-docker-desktop.sh hides that mount from k3s only.
k3s-install:
	curl -sfL $(K3S_URL) | INSTALL_K3S_CHANNEL=$(K3S_CHANNEL) INSTALL_K3S_VERSION=$(K3S_VERSION) INSTALL_K3S_EXEC="server --write-kubeconfig-mode=$(K3S_KUBECONFIG_MODE) --write-kubeconfig-group=$(K3S_KUBECONFIG_GROUP)" sh -
	bash hack/k3s-wsl-docker-desktop.sh

k3s-uninstall:
	/usr/local/bin/k3s-uninstall.sh
	sudo rm -f /etc/systemd/system/k3s.service.d/wsl-docker-desktop.conf /usr/local/sbin/k3s-wsl-wrap

k3d-install:
	curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash

# End-to-end local setup: install K3s, wait for its API, then install the chart.
# The admin UI is off by default (no authentication). Enable it with
# `make test-install-ui` or `make test-install UI_ENABLED=true`.
test-install: k3s-install
	until KUBECONFIG=$(K3S_KUBECONFIG) kubectl get nodes >/dev/null 2>&1; do sleep 2; done
	$(MAKE) install-operator UI_ENABLED=$(UI_ENABLED)

test-install-ui: UI_ENABLED := true
test-install-ui: test-install

# Starts k3d, a Docker-hosted RouterOS VM, the operator, and a RouterOS-backed E2E suite.
# Requires Docker, k3d, kubectl, Helm, and Go. k3d runs K3s inside Docker.
E2E_ROUTER_IMAGE ?= evilfreelancer/docker-routeros:7.21.5
E2E_OPERATOR_IMAGE ?= mikrotik-operator:e2e
e2e-test:
	E2E_ROUTER_IMAGE=$(E2E_ROUTER_IMAGE) E2E_OPERATOR_IMAGE=$(E2E_OPERATOR_IMAGE) bash hack/e2e/run.sh
