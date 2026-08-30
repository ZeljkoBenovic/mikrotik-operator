# MikroTik Operator Admin UI

React 18 + Vite + TypeScript admin panel for the five MikroTik Operator CRDs. The Go `ui-backend` serves this SPA and proxies the Kubernetes API under `/api`.

This UI has **no authentication**. Use it only on a trusted network or behind an authenticating proxy.

## Scripts

| Script | Description |
| --- | --- |
| `npm run dev` | Vite dev server on http://127.0.0.1:5173 with `/api` proxied to http://127.0.0.1:8080 |
| `npm run build` | Typecheck and production build to `dist/` |
| `npm run preview` | Serve the production build locally |
| `npm run lint` | ESLint |
| `npm test` | Vitest unit, component, and API client tests |

## Local development

1. Start the UI backend (from the repository root) so `/api` is available on port 8080.
2. In this directory:

```sh
npm install
npm run dev
```

3. Open http://127.0.0.1:5173.

During `npm run dev` and `npm run preview`, Vite proxies `/api` to `http://127.0.0.1:8080`. In-cluster, the backend serves `dist/` and `/api` from the same origin.

## Pages

- `/` — dashboard with per-kind counts and not-ready resources
- `/routers`, `/dns-records`, `/routes`, `/port-forwards`, `/firewall-rules` — namespaced lists
- `/routers/:namespace/:name` (and the same pattern for the other kinds) — spec, status, conditions, YAML

Owned resources (`managedBy` / controller owner references) are read-only. Edit those from the owning Service, Ingress, or HTTPRoute.
