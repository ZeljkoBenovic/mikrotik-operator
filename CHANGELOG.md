# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows semantic versioning.

## [Unreleased]

### Fixed

- Admin UI shell fills the viewport height, and owned-resource labels
  ellipsize instead of overlapping row actions.

## [0.2.0] - 2026-08-31

### Added

- Owned `MikroTikRoute` custom resources for Service and Ingress annotation
  routes, so generated `/32` routes stay Kubernetes-owned and read-only in the
  admin UI.
- Admin UI creates custom resources in the operator namespace and selects a
  live router from a dropdown instead of free-typing `routerRef`.
- Troubleshooting guide covering router selection, TLS API login, Ingress and
  HTTPRoute attachment, and admin UI ownership conflicts.
- Router-selection, status, Helm value, and local UI development details in the
  existing architecture, reference, getting-started, and admin UI docs.

### Changed

- Default operator and admin UI image tag is `v0.2.0` (`Chart.yaml`
  `appVersion`). Chart package version is `0.2.0`.
- GitHub Actions, container base images, and admin UI npm dependencies.

## [0.1.1] - 2026-08-31

### Fixed

- Resolve `MikroTikRouter` targets across namespaces when a resource has no local router. Explicit refs accept `name` or `namespace/name`.

### Changed

- Default operator and admin UI image tag is `v0.1.1` (`Chart.yaml` `appVersion`). Chart package version is `0.1.1`.

## [0.1.0] - 2026-08-31

### Changed

- Release publishing now uses GoReleaser to push multi-arch operator and admin UI container images to GHCR. GitHub Releases stay changelog-only, with no binary or OS packages.
- Helm chart publishing is a separate `main`-branch pipeline that uses `Chart.yaml` version, independent of operator image tags. That pipeline runs Helm lint, template, and CRD checks only.
- Improved repository guidance and AI-assisted contribution workflows.
- Added grouped Dependabot updates for Go, GitHub Actions, and container images.

## [0.3.0] - 2026-08-29

### Added

- RouterOS DNS, route, NAT, and firewall reconciliation.
- Service annotation, Ingress, and Gateway API support.
- Helm packaging, k3s helpers, and GHCR release automation.
