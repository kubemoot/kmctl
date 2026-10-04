<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/assets/kubemoot-horizontal-white-text.png">
  <img src=".github/assets/kubemoot-horizontal-color.png" alt="Kubemoot" height="64">
</picture>

# kmctl

[![Latest release](https://img.shields.io/github/v/release/kubemoot/kmctl?sort=semver)](https://github.com/kubemoot/kmctl/releases/latest) [![Build status](https://github.com/kubemoot/kmctl/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/kubemoot/kmctl/actions/workflows/ci.yaml?query=branch%3Amain) [![License: Apache 2.0](https://img.shields.io/github/license/kubemoot/kmctl)](https://github.com/kubemoot/kmctl/blob/main/LICENSE) [![OpenSSF Scorecard](https://img.shields.io/ossf-scorecard/github.com/kubemoot/kmctl?label=openssf%20scorecard)](https://scorecard.dev/viewer/?uri=github.com/kubemoot/kmctl)

`kmctl` is the command-line tool for working with crews and Kubemoot. It is the
scriptable, terminal-native counterpart to
[CrewForge for VS Code](https://github.com/kubemoot/vscode-crewforge): a convenience layer
over the Kubernetes resources Kubemoot manages (crews, agents, prompt modules,
models) plus its live discussions and fitness suites. The operator remains the
single source of lifecycle truth; `kmctl` is a client.

> Status: **early**. Commands are added as the tool grows. kmctl works against the
> `kubemoot.ai/v1alpha1` API served by the Kubemoot operator.

Documentation:

- [kmctl user guide](https://kubemoot.org/docs/user-guides/kmctl/)
- [kmctl command reference](https://kubemoot.org/docs/reference/kmctl/)

## Commands

```
kmctl
  status                       check cluster connectivity and whether Kubemoot is installed
  info                         show the resolved context, namespace, and cluster connection
  create <name>                scaffold the starter crew: 1 to 5 specialists that read their own namespace
                               (--chart for a Helm chart, --display-name for the name people read)
  apply -f <path>              apply Kubemoot resource manifests (server-side apply)
  delete (KIND NAME | -f ...)  delete Kubemoot resources (kubemoot.ai CRs only)
  crew                         list | get
  agent                        list | get
  conversation (conv)          ask | watch        (stream a live discussion)
  fitness                      run | list | get | scenarios | download
  prompt                       list | get         (PromptModules)
  model                        list | get | footprint   (providers + live GPU footprint)
  version                      print the kmctl version (--short for just the number)
  completion                   generate shell completion (bash, zsh, fish, powershell)
  help                         help about any command
```

The UX mirrors `kubectl` / `helm`: the standard kubeconfig, `--context`, and
`-n/--namespace` flags, `-o yaml|json` output (a table by default), full `--help`,
and generated shell completion.

Planned, not yet implemented: `crew status`, `conversation list | get`, and a
`config` command.

## Install

`kmctl` ships as a single static binary with no runtime dependencies. Each
[GitHub Release](https://github.com/kubemoot/kmctl/releases) publishes six archives,
`kmctl_<version>_<os>_<arch>`, for `linux`, `darwin`, and `windows` on `amd64` and
`arm64` (`.tar.gz`, or `.zip` for Windows), plus `checksums.txt`, its Sigstore signature
`checksums.txt.sigstore.json`, and the SLSA build provenance `kmctl_<version>.intoto.jsonl`
(see [Verify a release](#verify-a-release)).

### Linux and macOS

Releases are built for `amd64` and `arm64` only.

```bash
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"          # linux | darwin
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "kmctl releases are built for amd64 and arm64 only" >&2; exit 1 ;;
esac
TAG="$(basename "$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
  https://github.com/kubemoot/kmctl/releases/latest)")"
case "$TAG" in v*) ;; *) echo "no kmctl release found" >&2; exit 1 ;; esac
ARCHIVE="kmctl_${TAG#v}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/kubemoot/kmctl/releases/download/${TAG}"
curl -fsSLO "${BASE}/${ARCHIVE}"
curl -fsSLO "${BASE}/checksums.txt"
grep " ${ARCHIVE}\$" checksums.txt | sha256sum -c -     # macOS: shasum -a 256 -c -
tar -xzf "${ARCHIVE}" kmctl
sudo install -m 0755 kmctl /usr/local/bin/kmctl
kmctl version
```

With the GitHub CLI instead:

```bash
gh release download -R kubemoot/kmctl --pattern 'kmctl_*_linux_amd64.tar.gz' --pattern checksums.txt
sha256sum -c --ignore-missing checksums.txt
tar -xzf kmctl_*_linux_amd64.tar.gz kmctl
sudo install -m 0755 kmctl /usr/local/bin/kmctl
```

### Windows

Download `kmctl_<version>_windows_amd64.zip` (or `_arm64`) from the
[latest release](https://github.com/kubemoot/kmctl/releases/latest), extract
`kmctl.exe`, and put it on your `PATH`.

### Verify a release

Releases are signed without a stored key: Sigstore issues a short-lived certificate to
the `promote-release.yaml` workflow on `main` of `kubemoot/kmctl`, and the signature is
recorded in Sigstore's public transparency log. Check that `checksums.txt` was signed by
that workflow with [cosign](https://docs.sigstore.dev/cosign/system_config/installation/),
then check the archive against it:

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity https://github.com/kubemoot/kmctl/.github/workflows/promote-release.yaml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum -c --ignore-missing checksums.txt          # macOS: shasum -a 256 -c --ignore-missing
```

The build provenance states which workflow run built each archive from which commit.
Verify it with the GitHub CLI (add `--bundle kmctl_<version>.intoto.jsonl` to use the
downloaded copy instead of the repository's attestations):

```bash
gh attestation verify kmctl_<version>_linux_amd64.tar.gz --repo kubemoot/kmctl \
  --signer-workflow kubemoot/kmctl/.github/workflows/promote-release.yaml
```

### With Go

```bash
go install github.com/kubemoot/kmctl@latest
```

### Shell autocomplete

`kmctl` generates completion scripts for bash, zsh, fish, and PowerShell:

```bash
# bash, system-wide (needs the bash-completion package)
kmctl completion bash | sudo tee /etc/bash_completion.d/kmctl >/dev/null

# bash, current user (no sudo)
mkdir -p ~/.local/share/bash-completion/completions
kmctl completion bash > ~/.local/share/bash-completion/completions/kmctl

# zsh (ensure `autoload -U compinit && compinit` is in ~/.zshrc)
kmctl completion zsh | sudo tee "${fpath[1]}/_kmctl" >/dev/null

# fish
kmctl completion fish > ~/.config/fish/completions/kmctl.fish
```

Start a new shell (or `source` your rc file) and tab-completion works for commands,
flags, and (where supported) resource names. See `kmctl completion --help` for the
per-shell details.

## Build and test

Requires Go 1.27+.

```bash
make build      # build the ./kmctl binary
make test       # unit tests with coverage
make test-race  # unit tests with the race detector (needs CGO)
make lint       # golangci-lint (cyclomatic complexity <= 10, dupl, fmt)
make hooks      # install the pre-commit gate (gofmt + lint)
```

## Quality gates

- `golangci-lint` with `gocyclo` (CC <= 10) and `dupl`, plus `gofmt`/`goimports`,
  enforced by a pre-commit hook and in CI.
- A SonarQube quality report (`.github/workflows/quality.yaml`) runs in parallel
  with CI and never blocks builds.
- Every command ships with unit tests.

## Releases

Every merge to `main` tags a release candidate; the version comes from conventional commits.
A maintainer runs Promote Release to tag the final version, and `goreleaser` publishes
cross-platform binaries to a GitHub Release. See
[Releases and Versioning](https://kubemoot.org/docs/community/releases/).

## Community and contributing

Kubemoot is an independent open-source project under the Apache License 2.0. Contributing, support, governance, the code of conduct, security reporting, and releases are documented in one place: the [Community section of kubemoot.org](https://kubemoot.org/docs/community/). Ask questions and share ideas in [GitHub Discussions](https://github.com/orgs/kubemoot/discussions). Write to moot@kubemoot.org for anything else. Use security@kubemoot.org only to report a vulnerability, privately.

## License

Apache License 2.0. See [LICENSE](LICENSE).
