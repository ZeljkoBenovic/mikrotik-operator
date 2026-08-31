---
layout: default
title: Expose Services
nav_order: 2
redirect_from:
  - /how-to-expose-services.html
---

# Expose Services

This guide shows the normal annotation-driven workflow for a local Kubernetes
cluster using a MikroTik router as its gateway.

## Create a DNS record and route for a ClusterIP Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: web
  namespace: default
  annotations:
    mikrotik.operator.io/dns-name: web.home.arpa
spec:
  selector:
    app: web
  ports:
    - name: http
      port: 80
      targetPort: 8080
```

For a ClusterIP Service, the operator creates an owned `MikroTikDNSRecord`
pointing to the ClusterIP and owned `MikroTikRoute` objects (`/32` via node
InternalIP addresses). By default, all eligible nodes are used for redundancy.
Set `mikrotik.operator.io/route-mode: single-node` to use one node instead.
Headless Services (`clusterIP: None`) are skipped. `public-ip` forwards only
TCP and UDP ports.

## Expose a NodePort Service

```yaml
metadata:
  annotations:
    mikrotik.operator.io/dns-name: nodeport.home.arpa
    mikrotik.operator.io/public-ip: 203.0.113.10
```

For a NodePort Service, the generated DNS and NAT configuration targets a node
InternalIP and the allocated NodePort. The ClusterIP route is not created.

## Expose an Ingress

Ingresses use the `mikrotik` IngressClass and do not need a DNS annotation.
The operator creates owned `MikroTikDNSRecord` and `MikroTikRoute` resources
for each hostname and backend Service.

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
  annotations:
    mikrotik.operator.io/public-ip: 203.0.113.10
spec:
  ingressClassName: mikrotik
  rules:
    - host: web.home.arpa
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: web
                port:
                  number: 80
```

The IngressClass name must be `mikrotik`, and the cluster `IngressClass`
object must use controller `mikrotik.operator.io/controller`. Ingresses
without that class are ignored and any previously owned children are removed.

With `public-ip`, only the Service ports selected by Ingress paths receive
port forwards. Only TCP and UDP ports are forwarded.

## Use Gateway API HTTPRoute

Gateway API support is disabled by default. Enable it during Helm installation:

```sh
helm upgrade --install mikrotik-operator \
  ./charts/mikrotik-operator \
  --namespace mikrotik-operator-system \
  --create-namespace \
  --set gatewayAPI.enabled=true \
  --set gatewayAPI.gatewayClass.create=true
```

Create an `HTTPRoute` attached to the configured GatewayClass. The operator
creates DNS and routes only after all of the following are true:

- Gateway API CRDs are installed and `gatewayAPI.enabled` is true.
- The parent Gateway uses the configured GatewayClass
  (`mikrotik` / `mikrotik.operator.io/controller` by default).
- A listener protocol is HTTP or HTTPS (TCP/UDP listeners are ignored).
- The listener hostname intersects the HTTPRoute hostnames.
- `allowedRoutes` permits this HTTPRoute (same namespace by default, or
  `All` / label selector).
- Cross-namespace Service backends have a Gateway API `ReferenceGrant`
  in the Service namespace allowing this HTTPRoute.

See [`examples/gateway-api.yaml`](https://github.com/ZeljkoBenovic/mikrotik-operator/blob/main/examples/gateway-api.yaml).

## Remove managed configuration

Remove the annotation or delete the Kubernetes resource. The operator removes
only the corresponding RouterOS entries. RouterOS entries created manually, or
by another system, remain untouched.
