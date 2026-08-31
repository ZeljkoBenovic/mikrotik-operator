# MikroTik Operator Agent Instructions

Load the shared repository instructions first:

@AGENTS.md

Service, Ingress, HTTPRoute, and annotation controllers create child CRs;
they must not call the RouterOS client. See the CR-only mutation rule in
`AGENTS.md`.

The repository-local skills referenced by `AGENTS.md` are vendored under
`skills/`. Read the narrowest applicable `SKILL.md` before changing code,
Kubernetes resources, Helm charts, containers, or CI/CD configuration.
