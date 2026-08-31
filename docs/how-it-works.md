---
layout: default
title: How the operator works
---

# How the operator works

The operator watches Kubernetes resources and translates their desired state
into RouterOS API operations. Each `MikroTikRouter` supplies one or more
RouterOS endpoints and references a Kubernetes Secret containing `username`
and `password` values. Secrets are read from the router object's namespace.

## Reconciliation flow

1. A controller reads the Kubernetes resource and its dependencies.
2. The controller resolves the target router. A unique non-deleting router in
   the resource namespace is preferred. If that namespace has none, a unique
   non-deleting router anywhere in the cluster is selected. Ambiguous cases
   require an explicit `routerRef` or `mikrotik.operator.io/router-ref` value
   (`name` or `namespace/name`). A name-only ref is tried locally first, then
   as a unique cluster-wide name match.
3. It calculates the desired DNS, route, NAT, or firewall entries.
4. The RouterOS client reads entries carrying the operator's managed comment.
5. It applies only missing or changed entries and removes stale entries owned
   by that resource.
6. The controller records observed state in the resource status and retries
   transient failures. Successful applies requeue after about one minute for
   drift repair; connection and apply failures requeue after one minute.

Existing RouterOS entries without the operator's managed comment are never
modified or deleted.

Managed writes wait until `ensureRouterActive` succeeds: the router is not
deleting, has the `mikrotik.operator.io/managed-config` finalizer, has
durable `status.appliedEndpoints` matching the current spec, and uniquely
owns those endpoints. Endpoint identity is address, port, and TLS — not the
endpoint display name or Secret name — so credential rotation does not wipe
managed rules.

In-process fences serialize RouterOS operations per router. The Helm chart
enables leader election (`LeaderElectionID` `mikrotik-operator`); multiple
manager replicas without leader election are not supported.

## Controllers

- `RouterReconciler` validates RouterOS connectivity and tracks applied
  endpoints.
- `DNSReconciler` manages `MikroTikDNSRecord` resources and Service routes.
- `ServiceDNSReconciler` translates Service annotations into DNS records and
  port-forward resources.
- `IngressReconciler` translates the `mikrotik` IngressClass into DNS, routes,
  and optional port forwards.
- `HTTPRouteReconciler` handles HTTPRoutes attached to the configured
  MikroTik GatewayClass.
- `RouteReconciler`, `FirewallRuleReconciler`, and `PortForwardReconciler`
  manage their corresponding custom resources.

## Ownership and cleanup

Kubernetes-generated child resources use owner references. RouterOS entries
use comments derived from resource kind, namespace, and name. Finalizers keep
external cleanup tied to Kubernetes deletion. If a router object disappears
before cleanup, the operator cannot reconnect to that device and allows the
Kubernetes resource to finish deletion rather than leaving a permanent
finalizer block.

## Adding a feature

For a new managed RouterOS capability, update the API type and deepcopy code,
CRDs, RouterOS client interface and implementation, controller setup/RBAC,
Helm and raw manifests if needed, examples, documentation, and tests. Keep
the desired-state operation idempotent and add a stable managed-comment
namespace before implementing reconciliation. Copy CRD YAML to both
`config/crd/bases` and `charts/mikrotik-operator/crds`.
