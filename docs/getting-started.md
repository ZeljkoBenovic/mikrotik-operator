---
layout: default
title: Install and configure
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

The Secret must contain the keys `username` and `password`.

## Install the chart

```sh
helm upgrade --install mikrotik-operator \
  oci://ghcr.io/zeljkobenovic/charts/mikrotik-operator \
  --namespace mikrotik-operator-system \
  --create-namespace
```

For a source checkout, replace the OCI chart reference with
`./charts/mikrotik-operator`. Pin the image tag or digest for production
deployments.

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

The `routerRef` field is optional on managed resources when exactly one router
exists in their namespace. If multiple routers exist, set `routerRef`
explicitly. A router definition can also contain multiple endpoints; all
configured endpoints receive the managed configuration.

## Verify installation

```sh
kubectl -n mikrotik-operator-system get pods
kubectl -n mikrotik-system get mikrotikrouters
```

Check the resource conditions and operator logs if the router is not connected:

```sh
kubectl -n mikrotik-operator-system logs deploy/mikrotik-operator
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
  --namespace mikrotik-operator-system \
  --create-namespace \
  --set ui.enabled=true
```

See [Admin UI](admin-ui.md) for port-forward instructions and the read-only
rule for resources generated from a Service, Ingress, or HTTPRoute.
