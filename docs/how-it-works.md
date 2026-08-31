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
3. Workload and annotation controllers calculate desired child custom
   resources (DNS, Route, PortForward) and apply only the CR difference.
4. Each CR reconciler reads RouterOS entries carrying the operator's managed
   comment and applies only missing or changed entries.
5. It records observed state in the resource status and retries transient
   failures. Successful applies requeue after about one minute for drift
   repair; connection and apply failures requeue after one minute.

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
- `DNSReconciler` manages `MikroTikDNSRecord` resources. Standalone records
  with `spec.serviceRef` also own ClusterIP `MikroTikRoute` children.
- `ServiceDNSReconciler` translates Service annotations into owned
  `MikroTikDNSRecord`, `MikroTikRoute`, and `MikroTikPortForward` resources.
- `IngressReconciler` translates the `mikrotik` IngressClass into owned DNS,
  Route, and PortForward custom resources.
- `HTTPRouteReconciler` handles HTTPRoutes attached to the configured
  MikroTik GatewayClass the same way: it creates child CRs only.
- `RouteReconciler`, `FirewallRuleReconciler`, and `PortForwardReconciler`
  are the only controllers that talk to RouterOS for their kind.

Service, Ingress, HTTPRoute, and annotation controllers must not call the
RouterOS client. They create, update, or delete the corresponding custom
resources; the CR reconciler applies the RouterOS change.

## Ownership and cleanup

Kubernetes-generated child resources use owner references. RouterOS entries
use comments derived from resource kind, namespace, and name. Finalizers keep
external cleanup tied to Kubernetes deletion. If a router object disappears
before cleanup, the operator cannot reconnect to that device and allows the
Kubernetes resource to finish deletion rather than leaving a permanent
finalizer block.

## Adding a feature

For a new managed RouterOS capability, add a CRD and a reconciler that talks
to RouterOS. Workload and annotation controllers must create that CR rather
than calling the RouterOS client. Update the API type and deepcopy code,
CRDs, RouterOS client interface and implementation, controller setup/RBAC,
Helm and raw manifests if needed, examples, documentation, and tests. Keep
the desired-state operation idempotent and add a stable managed-comment
namespace before implementing reconciliation. Copy CRD YAML to both
`config/crd/bases` and `charts/mikrotik-operator/crds`.
