#!/usr/bin/env bash
# Docker Desktop on WSL2 mounts C:\Program Files\... at /Docker/host with an
# unescaped space in /proc/mounts. Kubelet requires exactly 6 fields per line
# and exits: "system validation failed - wrong number of fields (expected 6, got 7)".
#
# systemd PrivateMounts + ExecStartPre umount is not enough: the pre-start
# umount runs in the host namespace, Docker remounts the path, and kubelet
# still sees the bad line. Run k3s inside unshare --mount and unmount there.
set -Eeuo pipefail

is_wsl() {
  grep -qi microsoft /proc/version 2>/dev/null || grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null
}

has_malformed_proc_mounts() {
  awk 'NF != 6 { found = 1 } END { exit !found }' /proc/mounts
}

if ! is_wsl || ! has_malformed_proc_mounts; then
  exit 0
fi

if [ "$(id -u)" -ne 0 ]; then
  exec sudo --preserve-env=PATH "$0" "$@"
fi

wrap=/usr/local/sbin/k3s-wsl-wrap
cat >"$wrap" <<'EOF'
#!/bin/sh
exec unshare --mount /bin/sh -c 'umount -l /Docker/host >/dev/null 2>&1 || true; exec /usr/local/bin/k3s "$@"' sh "$@"
EOF
chmod 755 "$wrap"

drop_in_dir=/etc/systemd/system/k3s.service.d
mkdir -p "$drop_in_dir"

argv=$(systemctl show k3s -p ExecStart --value | sed -n 's/.*argv\[\]=\([^;]*\).*/\1/p')
args=${argv#/usr/local/bin/k3s }
args=${args#/usr/local/sbin/k3s-wsl-wrap }
if [ -z "$args" ]; then
  args="server"
fi

cat >"${drop_in_dir}/wsl-docker-desktop.conf" <<EOF
[Service]
PrivateMounts=no
ExecStart=
ExecStart=${wrap} ${args}
EOF

systemctl daemon-reload
systemctl reset-failed k3s.service >/dev/null 2>&1 || true
if systemctl list-unit-files k3s.service >/dev/null 2>&1; then
  systemctl restart k3s.service
fi
echo "applied WSL Docker Desktop kubelet mount workaround (unshare --mount)" >&2
