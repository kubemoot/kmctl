# kmctl

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
  create <name>                scaffold a working crew with starter fitness tests (--chart for a Helm chart)
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
`arm64` (`.tar.gz`, or `.zip` for Windows), plus `checksums.txt`.

### Linux and macOS

```bash
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"          # linux | darwin
ARCH="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
TAG="$(basename "$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
  https://github.com/kubemoot/kmctl/releases/latest)")"
curl -fsSL "https://github.com/kubemoot/kmctl/releases/download/${TAG}/kmctl_${TAG#v}_${OS}_${ARCH}.tar.gz" \
  | tar -xz kmctl
sudo install -m 0755 kmctl /usr/local/bin/kmctl
kmctl version
```

With the GitHub CLI instead:

```bash
gh release download -R kubemoot/kmctl --pattern 'kmctl_*_linux_amd64.tar.gz'
tar -xzf kmctl_*_linux_amd64.tar.gz kmctl
sudo install -m 0755 kmctl /usr/local/bin/kmctl
```

### Windows

Download `kmctl_<version>_windows_amd64.zip` (or `_arm64`) from the
[latest release](https://github.com/kubemoot/kmctl/releases/latest), extract
`kmctl.exe`, and put it on your `PATH`.

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

Conventional commits drive the version. A push to `main` computes the next SemVer
(`paulhatch/semantic-version`), tags it, and `goreleaser` publishes cross-platform
binaries (linux/darwin/windows x amd64/arm64) to a GitHub Release.

## License

Apache License 2.0. See [LICENSE](LICENSE).
