---
layout: home
title: MikroTik Kubernetes Operator
description: Manage MikroTik RouterOS configuration from Kubernetes.
hero:
  name: MikroTik Kubernetes Operator
  text: Manage RouterOS from Kubernetes
  tagline: DNS, routes, port forwarding, and firewall rules on MikroTik RouterOS v6 and v7, from a local cluster such as k3s.
  image:
    src: /images/mikrotik-operator-banner.png
    alt: Kubernetes Operator for MikroTik Routers
    width: 640
    height: 320
  actions:
    - theme: brand
      text: Get started
      link: /getting-started/
    - theme: alt
      text: GitHub
      link: https://github.com/ZeljkoBenovic/mikrotik-operator
features:
  - icon: 📦
    title: Install and configure
    details: Install with Helm and connect a MikroTik RouterOS device.
    link: /getting-started/
    link_text: Installation guide
  - icon: 🌐
    title: Expose Services
    details: Annotate a Service, Ingress, or HTTPRoute for DNS, routes, and NAT.
    link: /how-to-expose-services/
    link_text: Exposure guide
  - icon: 🖥️
    title: Admin UI
    details: Optional panel for listing and creating MikroTik custom resources.
    link: /admin-ui/
    link_text: Admin UI
  - icon: 💾
    title: Backup and restore
    details: Text /export snapshots and confirmed /import onto a router.
    link: /backup-restore/
    link_text: Backup and restore
  - icon: 📘
    title: Reference
    details: Annotations, custom resources, Helm values, and ownership comments.
    link: /reference/
    link_text: Reference
---

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
| `MikroTikBackup` | Text `/export` snapshot or cron policy |
| `MikroTikRestore` | Confirmed `/import` of a stored export |

## Safety model

Every generated RouterOS entry has a managed comment containing its Kubernetes
resource identity. Reconciliation is idempotent and periodic drift checks
restore changes made outside Kubernetes. Existing RouterOS entries without the
operator's managed comment are not modified or deleted by DNS, route, NAT, or
firewall reconcilers. Confirmed restore is the unmanaged-config exception: it
runs `/import` of a stored `/export` and does not wipe the device. See
[Backup and restore]({% link _guide/backup-restore.md %}).

## Project links

- [Source code](https://github.com/ZeljkoBenovic/mikrotik-operator)
- [Container images](https://github.com/ZeljkoBenovic/mikrotik-operator/pkgs/container/mikrotik-operator)
- [Examples](https://github.com/ZeljkoBenovic/mikrotik-operator/tree/main/examples)
- [Issue tracker](https://github.com/ZeljkoBenovic/mikrotik-operator/issues)
