#!/usr/bin/env bash
# Per-boot startup for the MikroTik operator Cloud Agent environment.
#
# Starts the Docker daemon (used for building the operator and admin UI
# container images). Docker is optional for most development tasks — Go builds,
# tests, linting, Helm validation and the envtest-based app run do not need it —
# so this script never blocks environment startup: it always exits 0.
set -uo pipefail

if command -v docker >/dev/null 2>&1; then
  if ! docker info >/dev/null 2>&1; then
    sudo sh -c 'nohup dockerd >/var/log/dockerd.log 2>&1 &' || true
    for _ in $(seq 1 30); do
      [ -S /var/run/docker.sock ] && break
      sleep 1
    done
    # Allow the agent user to reach the daemon without re-login for group
    # membership to take effect.
    sudo chmod 666 /var/run/docker.sock 2>/dev/null || true
  fi
fi

exit 0
