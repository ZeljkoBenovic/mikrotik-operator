---
layout: default
title: Architecture
---

# Architecture

The operator is a stand-alone Go application built with controller-runtime.
It watches Kubernetes resources, calculates desired RouterOS state, and sends
RouterOS API commands through one or more configured router endpoints.

## Reconciliation flow

1. A controller reads the Kubernetes object and its dependencies.
2. It resolves the target router or routers.
3. It calculates the desired DNS, route, NAT, or firewall entries.
4. It reads entries carrying the operator's managed comment.
5. It creates, updates, or removes only entries owned by that resource.
6. It records status and periodically checks for drift.

## Ownership and cleanup

Ingresses, HTTPRoutes, and annotated Services own generated child custom
resources through Kubernetes owner references. Custom resources use finalizers
when they have external RouterOS state. Router objects retain applied endpoint
metadata so their managed entries can be removed during deletion.

If a referenced Kubernetes dependency disappears, the operator removes the
last known external configuration before reporting the dependency failure.

## Multiple routers and nodes

All endpoints in one `MikroTikRouter` definition receive the desired state.
For ClusterIP Services, routes use node InternalIP addresses. The default
multi-node behavior provides multiple gateways; `single-node` selects one
gateway when that behavior is preferred.

## Security boundary

Credentials are read from Kubernetes Secrets and are not written into RouterOS
comments or resource status. Use a dedicated RouterOS account, restrict API
network access to the operator, and grant only the policies required by the
features enabled in your cluster.
