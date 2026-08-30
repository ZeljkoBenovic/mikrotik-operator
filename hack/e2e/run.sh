#!/usr/bin/env bash
set -Eeuo pipefail

CLUSTER_NAME="${E2E_CLUSTER_NAME:-mikrotik-e2e}"
CLUSTER_NETWORK="k3d-${CLUSTER_NAME}"
ROUTER_IMAGE="${E2E_ROUTER_IMAGE:-evilfreelancer/docker-routeros:7.21.5}"
OPERATOR_IMAGE="${E2E_OPERATOR_IMAGE:-mikrotik-operator:e2e}"
GATEWAY_API_VERSION="${E2E_GATEWAY_API_VERSION:-v1.6.1}"
ROUTER_CONTAINER="${CLUSTER_NAME}-routeros"
ROUTER_HOST_PORT="${E2E_ROUTEROS_HOST_PORT:-18728}"
KUBECONFIG_FILE="$(mktemp)"
FIXTURE_FILE="$(mktemp)"
ros_wait_pid=""

cleanup() {
  status=$?
  set +e
  if [ -n "${ros_wait_pid:-}" ]; then
    kill "$ros_wait_pid" >/dev/null 2>&1
    wait "$ros_wait_pid" >/dev/null 2>&1
  fi
  if [ "$status" -ne 0 ]; then
    echo "=== e2e failure diagnostics (exit ${status}) ===" >&2
    if [ -n "${KUBECONFIG:-}" ]; then
      kubectl -n e2e-test get pods,svc,nodes -o wide >&2 || true
      kubectl -n e2e-test get mikrotikrouter,mikrotikdnsrecord,mikrotikroute,mikrotikportforward,mikrotikfirewallrule >&2 || true
      for kind in mikrotikrouter mikrotikportforward mikrotikfirewallrule; do
        echo "=== ${kind} conditions ===" >&2
        kubectl -n e2e-test get "$kind" -o jsonpath='{range .items[*]}{.metadata.name}{" applied="}{.status.applied}{"\n"}{range .status.conditions[*]}{"  "}{.type}={.status} reason={.reason} msg={.message}{"\n"}{end}{end}' >&2 || true
      done
      kubectl -n mikrotik-operator-system get pods,deploy -o wide >&2 || true
      kubectl -n mikrotik-operator-system logs -l app.kubernetes.io/name=mikrotik-operator --tail=120 >&2 || true
    fi
    docker logs "$ROUTER_CONTAINER" 2>&1 | tail -n 80 >&2 || true
  fi
  docker rm -f "$ROUTER_CONTAINER" >/dev/null 2>&1
  if [ "${E2E_KEEP_ON_FAILURE:-}" != "1" ] || [ "$status" -eq 0 ]; then
    k3d cluster delete "$CLUSTER_NAME" >/dev/null 2>&1
    docker network rm "$CLUSTER_NETWORK" >/dev/null 2>&1
  else
    echo "keeping k3d cluster ${CLUSTER_NAME} for investigation" >&2
  fi
  rm -f "$KUBECONFIG_FILE" "$FIXTURE_FILE"
}
trap cleanup EXIT

for command in docker k3d kubectl helm go; do
  command -v "$command" >/dev/null 2>&1 || { echo "missing required command: $command" >&2; exit 1; }
done

# QEMU RouterOS cannot be reached through Kubernetes CNI: the image bridges the
# guest onto the container NIC, so in-cluster hostPort/Service/pod-IP targets
# never see TCP/8728. Run it as a Docker container with two networks so eth0
# keeps QEMU hostfwd (published to the host) and eth1 joins the k3d network.
docker rm -f "$ROUTER_CONTAINER" >/dev/null 2>&1 || true
k3d cluster delete "$CLUSTER_NAME" >/dev/null 2>&1 || true
docker network rm "$CLUSTER_NETWORK" >/dev/null 2>&1 || true
docker network create "$CLUSTER_NETWORK" >/dev/null

docker_create_args=(
  --name "$ROUTER_CONTAINER"
  --privileged
  --cap-add=NET_ADMIN
  --device=/dev/net/tun
  --network bridge
  --publish "${ROUTER_HOST_PORT}:8728"
)
if [ -e /dev/kvm ]; then
  docker_create_args+=(--device=/dev/kvm)
fi
docker pull "$ROUTER_IMAGE" >/dev/null
docker create "${docker_create_args[@]}" "$ROUTER_IMAGE" >/dev/null
docker network connect "$CLUSTER_NETWORK" "$ROUTER_CONTAINER"
docker start "$ROUTER_CONTAINER" >/dev/null

echo "Waiting for RouterOS API on 127.0.0.1:${ROUTER_HOST_PORT}..."
E2E_WAIT_ONLY=1 E2E_ROUTEROS_ADDRESS="127.0.0.1:${ROUTER_HOST_PORT}" go run -buildvcs=false ./hack/e2e &
ros_wait_pid=$!

k3d cluster create "$CLUSTER_NAME" --servers 1 --agents 1 --wait --timeout 180s \
  --network "$CLUSTER_NETWORK" \
  --k3s-arg '--disable=traefik@server:*' >/dev/null
k3d kubeconfig get "$CLUSTER_NAME" >"$KUBECONFIG_FILE"
export KUBECONFIG="$KUBECONFIG_FILE"

docker build -t "$OPERATOR_IMAGE" .
k3d image import "$OPERATOR_IMAGE" --cluster "$CLUSTER_NAME"

kubectl apply --server-side -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"
for crd in \
  gatewayclasses.gateway.networking.k8s.io \
  gateways.gateway.networking.k8s.io \
  httproutes.gateway.networking.k8s.io \
  referencegrants.gateway.networking.k8s.io; do
  kubectl wait --for=condition=Established "crd/$crd" --timeout=120s
done
helm upgrade --install mikrotik-operator ./charts/mikrotik-operator \
  --namespace mikrotik-operator-system --create-namespace \
  --set replicaCount=1 --set image.repository=mikrotik-operator \
  --set image.tag=e2e --set image.pullPolicy=IfNotPresent \
  --set leaderElection.enabled=false --set gatewayAPI.enabled=true \
  --set gatewayAPI.gatewayClass.create=false
operator_deployment="$(kubectl -n mikrotik-operator-system get deployment -l app.kubernetes.io/name=mikrotik-operator -o name | head -n 1)"
test -n "$operator_deployment"
kubectl -n mikrotik-operator-system rollout status "$operator_deployment" --timeout=180s
wait "$ros_wait_pid"
ros_wait_pid=""

# k3d does not always inject host.k3d.internal into CoreDNS (Docker Desktop).
# Pods can reach host-published ports through the k3d Docker network gateway.
router_api_ip="$(docker network inspect "$CLUSTER_NETWORK" -f '{{(index .IPAM.Config 0).Gateway}}')"
test -n "$router_api_ip"

sed -e "s/routeros.e2e-test.svc.cluster.local/${router_api_ip}/" \
  -e "s/port: 8728/port: ${ROUTER_HOST_PORT}/" \
  examples/e2e-all.yaml >"$FIXTURE_FILE"
kubectl apply -f "$FIXTURE_FILE"
kubectl -n e2e-test rollout status deployment/web --timeout=180s

cluster_ip="$(kubectl -n e2e-test get service web-cluster -o jsonpath='{.spec.clusterIP}')"
ingress_ip="$(kubectl -n e2e-test get service web-ingress -o jsonpath='{.spec.clusterIP}')"
node_port="$(kubectl -n e2e-test get service web-node -o jsonpath='{.spec.ports[0].nodePort}')"
node_ips="$(kubectl get nodes -o jsonpath='{range .items[*]}{range .status.addresses[?(@.type=="InternalIP")]}{.address}{"\n"}{end}{end}' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' || true)"
node_ip="${node_ips%%$'\n'*}"
test -n "$cluster_ip"
test -n "$ingress_ip"
test -n "$node_ip"

echo "Waiting for Kubernetes resources to be applied to RouterOS..."
kubectl -n e2e-test wait --for=jsonpath='{.status.applied}'=true mikrotikdnsrecord --all --timeout=240s
kubectl -n e2e-test wait --for=jsonpath='{.status.applied}'=true mikrotikroute --all --timeout=240s
kubectl -n e2e-test wait --for=jsonpath='{.status.applied}'=true mikrotikportforward --all --timeout=240s
kubectl -n e2e-test wait --for=jsonpath='{.status.applied}'=true mikrotikfirewallrule --all --timeout=240s

echo "Verifying RouterOS configuration..."
E2E_CLUSTER_IP="$cluster_ip" E2E_INGRESS_IP="$ingress_ip" E2E_NODE_IP="$node_ip" E2E_NODE_IPS="$node_ips" E2E_NODE_PORT="$node_port" \
  E2E_ROUTEROS_ADDRESS="127.0.0.1:${ROUTER_HOST_PORT}" go run -buildvcs=false ./hack/e2e
