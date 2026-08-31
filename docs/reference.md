---
layout: default
title: Reference
---

# Reference

## Annotations

| Annotation | Applies to | Meaning |
| --- | --- | --- |
| `mikrotik.operator.io/dns-name` | Service | Creates or updates an owned `MikroTikDNSRecord`. For ClusterIP Services, also creates owned `MikroTikRoute` objects (`/32` via node InternalIPs). |
| `mikrotik.operator.io/public-ip` | Service, Ingress, HTTPRoute | Creates one `dst-nat`/`src-nat` pair per selected TCP or UDP Service port. The value must be an IP address. |
| `mikrotik.operator.io/router-ref` | Service, Ingress, HTTPRoute and custom resources | Selects a `MikroTikRouter` by name in the resource namespace, or as `namespace/name` for a router in another namespace. |
| `mikrotik.operator.io/route-mode` | Service | Use `single-node` instead of routing through all node InternalIP addresses. |

## Custom resources

All custom resources use API version `mikrotik.operator.io/v1alpha1`.

### `MikroTikRouter`

Defines RouterOS connectivity. Use `spec.address`, `spec.port`, `spec.tls`, and
`spec.credentialsSecret`, or provide multiple entries under `spec.routers`.
Each endpoint references a Secret in the same namespace.

### `MikroTikDNSRecord`

Defines `spec.name` and `spec.address`, with optional `spec.ttl` and
`spec.serviceRef`. A Service reference makes the address follow the Service.
Standalone records (not owned by a Service, Ingress, or HTTPRoute) also
create owned `MikroTikRoute` children for ClusterIP backends.

### `MikroTikRoute`

Defines a RouterOS route with `spec.destination`, `spec.gateway`, and optional
`spec.distance`.

### `MikroTikPortForward`

Defines `spec.protocol`, `spec.externalPort`, and `spec.targetPort`. Set
`spec.targetAddress` for a direct IP target, or use `serviceRef`/`podRef` to
resolve the target from Kubernetes. The resource creates destination NAT,
source masquerade, and a forward firewall rule.

### `MikroTikFirewallRule`

Defines a RouterOS filter rule. Specify `chain`, `action`, and any desired
address, port, protocol, state, interface, logging, or `placeBefore` fields.

## RouterOS ownership

Managed comments use the form:

```text
managed-by=mikrotik-operator/<kind>/<namespace>/<name>
```

The operator queries and mutates only entries with its expected managed
comment. Resource finalizers preserve enough metadata to remove those entries
when Kubernetes resources are deleted.
