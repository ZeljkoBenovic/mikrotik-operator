# Copilot Instructions

## Project profile

This repository contains a standalone Go Kubernetes operator for MikroTik
RouterOS v6 and v7. The executable is `cmd/manager`; controllers live in
`internal/controller`; RouterOS API operations live in `internal/routeros`.
The public Kubernetes API is defined in `api/v1alpha1` and its CRD manifests
are maintained in `config/crd/bases` and copied into the Helm chart under
`charts/mikrotik-operator/crds`.

## Required workflow

Before changing a subsystem, read the narrowest applicable skill under
`skills/`, then inspect the existing controller, API type, CRD, chart, and
example paths involved in the change.

Use these commands before submitting a change:

```sh
gofmt -w cmd internal api
go test ./...
go vet ./...
go mod verify
helm lint charts/mikrotik-operator
helm template validation charts/mikrotik-operator --include-crds
kubectl kustomize config
```

Use `go test -race ./...` when the environment has a working C compiler.
RouterOS integration tests must remain separate from the default unit-test
path and must not require credentials committed to the repository.

## Go and controller conventions

- Reconciliation is observe → calculate desired state → diff → apply → status.
- Reconcile functions must be idempotent and must periodically repair external
  RouterOS drift.
- Use context-aware RouterOS calls and return transient errors for retries.
- Use finalizers for external RouterOS state and remove them only after cleanup
  succeeds or the referenced router is irrecoverably gone.
- Manage only RouterOS entries with the operator's managed-comment prefix.
- Never adopt or overwrite an unowned Kubernetes child resource.
- Store observed values in status; never mutate user-owned `spec` to record
  derived state.
- Preserve RouterOS v6/v7 compatibility and use `net.JoinHostPort` for router
  endpoints.
- Keep controllers focused; extract helpers instead of adding more branching
  to a large `Reconcile` method.

## Kubernetes and packaging conventions

- Keep CRDs namespaced, schema-validated, status-enabled, and synchronized
  between `config/crd/bases` and `charts/mikrotik-operator/crds`.
- Keep RBAC least-privilege and update `config/rbac/role.yaml` when a new
  watched resource or API operation is introduced.
- Keep generated YAML human-readable and use supported API versions.
- Keep chart values, deployment arguments, GatewayClass/IngressClass objects,
  and controller constants or flags aligned.
- Production deployments must use a release tag or digest, not `latest`.

## Review-derived conventions

- Add or update tests for ownership boundaries, deletion/finalizer paths,
  dependency changes, and RouterOS desired-state comparisons.
- Avoid delete-and-recreate behavior when a managed RouterOS rule is already
  correct.
- A resource that references another namespace must resolve that namespace
  explicitly and must not silently fall back to the route's namespace.
- Changes to generated resources must be validated with both Helm rendering
  and Kustomize rendering.
- Do not publish, push, deploy, or modify a live cluster unless the user
  explicitly requests it.

## Maintenance matrix

| Change | Update and validate |
| --- | --- |
| API type or status change | `api/v1alpha1/*.go`, deepcopy code, `config/crd/bases`, chart CRDs, examples, tests |
| New or changed controller | `internal/controller`, RBAC, watches, status behavior, unit tests, `docs/how-it-works.md` |
| RouterOS command or managed comment | `internal/routeros`, RouterOS tests, controller cleanup paths, RouterOS compatibility notes |
| New CLI flag | `cmd/manager/main.go`, Helm deployment template, raw deployment, values/documentation |
| Helm value or template | `charts/mikrotik-operator`, README, rendered-resource validation, chart version in `Chart.yaml` |
| Kubernetes watch or permission | controller setup, `config/rbac/role.yaml`, Helm RBAC templates, tests |
| Release/image behavior | `.goreleaser.yaml`, `Dockerfile`, `Dockerfile.release`, `Dockerfile.ui`, `Dockerfile.ui.release`, `Makefile`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, README |
| Helm chart publishing | `charts/mikrotik-operator/Chart.yaml`, `.github/workflows/release-chart.yml`, README |
| Dependency update behavior | `go.mod`, `go.sum`, `Dockerfile`, `.github/workflows`, `.github/dependabot.yml`, CI validation |
| Contributor workflow | `AGENTS.md`, `CLAUDE.md`, this file, README, `.github/PULL_REQUEST_TEMPLATE.md` |

## Secrets and safety

Use Kubernetes Secrets for RouterOS credentials. Never place credentials in
examples, logs, CI output, image layers, or committed manifests. Treat GitHub
Actions expressions and shell inputs as untrusted.
