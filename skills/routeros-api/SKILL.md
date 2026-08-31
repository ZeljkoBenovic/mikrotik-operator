---
name: routeros-api
description: RouterOS API and configuration guidance for this operator. Use when changing the RouterOS client, DNS, routes, NAT, firewall rules, managed comments, or RouterOS integration tests. Covers RouterOS v6 and v7; verify version-specific behavior against both versions.
version: 1.0.0
author: mikrotik-operator maintainers, informed by tikoci/routeros-skills
license: MIT
tags: [mikrotik, routeros, api, firewall, nat, dns, routing, operator]
---

# RouterOS API guidance for this operator

This project uses the RouterOS native API through `go-routeros`. It is not a
Linux shell integration. RouterOS has its own CLI, command tree, object IDs,
property names, and version-specific behavior.

This skill is a curated adaptation of the most relevant guidance from
[`tikoci/routeros-skills`](https://github.com/tikoci/routeros-skills),
especially `routeros-fundamentals`, `routeros-firewall`,
`routeros-scripting`, and `routeros-command-tree`. The upstream skill set is
focused on RouterOS v7; this project also supports v6, so verify any v7-only
feature before using it.

## API and session rules

- Use the reconcile `context.Context` for dialing and every RouterOS command.
- Use TLS port `8729` when TLS is enabled and port `8728` otherwise unless the
  user explicitly configures another port.
- Keep RouterOS connections short-lived per reconcile and always close them,
  including error and deletion paths.
- Prefer native API command paths such as `/ip/dns/static/print`,
  `/ip/route/print`, `/ip/firewall/nat/print`, and `/ip/firewall/filter/print`.
- Use `.proplist` to request only fields needed for comparison.
- RouterOS values are strings on the wire. Compare normalized values and
  account for omitted/default properties.
- Do not use interactive `print` row numbers as stable identifiers. Use the
  returned `.id` only for the current operation, or locate entries by the
  operator-managed `comment`.

## Idempotent managed configuration

Every entry created by this operator must carry a stable managed comment. Read
only entries matching the exact resource comment or its intended managed
prefix. Never modify or remove a RouterOS entry merely because its fields look
similar.

Use this reconciliation pattern:

1. Print entries with a narrow comment selector and required properties.
2. If exactly one entry matches the desired state, do nothing.
3. If an owned entry is missing or differs, remove the owned entry and add the
   desired entry.
4. If multiple owned entries exist, remove all owned duplicates before adding
   one canonical entry.
5. Treat command errors as reconciliation errors; do not report success.

Deletion must use the same stable comment as creation. Never delete a
RouterOS entry by destination address, port, or another user-controlled field
alone.

## Firewall and NAT behavior

RouterOS firewall filters are evaluated top-to-bottom. The API `place-before`
argument is an item `.id` in the same chain, not a print row number. When that
chain is empty, omit `place-before`; otherwise insert before the first printed
`.id` in the chain so the rule is at the top. An `accept` rule below a broad
`drop` rule may never be reached. When a managed rule requests `PlaceBefore`,
reconciliation must also detect if an existing matching rule was manually moved
and restore its order.

For port forwarding:

- `dst-nat` must target the discovered Service, NodePort node address, or Pod
  address and target port.
- The matching `src-nat` masquerade rule must use the same internal destination
  address when required by the cluster topology.
- The forward firewall rule must be restricted to the internal target address
  and inserted before rules that could drop the traffic.
- Create one rule set per selected Service port and protocol; do not expose
  unrelated Service ports.
- Preserve explicit public IPs exactly after validating that they are IP
  addresses.
- Optional `dst-address` on dst-nat (the IP that initially receives the traffic)
  comes from `MikroTikPortForward` `spec.destinationAddress`, falling back to
  the `public-ip` annotation. Omit both to match any destination.

## Routing and DNS behavior

- ClusterIP routes are single-host routes (`<clusterIP>/32`), not the whole
  Service CIDR. Annotation, Ingress, and HTTPRoute controllers create
  `MikroTikRoute` CRs for those destinations; only `RouteReconciler` calls
  `/ip/route`.
- Multi-node mode may create the same destination through multiple node
  InternalIP gateways for redundancy. Single-node mode intentionally selects
  one gateway.
- NodePort targets use a node InternalIP and the allocated NodePort, not the
  Service ClusterIP and Service port.
- DNS records that reference a Service must follow the current Service address
  and clean up when the Service disappears.
- RouterOS DNS, route, NAT, and filter changes must be independently owned and
  cleaned by their originating custom resource. Do not apply those mutations
  from Service, Ingress, HTTPRoute, or annotation controllers.

## Version and testing discipline

RouterOS v6 and v7 can differ in command properties, defaults, and available
subsystems. For every RouterOS client change:

- verify the command path and property names against both supported versions;
- avoid REST-only or v7-only syntax when the native API supports both;
- add a fake-client unit test for matching, drift, and deletion behavior;
- use a real RouterOS/CHR integration test when command ordering or wire
  behavior is involved;
- document any deliberate version limitation in the API and user docs.

RouterOS is not GNU/Linux: do not propose `bash`, `iptables`, filesystem
paths, package managers, or ordinary Unix commands as RouterOS operations.
