# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows semantic versioning.

## [Unreleased]

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
