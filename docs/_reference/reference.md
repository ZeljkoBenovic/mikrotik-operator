---
layout: default
title: Reference
---

# Reference

## Annotations

| Annotation | Applies to | Meaning |
| --- | --- | --- |
| `mikrotik.operator.io/dns-name` | Service | Creates or updates an owned `MikroTikDNSRecord`. For ClusterIP Services, also creates owned `MikroTikRoute` objects (`/32` via node InternalIPs). |
| `mikrotik.operator.io/public-ip` | Service, Ingress, HTTPRoute, `MikroTikPortForward` | Creates `dst-nat`/`src-nat` (and a forward filter rule) for selected TCP or UDP ports. The value must be an IP address. On a standalone port-forward CR it is the external address. |
| `mikrotik.operator.io/router-ref` | Service, Ingress, HTTPRoute and custom resources | Selects a `MikroTikRouter` by name in the resource namespace, or as `namespace/name` for a router in another namespace. See [Architecture](architecture.md#router-selection). |
| `mikrotik.operator.io/route-mode` | Service | `all-nodes` (default) or `single-node`. Other values are rejected. |

The operator also writes `mikrotik.operator.io/router-targets` and
`mikrotik.operator.io/service-route-router` for deletion cleanup. Do not use
those as configuration.

## Custom resources

All custom resources use API version `mikrotik.operator.io/v1alpha1`.

### `MikroTikRouter`

Defines RouterOS connectivity. Use `spec.address`, `spec.port`, `spec.tls`, and
`spec.credentialsSecret`, or provide multiple entries under `spec.routers`.
Each endpoint references a Secret in the **same namespace as the router**.
Omitted `port` defaults to `8728`, or `8729` when TLS is enabled.

`spec.routeGateway` (or per-endpoint `routeGateway`) overrides node InternalIP
gateways on generated ClusterIP `/32` routes.

`status.connected` and `status.appliedEndpoints` are observed. `Ready=True`
with reason `Connected` means the API login succeeded.

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
source masquerade, and a forward firewall rule. Set the `public-ip` annotation
to the external address.

### `MikroTikFirewallRule`

Defines a RouterOS filter rule. Specify `chain`, `action`, and any desired
address, port, protocol, state, interface, logging, or `placeBefore` fields.
`placeBefore: true` inserts the rule before the first existing rule in that
chain when the table is not empty.

## Status and conditions

Each CR has one `Ready` condition.

| Kind | Ready reason | Meaning |
| --- | --- | --- |
| `MikroTikRouter` | `Connected` / `ConnectionFailed` | API dial and login |
| Other CRs | `Applied` / `ApplyFailed` | Last RouterOS apply |

`status.applied` is true after a successful apply. `status.routerRef` stores
the selected router as `name` when it is in the resource namespace, otherwise
`namespace/name`.

## Helm values

Chart package version (`Chart.yaml` `version`) is independent of operator
image tags (`appVersion`, `image.tag`, `ui.image.tag`). Empty image tags use
`appVersion`. Pin a tag or digest in production.

| Value | Default | Effect |
| --- | --- | --- |
| `replicaCount` | `2` | Manager replicas; requires `leaderElection.enabled` |
| `leaderElection.enabled` | `true` | Required for more than one replica |
| `gatewayAPI.enabled` | `false` | Watch HTTPRoutes; needs Gateway API CRDs |
| `gatewayAPI.gatewayClass.create` | `false` | Create the `mikrotik` GatewayClass |
| `ui.enabled` | `false` | Admin UI; **no authentication** |
| `image.tag` / `image.digest` | empty / empty | Override `appVersion` |

See [`charts/mikrotik-operator/values.yaml`](https://github.com/ZeljkoBenovic/mikrotik-operator/blob/main/charts/mikrotik-operator/values.yaml)
for the full list. Manager flags are `--metrics-bind-address`,
`--health-probe-bind-address`, `--leader-elect`, `--gateway-api-enabled`,
`--gateway-class-name`, and `--gateway-controller-name`.

## RouterOS ownership

Managed comments use the form:

```text
managed-by=mikrotik-operator/<kind>/<namespace>/<name>
```

The operator queries and mutates only entries with its expected managed
comment. Resource finalizers preserve enough metadata to remove those entries
when Kubernetes resources are deleted.

| Finalizer | Object |
| --- | --- |
| `mikrotik.operator.io/managed-config` | Routers and managed CRs |
| `mikrotik.operator.io/service-route` | Annotated ClusterIP Services that own a `/32` route |
