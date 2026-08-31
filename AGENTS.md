# MikroTik Operator Agent Instructions

This repository contains a standalone Go Kubernetes operator for MikroTik RouterOS v6 and v7. Keep changes production-oriented, small, testable, and compatible with local Kubernetes distributions such as k3s.

Project architecture and contributor onboarding are documented in
`docs/_reference/how-it-works.md`, while GitHub Copilot-specific conventions and the
maintenance matrix live in `.github/copilot-instructions.md`. Keep those files
aligned when changing the controller, API, packaging, or contributor workflow.

## Repository-local guidance

Before changing Kubernetes, Helm, Docker, operator, or CI/CD files, read the applicable repository-local skill:

- `skills/kubernetes-skill/SKILL.md` for Kubernetes manifests, security, rollout, networking, API versions, and validation.
- `skills/kubernetes-operator/SKILL.md` for CRDs, controller-runtime reconciliation, finalizers, conditions, RBAC, and operator lifecycle.
- `skills/helm-chart-builder/SKILL.md` for chart structure, values, templates, CRDs, and chart validation.
- `skills/docker-development/SKILL.md` for Dockerfiles, image hardening, build caching, and image reproducibility.
- `skills/ci-cd-pipeline-builder/SKILL.md` for GitHub Actions, release gates, artifact publishing, and deployment stages.
- `skills/golang/golang-code-style/SKILL.md` for readable, idiomatic Go structure and control flow.
- `skills/golang/golang-error-handling/SKILL.md` for wrapped errors, error propagation, and logging boundaries.
- `skills/golang/golang-testing/SKILL.md` for table-driven tests, race detection, fixtures, and integration-test isolation.
- `skills/golang/golang-lint/SKILL.md` for golangci-lint configuration and lint review.
- `skills/golang/golang-safety/SKILL.md` for nil safety, data integrity, and runtime defensive coding.
- `skills/golang/golang-security/SKILL.md` for secrets, network input, command construction, and dependency security.
- `skills/golang/golang-context/SKILL.md` for context propagation and cancellation.
- `skills/golang/golang-observability/SKILL.md` for structured logging, metrics, and production diagnostics.
- `skills/golang/golang-dependency-management/SKILL.md` for Go module and dependency hygiene.
- `skills/golang/golang-project-layout/SKILL.md` for `cmd`, `internal`, and package organization.
- `skills/golang/golang-continuous-integration/SKILL.md` for Go-specific CI checks and release validation.
- `skills/routeros-api/SKILL.md` for RouterOS API commands, managed comments, DNS, routes, NAT, firewall ordering, and v6/v7 compatibility.
- `skills/standards/STANDARDS.md` for the repository-wide communication, quality, Git, documentation, and security standards. Follow the linked standard for the current task.

Use the narrowest applicable skill first. Load referenced material only when the current task needs it.

## Go and controller rules

- Use `gofmt`, clear imports, named composite-literal fields, and readable semantic line breaks.
- Prefer early returns and focused functions. Use an options struct when a function has more than four meaningful inputs.
- Keep public APIs small; unexport helpers unless callers require them.
- Reconcile with: observe actual state → calculate desired state → compare → apply only the difference → update status.
- Reconciliation must be idempotent. Never delete and recreate an unchanged MikroTik rule.
- Manage only objects whose comments carry the operator’s managed prefix. Never modify or delete pre-existing user rules.
- Do not mutate user-provided `spec` fields during reconciliation. Put observed or derived values in status or local variables.
- Use status subresources, stable Conditions, and finalizers. Do not remove a finalizer until external cleanup succeeds.
- Return transient errors so controller-runtime retries them. Never sleep or block inside reconciliation.
- Preserve RouterOS v6/v7 compatibility and test RouterOS command changes carefully.
- Service, Ingress, HTTPRoute, and annotation controllers must create, update, or delete the corresponding custom resources (`MikroTikDNSRecord`, `MikroTikRoute`, `MikroTikPortForward`, `MikroTikFirewallRule`). They must not call the RouterOS client to mutate DNS, routes, NAT, or firewall entries. Only that CR’s own reconciler talks to RouterOS for its kind.

## Kubernetes and Helm rules

- Use supported, non-deprecated API versions and validate CRDs with OpenAPI schemas.
- Keep RBAC least-privilege and explain any cluster-wide permission.
- Keep non-root, read-only-root-filesystem, dropped-capability container defaults.
- Provide resource requests and limits, health probes, leader election for multiple replicas, and a disruption policy where appropriate.
- Use immutable image digests or release tags for production deployments; never use `latest` as the deployed default.
- Preserve human-readable multi-line YAML. Do not compress YAML into opaque one-line mappings.
- Validate with `helm lint`, `helm template --include-crds`, server-side dry-run when a cluster is available, and `kubeconform` where possible.
- Review generated resources together: labels/selectors, owner references, ports, RBAC, CRDs, and chart values must agree.

## CI/CD and security rules

- Pull requests and `main` must run formatting, tests, race tests, vet, vulnerability checks, Go linting, Docker build validation, Helm validation, manifest validation, and workflow linting.
- Publishing container images is allowed only from trusted semantic-version tags and must depend on the complete quality gate.
- Publishing Helm charts is allowed only from `main` after Helm chart validation, using the version in `charts/mikrotik-operator/Chart.yaml`. Do not stamp chart versions from operator image tags. Do not run application tests or e2e suites in the chart pipeline.
- Publish images and charts with `GITHUB_TOKEN`; never commit credentials or embed secrets in images, manifests, examples, or logs.
- Treat GitHub Actions expressions and shell inputs as untrusted. Quote variables and avoid executing pull-request-controlled strings.
- Do not push, publish, deploy, or modify a live cluster unless the user explicitly requests that action.

## Required validation before handoff

Run the checks relevant to the change, normally:

```sh
gofmt -l cmd internal api
go test ./...
go vet ./...
go mod verify
helm lint charts/mikrotik-operator
helm template validation charts/mikrotik-operator --include-crds
```

For Kubernetes changes, use the local k3s kubeconfig when available and run a server-side dry run. For release changes, run `actionlint`, `goreleaser check`, and verify that image publishing is tag-gated and chart publishing is driven by `Chart.yaml` on `main`.

Report assumptions, validations performed, external-cluster limitations, and rollback/recovery notes in the final response.
