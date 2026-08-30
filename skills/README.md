# Repository-local agent skills

These skill packages are vendored so agents can follow the project’s Kubernetes,
operator, Helm, container, and CI/CD conventions without depending on a
machine-global installation. The entry point for agents is the repository root
[`AGENTS.md`](../AGENTS.md).

Use the narrowest applicable `SKILL.md` for the task:

- [`kubernetes-skill`](kubernetes-skill/SKILL.md): Kubernetes manifests, security, networking, API compatibility, and validation.
- [`kubernetes-operator`](kubernetes-operator/SKILL.md): CRDs, reconciliation, finalizers, status, RBAC, and operator lifecycle.
- [`helm-chart-builder`](helm-chart-builder/SKILL.md): chart structure, values, templates, CRDs, and chart validation.
- [`docker-development`](docker-development/SKILL.md): Dockerfile design, image hardening, caching, and reproducibility.
- [`ci-cd-pipeline-builder`](ci-cd-pipeline-builder/SKILL.md): CI checks, release gates, and artifact publishing.
- [`routeros-api`](routeros-api/SKILL.md): RouterOS API, managed configuration, DNS, routing, NAT, firewall ordering, and v6/v7 compatibility.

These copies should be updated deliberately when the upstream guidance changes.
Do not include them in production container images; the root `.dockerignore`
excludes this directory.

## Repository standards

The [`standards/STANDARDS.md`](standards/STANDARDS.md) entry point and its linked
standards are vendored from the upstream
[`claude-skills/standards`](https://github.com/alirezarezvani/claude-skills/tree/main/standards)
library at commit `19392f7a08264ed00486a251f5b2098321771f94`.

All five standards apply to this project:

- `communication`: concise, honest, actionable agent and technical communication.
- `quality`: validation and completion expectations.
- `git`: release, branch, and commit conventions.
- `documentation`: maintainable Markdown and living project documentation.
- `security`: secret handling, input validation, and dependency review.

These standards complement the implementation-focused skills above; use the
specific standard linked from `standards/STANDARDS.md` when it applies.

## Go guidance

The `golang/` subdirectory contains the focused Go skills selected from
[`samber/cc-skills-golang`](https://github.com/samber/cc-skills-golang):

- `golang-code-style`
- `golang-error-handling`
- `golang-testing`
- `golang-lint`
- `golang-safety`
- `golang-security`
- `golang-context`
- `golang-observability`
- `golang-dependency-management`
- `golang-project-layout`
- `golang-continuous-integration`

These are the relevant skills for a production Go Kubernetes operator. Skills
for databases, CLI applications, HTTP APIs, unrelated frameworks, and
library-specific packages were intentionally not vendored. The current copies
come from upstream commit `147c0679e2442fffd45e8f2275e9417f2991e6f5` so they can
be reviewed and refreshed deliberately.
