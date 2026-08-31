---
layout: default
title: Backup and restore
nav_order: 4
redirect_from:
  - /backup-restore.html
---

# Backup and restore

The operator stores RouterOS **text `/export`** snapshots in Kubernetes and
can apply them with `/import`. Binary `/system backup save` files are **not**
stored in custom resources. Connection details always come from an existing
`MikroTikRouter`, except restores onto a brand-new device which may use inline
credentials.

There are two custom resources:

| Kind | Purpose |
| --- | --- |
| `MikroTikBackup` | One `/export` snapshot, **or** a cron policy that owns snapshot children |
| `MikroTikRestore` | Apply a stored export after an explicit confirmation |

## Manual backup

Create a `MikroTikBackup` with `spec.routerRef` and **no** `spec.schedule`.
The controller connects through that `MikroTikRouter`, runs `/export compact`
(falling back to `/export`), and writes the script to `status.export`. On
RouterOS v7 the export includes `show-sensitive` so passwords and certificates
are present. RouterOS v6 `/export` already includes secrets; there is no hide
flag.

```yaml
apiVersion: mikrotik.operator.io/v1alpha1
kind: MikroTikBackup
metadata:
  name: edge-now
  namespace: default
spec:
  routerRef: local-router
```

A successful snapshot sets `Ready=True` with reason `Captured`. The same
object is not re-exported unless its generation changes.

## Scheduled backups and retention

Set `spec.schedule` to a standard five-field cron expression. That object
becomes a **policy**. The controller creates child `MikroTikBackup` snapshots
(empty schedule, owner reference) and prunes older **captured** owned
snapshots down to `spec.retention` (default 5) for that policy. Snapshots
with an empty `status.export` do not count toward retention, so a failed
capture cannot delete the last good snapshot. The policy stays `Ready=False`
until the due snapshot has captured, and `LastScheduleTime` advances only
after that capture.

```yaml
apiVersion: mikrotik.operator.io/v1alpha1
kind: MikroTikBackup
metadata:
  name: edge-nightly
  namespace: default
spec:
  routerRef: local-router
  schedule: "0 2 * * *"
  retention: 7
```

You do not create a Kubernetes CronJob. The first snapshot is taken when the
policy is created; later snapshots follow the cron. Deleting the policy
garbage-collects its snapshot children.

Point a restore at a **snapshot** child (`status.role: Snapshot` and a
non-empty `status.export`), not at the policy object.

## Restore confirmation

Kubernetes has no native confirm dialog. Restores stay idle until
`spec.confirm` is exactly `RESTORE`. Any other value, including `true`, is
ignored. The admin UI has a **Confirm restore** dialog that types that field
for you. YAML create/edit in the admin UI drops `spec.confirm` so a paste
cannot authorize `/import`; use the dialog or kubectl.

Until confirmation, `Ready=False` with reason `WaitingForConfirmation`. After
a successful `/import`, the controller records `status.applied: true` and does
not import again unless `backupRef` or the restore target changes. A
generation bump alone does not re-import.

## Restore onto an existing router

```yaml
apiVersion: mikrotik.operator.io/v1alpha1
kind: MikroTikRestore
metadata:
  name: edge-rollback
  namespace: default
spec:
  backupRef:
    name: edge-now
  routerRef: local-router
  confirm: RESTORE
```

`routerRef` does not require the `MikroTikRouter` to already be `Ready`. An
empty `backupRef.namespace` means the Restore's namespace. An explicit
namespace is allowed, matching `routerRef` and other cluster-trust
cross-namespace references in this operator.

## Restore onto a new empty router

Use inline `spec.connection` instead of `routerRef` **only for devices that
do not have a Router CR**. If the address matches an existing
`MikroTikRouter` endpoint, restore uses that router's operation fence. The
Secret must live in the Restore namespace and contain `username` and
`password`. You cannot set both `routerRef` and `connection`.

```yaml
apiVersion: mikrotik.operator.io/v1alpha1
kind: MikroTikRestore
metadata:
  name: spare-router
  namespace: default
spec:
  backupRef:
    name: edge-now
  connection:
    address: 192.168.88.1
    port: 8729
    tls: true
    credentialsSecret:
      name: spare-credentials
  confirm: RESTORE
```

## Blast radius

Restore runs `/import` of the stored `/export`. It does **not** wipe the
device and does **not** run `/system reset-configuration`. `/import` is not
limited to operator-managed comments. Existing objects on the router may
cause `already have such` errors. Use restore on an empty or unconfigured
router when you need a clean apply. Unmanaged firewall rules, users, and
other RouterOS objects in the export are applied. Use a dedicated Restore
object and confirmation for every apply.

On a `MikroTikRouter` with several endpoints, restore uses the first
connection. Multi-endpoint routers are assumed to share configuration.

## Transport and RouterOS versions

Backup uses `/export compact`, then `/export`. Restore writes the script to a
temporary file with `/file print file=` and `/file set contents=`, then runs
`/import file-name=`. RouterOS only accepts `contents=` for small files
(about 4KB on v6, about 60KB on v7), so the client splits a stored `/export`
on statement boundaries into chunks under the v6 cap, repeats the current CLI
path at the start of each chunk, and imports them in order. That path is used
on both v6 and v7. `/system/script` is not used; its source size is far below
the 1MiB snapshot cap. `/import` of a large script can take tens of seconds;
each RouterOS call allows 60s. A single restore statement larger than the v6
`contents=` cap cannot be uploaded this way and fails.

## Secrets in `status.export`

`status.export` can contain RouterOS user passwords, certificates, and other
secrets. It is stored in etcd on the snapshot CR, covered by Kubernetes RBAC,
and returned by the admin UI **GET-by-name** API. List and overview responses
omit the export body. Restrict who can `get` `MikroTikBackup` objects.

## etcd size

The export body is stored on the snapshot CR. Kubernetes objects should stay
under about 1.5MiB. The operator refuses exports larger than 1MiB (`status.export`
maxLength 1048576) and warns from 512KiB in `status.exportWarning`. Huge
RouterOS configs are not a good fit for etcd-backed snapshots.

## Remote storage (not implemented)

`spec.remote` is reserved for a later release that would write both the text
`/export` and a binary `/system backup save` file to FTP, SMB, or S3.
`spec.remote.enabled` must stay `false`. Enabling it is rejected by the CRD
and, if it reaches the controller, sets `Ready=False` with
`RemoteStorageNotImplemented`.
