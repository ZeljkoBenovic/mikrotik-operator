---
layout: default
title: MikroTik Kubernetes Operator
description: Manage MikroTik RouterOS configuration from Kubernetes.
---

![MikroTik Kubernetes Operator](images/mikrotik-operator-banner.png)

# MikroTik Kubernetes Operator

Manage DNS, routes, port forwarding, and firewall rules on MikroTik RouterOS
from Kubernetes. The operator is designed for local clusters such as k3s,
where a MikroTik router is the network gateway.

It supports RouterOS v6 and v7, uses Kubernetes Secrets for credentials, and
only changes RouterOS entries created and labeled by this operator.

## Start here

- [Install and configure the operator](getting-started.md)
- [Expose a Service, Ingress, or HTTPRoute](how-to-expose-services.md)
- [Optional admin UI](admin-ui.md)
- [Resource and annotation reference](reference.md)
- [Architecture and ownership model](architecture.md)

## What it manages

| Kubernetes resource | RouterOS configuration |
| --- | --- |
| `Service` annotations | Owned DNS, ClusterIP route, and optional port-forward CRs |
| `Ingress` with class `mikrotik` | Owned DNS, route, and optional port-forward CRs |
| `HTTPRoute` attached to the MikroTik GatewayClass | Owned DNS, route, and optional port-forward CRs |
| `MikroTikDNSRecord` | `/ip dns static`; standalone `serviceRef` also owns ClusterIP routes |
| `MikroTikRoute` | `/ip route` |
| `MikroTikPortForward` | `dst-nat`, `src-nat`, and forward firewall rules |
| `MikroTikFirewallRule` | `/ip firewall filter` |

## Safety model

Every generated RouterOS entry has a managed comment containing its Kubernetes
resource identity. Reconciliation is idempotent and periodic drift checks
restore changes made outside Kubernetes. Existing RouterOS entries without the
operator's managed comment are not modified or deleted.

## Project links

- [Source code](https://github.com/ZeljkoBenovic/mikrotik-operator)
- [Container images](https://github.com/ZeljkoBenovic/mikrotik-operator/pkgs/container/mikrotik-operator)
- [Examples](https://github.com/ZeljkoBenovic/mikrotik-operator/tree/main/examples)
- [Issue tracker](https://github.com/ZeljkoBenovic/mikrotik-operator/issues)
