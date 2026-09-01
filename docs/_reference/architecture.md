---
layout: default
title: Architecture
nav_order: 2
redirect_from:
  - /architecture.html
---

# Architecture

The operator is a stand-alone Go application built with controller-runtime.
It watches Kubernetes resources, calculates desired RouterOS state, and sends
RouterOS API commands through one or more configured router endpoints.

## Reconciliation flow

1. A controller reads the Kubernetes object and its dependencies.
2. It resolves the target `MikroTikRouter` (see [Router selection](#router-selection)).
3. It calculates the desired DNS, route, NAT, or firewall entries.
4. It reads entries carrying the operator's managed comment.
5. It creates, updates, or removes only entries owned by that resource.
6. It records a `Ready` condition and requeues about once a minute to repair
   drift.

## Router selection

Resolution is the same for custom resources (`spec.routerRef`) and for
Services, Ingresses, and HTTPRoutes (`mikrotik.operator.io/router-ref`).

1. `namespace/name` loads that object and does not fall back.
2. A bare `name` loads `resource-namespace/name` first. If that object is
   missing, a unique non-deleting cluster router with the same name is used.
3. An empty ref prefers the unique non-deleting router in the resource
   namespace. If that namespace has none, it uses the unique non-deleting
   router in the cluster.

"Non-deleting" means `metadata.deletionTimestamp` is empty. A router that
fails to connect still counts, so two routers in a namespace always require
an explicit ref. Ambiguous or empty-cluster cases fail with `implicit router
selection is invalid` instead of picking an arbitrary device.

Credentials stay on the router object. Each endpoint Secret must live in the
**router** namespace, not the workload namespace. Managed resources wait until
the router has its finalizer and has recorded current endpoints in
`status.appliedEndpoints`.

A `MikroTikRouter` may list several endpoints under `spec.routers`. All of
them receive the same desired state. Two router objects cannot share an
endpoint; identity is `address`, port (default `8728`/`8729`), and TLS.

## Ownership and cleanup

Ingresses, HTTPRoutes, and annotated Services own generated child custom
resources through Kubernetes owner references. Those controllers never call
RouterOS themselves: they create `MikroTikDNSRecord`, `MikroTikRoute`, and
`MikroTikPortForward` objects, and the CR controllers apply RouterOS state.
Custom resources use the `mikrotik.operator.io/managed-config` finalizer when
they have external RouterOS state. Older annotated ClusterIP Services may
still carry `mikrotik.operator.io/service-route`; the operator strips that
leftover finalizer after owned route children are deleted.

Router objects retain `status.appliedEndpoints` so managed entries can be
removed during deletion or when an endpoint is dropped. If a referenced
router disappears before cleanup, the operator cannot reconnect and allows
the Kubernetes resource to finish deletion rather than leaving a permanent
finalizer block.

The operator may write `mikrotik.operator.io/router-targets` (and, on
Services, `mikrotik.operator.io/service-route-router`) so cleanup still knows
the last router after a ref change. Do not treat those annotations as user
configuration.

## Multiple routers and nodes

All endpoints in one `MikroTikRouter` definition receive the desired state.
For ClusterIP Services, routes use node InternalIP addresses unless
`spec.routeGateway` or a per-endpoint `routeGateway` overrides them. The
default multi-node behavior provides multiple gateways;
`mikrotik.operator.io/route-mode: single-node` selects one InternalIP.

## Security boundary

Credentials are read from Kubernetes Secrets and are not written into RouterOS
comments or resource status. Use a dedicated RouterOS account, restrict API
network access to the operator, and grant only the policies required by the
features enabled in your cluster.

TLS API connections (`spec.tls: true`) verify certificates with the Go
defaults. The optional admin UI has no authentication; keep it ClusterIP-only
or behind an authenticating proxy.

See [How the operator works]({% link _reference/how-it-works.md %}) for controller names and the
contributor checklist when adding a managed capability.
