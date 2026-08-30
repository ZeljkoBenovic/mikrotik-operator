## Summary

<!-- What does this change do, and why? -->

## Changes

-

## How to test

<!-- Include commands and, where relevant, k3s/RouterOS validation. -->

```sh
go test ./...
go vet ./...
```

## Checklist

- [ ] I ran `gofmt` on changed Go files.
- [ ] I added or updated tests for behavior changes.
- [ ] I updated CRDs, Helm templates, raw manifests, examples, and docs when applicable.
- [ ] I verified ownership and deletion behavior for external RouterOS state.
- [ ] I did not include credentials or other secrets.
- [ ] I ran the relevant Helm, Kustomize, and manifest validation commands.
