---
layout: default
title: Install and configure
nav_order: 1
redirect_from:
  - /getting-started.html
---

# Install and configure

This guide installs the operator with Helm and connects it to a MikroTik
RouterOS device.

## Prerequisites

- Kubernetes 1.31 or newer
- Helm 3
- RouterOS v6 or v7 with the API service reachable from the operator Pod
- A RouterOS account with the minimum API permissions needed for the features
  you use

The API uses TCP port `8728` without TLS or `8729` with TLS.

## Run the complete local E2E test

The repository includes a disposable RouterOS-backed test environment. It uses
k3d (K3s in Docker) and the
[`docker-routeros`](https://github.com/EvilFreelancer/docker-routeros) QEMU
image running as a Docker container with its API published onto the k3d
network. Install Docker, k3d, kubectl, Helm, Go, and a Linux shell such as WSL,
then run:

```sh
make e2e-test
```

The test creates and removes its own K3s cluster, Docker network, and RouterOS
container. It validates Kubernetes-generated DNS, routes, destination NAT,
source masquerade, and firewall rules directly against RouterOS. The default
test image uses the RouterOS `admin` account with an empty password.

## Create credentials

Create a Secret in the namespace where the `MikroTikRouter` will live. Keep
real credentials out of source control.

```sh
kubectl create namespace mikrotik-system
kubectl create secret generic mikrotik-credentials \
  --namespace mikrotik-system \
  --from-literal=username=operator \
  --from-literal=password='replace-me'
```

The Secret must contain the keys `username` and `password`, and it must be in
the same namespace as the `MikroTikRouter`. Workloads in other namespaces can
reference that router; they do not need a copy of the Secret.

## Install the chart

```sh
helm upgrade --install mikrotik-operator \
  oci://ghcr.io/zeljkobenovic/charts/mikrotik-operator \
  --version 0.3.0 \
  --namespace mikrotik-operator-system \
  --create-namespace
```

Chart versions and operator image tags are independent. The chart's
`appVersion` is the default image tag when `image.tag` is empty. For a source
checkout, replace the OCI chart reference with `./charts/mikrotik-operator`.
Pin the image tag or digest for production deployments.

## Define a router

```yaml
apiVersion: mikrotik.operator.io/v1alpha1
kind: MikroTikRouter
metadata:
  name: home-router
  namespace: mikrotik-system
spec:
  address: 192.168.88.1
  port: 8729
  tls: true
  credentialsSecret:
    name: mikrotik-credentials
```

```sh
kubectl apply -f router.yaml
kubectl -n mikrotik-system get mikrotikrouter home-router
```

The `routerRef` field is optional on managed resources when exactly one
non-deleting router exists in their namespace, or when that namespace has none
and exactly one non-deleting router exists in the cluster. "Live" here means
the object is not deleting; a disconnected router still counts. If more than
one router exists, set `routerRef` (or the `mikrotik.operator.io/router-ref`
annotation) to the router name or to `namespace/name`.

A router definition can also contain multiple endpoints under `spec.routers`;
all configured endpoints receive the managed configuration. Optional
`spec.routeGateway` overrides node InternalIP gateways on generated ClusterIP
routes.

Omitted `spec.port` is `8728`, or `8729` when `spec.tls` is true. TLS
connections verify certificates with the Go defaults, so a self-signed
RouterOS API certificate will not connect until the cluster trusts that CA.

## Verify installation

```sh
kubectl -n mikrotik-operator-system get pods
kubectl -n mikrotik-system get mikrotikrouters
```

Check the resource `Ready` condition and operator logs if the router is not
connected. See [Troubleshooting]({% link _guide/troubleshooting.md %}) for TLS, Secret
namespace, and ambiguous-router failures.

```sh
kubectl -n mikrotik-operator-system logs -l app.kubernetes.io/name=mikrotik-operator
```

## Optional admin UI

The chart can deploy a browser panel for the MikroTik custom resources. It is
off by default and has **no authentication**. Enable it only on a trusted
network or behind an authenticating proxy.

On a Linux host, install K3s and the chart with the UI enabled:

```sh
make test-install-ui
```

Or upgrade an existing chart install:

```sh
helm upgrade --install mikrotik-operator \
  oci://ghcr.io/zeljkobenovic/charts/mikrotik-operator \
  --version 0.3.0 \
  --namespace mikrotik-operator-system \
  --create-namespace \
  --set ui.enabled=true
```

See [Admin UI]({% link _guide/admin-ui.md %}) for port-forward instructions and the read-only
rule for resources generated from a Service, Ingress, or HTTPRoute.

## Chart and image versions

Helm chart package versions and operator image tags are independent. Chart
`0.3.0` sets `appVersion` to `v0.3.0`, which is the image tag when
`image.tag` is empty. Pin both when they must not drift:

```sh
helm upgrade --install mikrotik-operator \
  oci://ghcr.io/zeljkobenovic/charts/mikrotik-operator \
  --version 0.3.0 \
  --namespace mikrotik-operator-system \
  --create-namespace \
  --set image.tag=v0.3.0
```

Images are published from trusted `vMAJOR.MINOR.PATCH` git tags. Chart
packages are published from `main` when `charts/mikrotik-operator/Chart.yaml`
`version` changes. Re-pushing an existing chart version is skipped.
