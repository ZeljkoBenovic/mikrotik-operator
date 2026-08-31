#!/usr/bin/env bash
set -Eeuo pipefail

CLUSTER_NAME="${E2E_UI_CLUSTER_NAME:-mikrotik-e2e-ui}"
UI_IMAGE="${E2E_UI_IMAGE:-mikrotik-operator-ui:e2e}"
NAMESPACE="${E2E_UI_NAMESPACE:-mikrotik-operator-system}"
WORKLOAD_NS="${E2E_UI_WORKLOAD_NS:-e2e-ui}"
LOCAL_PORT="${E2E_UI_PORT:-18080}"
KUBECONFIG_FILE="$(mktemp)"
pf_pid=""

cleanup() {
  status=$?
  set +e
  if [ -n "${pf_pid:-}" ]; then
    kill "$pf_pid" >/dev/null 2>&1
    wait "$pf_pid" >/dev/null 2>&1
  fi
  if [ "$status" -ne 0 ]; then
    echo "=== UI e2e failure diagnostics (exit ${status}) ===" >&2
    if [ -n "${KUBECONFIG:-}" ]; then
      kubectl -n "$NAMESPACE" get deploy,svc,pods -l app.kubernetes.io/component=ui -o wide >&2 || true
      kubectl -n "$NAMESPACE" logs -l app.kubernetes.io/component=ui --tail=120 >&2 || true
      kubectl -n "$WORKLOAD_NS" get svc,mikrotikrouter,mikrotikdnsrecord,mikrotikroute,mikrotikportforward,mikrotikfirewallrule >&2 || true
    fi
  fi
  if [ "${E2E_KEEP_ON_FAILURE:-}" != "1" ] || [ "$status" -eq 0 ]; then
    k3d cluster delete "$CLUSTER_NAME" >/dev/null 2>&1
  else
    echo "keeping k3d cluster ${CLUSTER_NAME} for investigation" >&2
  fi
  rm -f "$KUBECONFIG_FILE"
}
trap cleanup EXIT

for command in docker k3d kubectl helm go; do
  command -v "$command" >/dev/null 2>&1 || { echo "missing required command: $command" >&2; exit 1; }
done

k3d cluster delete "$CLUSTER_NAME" >/dev/null 2>&1 || true
k3d cluster create "$CLUSTER_NAME" --servers 1 --agents 0 --wait --timeout 180s \
  --k3s-arg '--disable=traefik@server:*' >/dev/null
k3d kubeconfig get "$CLUSTER_NAME" >"$KUBECONFIG_FILE"
export KUBECONFIG="$KUBECONFIG_FILE"

docker build -f Dockerfile.ui -t "$UI_IMAGE" .
k3d image import "$UI_IMAGE" --cluster "$CLUSTER_NAME"

helm upgrade --install mikrotik-operator ./charts/mikrotik-operator \
  --namespace "$NAMESPACE" --create-namespace \
  --set replicaCount=0 \
  --set leaderElection.enabled=false \
  --set podDisruptionBudget.enabled=false \
  --set ui.enabled=true \
  --set ui.replicaCount=1 \
  --set ui.image.repository=mikrotik-operator-ui \
  --set ui.image.tag=e2e \
  --set ui.image.pullPolicy=IfNotPresent

ui_deploy="$(kubectl -n "$NAMESPACE" get deployment -l app.kubernetes.io/component=ui -o name | head -n 1)"
test -n "$ui_deploy"
kubectl -n "$NAMESPACE" rollout status "$ui_deploy" --timeout=180s

kubectl create namespace "$WORKLOAD_NS" --dry-run=client -o yaml | kubectl apply -f -
# Create the controller owner first. A DNS record with a dangling owner UID is
# garbage-collected almost immediately, which made GET owned-dns a flake.
kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: router-creds
  namespace: ${WORKLOAD_NS}
type: Opaque
stringData:
  username: admin
  password: super-secret-e2e
---
apiVersion: v1
kind: Service
metadata:
  name: web
  namespace: ${WORKLOAD_NS}
spec:
  ports:
  - name: http
    port: 80
    targetPort: 80
EOF
# Kubernetes garbage-collects objects whose controller owner is missing.
# Create Service/web first and reuse its UID so owned-dns is not orphaned.
web_uid="$(kubectl -n "$WORKLOAD_NS" get svc web -o jsonpath='{.metadata.uid}')"
test -n "$web_uid"

kubectl apply -f - <<EOF
apiVersion: mikrotik.operator.io/v1alpha1
kind: MikroTikDNSRecord
metadata:
  name: owned-dns
  namespace: ${WORKLOAD_NS}
  ownerReferences:
  - apiVersion: v1
    kind: Service
    name: web
    uid: ${web_uid}
    controller: true
spec:
  name: owned.e2e.home.arpa
  address: 10.99.0.8
EOF
kubectl -n "$WORKLOAD_NS" get mikrotikdnsrecord owned-dns >/dev/null

ui_svc="$(kubectl -n "$NAMESPACE" get svc -l app.kubernetes.io/component=ui -o jsonpath='{.items[0].metadata.name}')"
test -n "$ui_svc"
kubectl -n "$NAMESPACE" port-forward "svc/${ui_svc}" "${LOCAL_PORT}:8080" >/dev/null 2>&1 &
pf_pid=$!

echo "Running Admin UI HTTP verification..."
E2E_UI_BASE_URL="http://127.0.0.1:${LOCAL_PORT}" \
  E2E_UI_NAMESPACE="$WORKLOAD_NS" \
  E2E_UI_OPERATOR_NAMESPACE="$NAMESPACE" \
  go run -buildvcs=false ./hack/e2e-ui
