# Contributing to kmctl

The process for issues, pull requests, and commit conventions is on
[kubemoot.org](https://kubemoot.org/docs/community/contributing/). This page covers
what is specific to this repository.

## Build from source

You need the Go version named in `go.mod`.

```bash
make build      # build the ./kmctl binary
make test       # unit tests with coverage
make test-race  # unit tests with the race detector (needs CGO)
make lint       # golangci-lint (cyclomatic complexity <= 10, dupl, fmt)
make hooks      # install the pre-commit gate (gofmt + lint)
```

A build from source reports the module or commit it was built from as its version. The
released binaries carry the release version, stamped at build time.

## Quality gates

- `golangci-lint` with `gocyclo` (cyclomatic complexity of 10 or less) and `dupl`, plus
  `gofmt` and `goimports`, enforced by the pre-commit hook and in CI.
- A SonarQube quality report (`.github/workflows/quality.yaml`) runs in parallel with CI
  and never blocks builds.
- Every command ships with unit tests; see [TESTING.md](TESTING.md) for the test tiers.
- **The scaffold applies.** `make test-scaffold-applies` proves that what `kmctl create`
  writes is a crew the Kubemoot API accepts. For one to five specialists, the bundle and
  the chart (rendered with `helm template`) are applied to an ephemeral API server loaded
  with Kubemoot's CRDs, with strict field validation, so a field Kubemoot renamed or
  removed is an error rather than silently dropped, and each object is read back to check
  nothing was pruned. A control in the test confirms the strict check does refuse an
  undeclared field. A unit test also checks that the scaffold names none of the old
  workspace names. It needs `helm` and takes about 15 seconds including the first
  download of the API server binaries. CI runs it on every push and pull request against
  Kubemoot `main`'s CRDs, and Publish Release runs it, dry runs included, before anything
  is tagged, against the CRDs of the Kubemoot release `go.mod` pins.

## Releases

Every merge to `main` tags a release candidate; the version comes from conventional
commits. A maintainer runs Publish Release to tag the final version, and `goreleaser`
publishes the cross-platform binaries to a GitHub Release. See
[Releases and Versioning](https://kubemoot.org/docs/community/releases/).
