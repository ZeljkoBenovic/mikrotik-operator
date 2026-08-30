# How the operator works

The operator watches Kubernetes resources and translates their desired state
into RouterOS API operations. Each `MikroTikRouter` supplies one or more
RouterOS endpoints and references a Kubernetes Secret containing `username`
and `password` values.

## Reconciliation flow

1. A controller reads the Kubernetes resource and its dependencies.
2. The controller resolves the target router or routers.
3. It calculates the desired DNS, route, NAT, or firewall entries.
4. The RouterOS client reads entries carrying the operator's managed comment.
5. It applies only missing or changed entries and removes stale entries owned
   by that resource.
6. The controller records observed state in the resource status and retries
   transient failures.

Existing RouterOS entries without the operator's managed comment are never
modified or deleted.

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
namespace before implementing reconciliation.
