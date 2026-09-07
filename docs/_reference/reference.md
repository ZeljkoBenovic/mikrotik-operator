---
layout: default
title: Reference
nav_order: 1
redirect_from:
  - /reference.html
---

# Reference

## Annotations

| Annotation | Applies to | Meaning |
| --- | --- | --- |
| `mikrotik.operator.io/dns-name` | Service | Creates or updates an owned `MikroTikDNSRecord`. For ClusterIP Services, also creates owned `MikroTikRoute` objects (`/32` via node InternalIPs). |
| `mikrotik.operator.io/public-ip` | Service, Ingress, HTTPRoute, `MikroTikPortForward` | Creates `dst-nat`/`src-nat` (and a forward filter rule) for selected TCP or UDP ports. The value must be an IP address. On a standalone port-forward CR it is the fallback for `spec.destinationAddress`. |
| `mikrotik.operator.io/router-ref` | Service, Ingress, HTTPRoute and custom resources | Selects a `MikroTikRouter` by name in the resource namespace, or as `namespace/name` for a router in another namespace. See [Architecture]({% link _reference/architecture.md %}#router-selection). |
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
source masquerade, and a forward firewall rule. Optional
`spec.destinationAddress` sets RouterOS `dst-address` on the dst-nat rule so
the match applies only to traffic initially received on that IP. Omit it to
match any destination. The `public-ip` annotation is still honored when
`spec.destinationAddress` is empty, and generated children copy the parent
annotation into both places.

### `MikroTikFirewallRule`

Defines a RouterOS filter rule. Specify `chain`, `action`, and any desired
address, port, protocol, state, interface, logging, or `placeBefore` fields.
`placeBefore: true` inserts the rule before the first existing rule in that
chain when the table is not empty.

### Backup and restore API preview

Chart `0.5.0` installs these CRDs. The current operator image (`appVersion`
`v0.4.0`) does not watch or reconcile them. `controller.Setup` registers
router, DNS, Service, Ingress, HTTPRoute, route, firewall, and port-forward
reconcilers only. ClusterRole rules also omit `mikrotikbackups` and
`mikrotikrestores`. Applying either kind stores the object in etcd and does
not call RouterOS. The admin UI allowlist is the five reconciled kinds.

The published `v1alpha1` schema is:

| Kind | Required spec | Intended behavior (not reconciled yet) |
| --- | --- | --- |
| `MikroTikBackup` | `routerRef` | Empty `schedule` is a one-shot `/export` into `status.export`. A cron `schedule` is a policy that would own snapshot children. `retention` defaults to 5 (max 100). |
| `MikroTikRestore` | `backupRef` | Applies a stored export with `/import` only when `confirm` is exactly `RESTORE`. Set exactly one of `routerRef` or `connection.address` plus `connection.credentialsSecret`. |

Constraints already enforced by the CRD:

- `spec.remote.enabled` must be false. Remote FTP/SMB/S3 storage is reserved
  and CEL rejects enabling it.
- Restore CEL requires exactly one target: `routerRef`, or inline
  `connection.address` with `credentialsSecret.name`.
- `status.export` is capped at 1,048,576 bytes. The type comments say an
  export may contain RouterOS passwords and certificates.

Neither kind uses a deletion finalizer. The type comments say there is no
RouterOS object to finalize for a backup, and deleting a restore must not
undo device changes.

The RouterOS client already implements `Export` and `Import` in
`internal/routeros/export.go` for a future reconciler. `/export` prefers
compact output and adds `show-sensitive` on RouterOS v7+. `/import` writes
`mikrotik-operator-restore.rsc` in chunks of at most 4095 bytes (the v6
`/file/set contents=` limit) and uses a 60s timeout instead of the usual 15s
operation timeout.

## Status and conditions

Each reconciled CR has one `Ready` condition. Preview Backup and Restore
objects never receive one.

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
