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

## Releases

Every merge to `main` tags a release candidate; the version comes from conventional
commits. A maintainer runs Publish Release to tag the final version, and `goreleaser`
publishes the cross-platform binaries to a GitHub Release. See
[Releases and Versioning](https://kubemoot.org/docs/community/releases/).
