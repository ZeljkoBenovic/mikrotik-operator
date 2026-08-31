---
layout: default
title: Troubleshooting
---

# Troubleshooting

Use resource conditions first, then operator logs. Every custom resource has a
single `Ready` condition. Failure reasons are `ConnectionFailed` on routers and
`ApplyFailed` on managed objects. The `message` is the last reconcile error.

```sh
kubectl get mikrotikrouters,mikrotikdnsrecords,mikrotikroutes,mikrotikportforwards,mikrotikfirewallrules -A
kubectl describe mikrotikrouter -n mikrotik-system home-router
kubectl -n mikrotik-operator-system logs -l app.kubernetes.io/name=mikrotik-operator
```

Helm installs two replicas with leader election. Only the elected manager
reconciles RouterOS. Check which replica holds the lease if logs look idle:

```sh
kubectl -n mikrotik-operator-system get lease mikrotik-operator -o yaml
```

## Problem: implicit router selection is invalid

**Symptoms:**

- `Ready=False` with `multiple MikroTikRouters exist; set routerRef explicitly`
- The same message scoped to one namespace

**Cause:** The operator only auto-selects a router when exactly one
non-deleting `MikroTikRouter` exists in the resource namespace, or, if that
namespace has none, exactly one non-deleting router in the cluster. Connected
status does not change that count.

**Solution:**

1. Set `spec.routerRef` on custom resources, or
   `mikrotik.operator.io/router-ref` on Services, Ingresses, and HTTPRoutes.
2. Use `name` for a router in the same namespace, or `namespace/name` for a
   router in another namespace.
3. A name-only value can also match a unique cluster-wide router with that
   name. Prefer `namespace/name` when more than one namespace has routers.

**Verification:** `Ready=True` and `status.routerRef` shows the selected
router.

## Problem: router stays disconnected

**Symptoms:**

- `status.connected=false`
- `Ready=False`, reason `ConnectionFailed`

**Cause:** The operator cannot dial or log in to the RouterOS API. Default
ports are `8728` without TLS and `8729` with `spec.tls: true`. TLS uses the
Go default certificate verification; a self-signed RouterOS certificate fails
unless the cluster trusts that CA.

**Solution:**

1. Confirm the Secret is in the **router namespace** and has `username` and
   `password` keys.
2. From a debug Pod in the operator namespace, check TCP reachability to
   `address:port`.
3. Match `spec.tls` to the API service you enabled (`/ip service` `api` vs
   `api-ssl`).
4. For TLS, install a certificate the operator can verify, or use the
   non-TLS API on a trusted network. `spec.address` must match a name or IP
   on that certificate.

**Verification:** `kubectl get mikrotikrouter` shows `Connected=true`.

## Problem: ClusterIP routes or NodePort NAT never appear

**Symptoms:**

- DNS record exists but no `/ip route` `/32` entry
- `ApplyFailed` mentioning `no node InternalIP` or `service is not addressable`

**Cause:** ClusterIP `/32` routes and NodePort NAT targets use each node's
`InternalIP`. Headless Services and Services without a ClusterIP are skipped.
SCTP and other non-TCP/UDP ports are not forwarded.

**Solution:** Confirm nodes have `InternalIP` addresses (`kubectl get nodes
-o wide`). Use a ClusterIP Service for in-cluster routes, or a NodePort
Service for node-IP NAT.

**Verification:** The owned `MikroTikRoute` or `MikroTikPortForward` is
`Ready=True`.

## Problem: two routers claim the same device

**Symptoms:**

- `ConnectionFailed` mentioning an endpoint `is owned by` another
  `MikroTikRouter`
- Duplicate endpoint errors on a single router object

**Cause:** Endpoint identity is `address|port|tls`. Two `MikroTikRouter`
objects cannot manage the same physical API endpoint. Credential rotation or
renaming an endpoint does not count as a new device.

**Solution:** Keep one router object per device. Split HA pairs with different
addresses under `spec.routers`.

## Problem: Ingress or HTTPRoute creates nothing

**Symptoms:**

- No owned `MikroTikDNSRecord` or `MikroTikPortForward` children
- Changing `public-ip` has no effect

**Cause:** Ingresses must set `spec.ingressClassName: mikrotik` and the
cluster `IngressClass` controller must be `mikrotik.operator.io/controller`.
HTTPRoutes are ignored unless Helm `gatewayAPI.enabled` is true, the parent
Gateway uses the configured GatewayClass, and a listener accepts the route
(HTTP or HTTPS protocol, hostname intersection, and `allowedRoutes`
namespace policy). Cross-namespace Service backends also need a Gateway API
`ReferenceGrant`.

**Solution:**

1. `kubectl get ingressclass mikrotik`
2. For Gateway API, confirm `gatewayAPI.enabled=true` and install the
   Gateway API CRDs separately.
3. Put HTTPRoute hostnames on the route or the Gateway listener.
4. Headless Services (`clusterIP: None`) and non-TCP/UDP ports are skipped.

## Problem: admin UI rejects edit or delete

**Symptoms:**

- HTTP 409 with a `managedBy` object
- Edit controls disabled and a "Managed by" banner

**Cause:** The object is owned by a Service, Ingress, or HTTPRoute. The UI
refuses mutations so generated children stay in sync with the parent.

**Solution:** Change the owning Kubernetes resource, not the child CR.

## Problem: k3s crash-loops on WSL2

**Symptoms:**

- Kubelet fatal `/proc/mounts` parse error mentioning `/Docker/host`

**Cause:** Docker Desktop on WSL2 exposes a 9p mount whose options contain an
unescaped space.

**Solution:** `make k3s-install` applies `hack/k3s-wsl-docker-desktop.sh`,
which hides that mount from k3s only. `make e2e-test` uses k3d and is
unaffected.

## Problem: chart install pulls the wrong image

**Symptoms:**

- Operator Pods run an unexpected tag after `helm upgrade`

**Cause:** Chart package version and image tags are independent. An empty
`image.tag` uses `Chart.yaml` `appVersion`.

**Solution:** Pin `--set image.tag=v0.2.0` (and `ui.image.tag` when the UI is
enabled), or set `image.digest`. See [Install and configure](getting-started.md).

**Verification:** `kubectl -n mikrotik-operator-system get pods -o jsonpath='{.items[*].spec.containers[*].image}'` shows the pinned tag or digest.
