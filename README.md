# kmctl

`kmctl` is the command-line tool for working with crews and Kubemoot. It is the
scriptable, terminal-native counterpart to
[CrewForge for VS Code](https://github.com/kubemoot/vscode-crewforge): a convenience layer
over the Kubernetes resources Kubemoot manages (crews, agents, prompt modules,
models) plus its live discussions and fitness suites. The operator remains the
single source of lifecycle truth; `kmctl` is a client.

> Status: **early**. The repository, release pipeline, and quality gates are in
> place; commands are added as the tool grows. See the planned surface below.

## Planned command surface

```
kmctl
  create <name>        scaffold a working crew + starter fitness tests (interactive)
  crew                 list | get | status
  agent                list | get
  conversation (conv)  ask <crew> "..." | list | get | watch   (live stream over HTTP/SSE)
  fitness              run | list | get | download
  prompt               list | get        (PromptModules)
  model                list              (providers + live footprint)
  apply -f / delete                      (CRD-only, server-side apply)
  status | info | version | completion | config | help
```

The UX mirrors `kubectl` / `istioctl` / `helm`: standard kubeconfig/context/namespace
flags, `-o {table|yaml|json|name|wide}` output, full `--help`, and generated shell
completion.

## Install

`kmctl` ships as a single static binary. No runtime dependencies.

### Linux (x86_64 / arm64)

While the repository is private, download the latest release with the GitHub CLI
(it handles auth and resolves the latest version). Use `linux_arm64` on ARM:

```bash
gh release download -R kubemoot/kmctl \
  --pattern 'kmctl_*_linux_amd64.tar.gz' --clobber
tar -xzf kmctl_*_linux_amd64.tar.gz kmctl
sudo install -m 0755 kmctl /usr/local/bin/kmctl
rm -f kmctl_*_linux_amd64.tar.gz
kmctl version
```

After the open-source release the same artifacts install without auth:

```bash
VER="$(curl -fsSL https://api.github.com/repos/kubemoot/kmctl/releases/latest \
  | grep -oP '"tag_name":\s*"\K[^"]+')"
curl -fsSL "https://github.com/kubemoot/kmctl/releases/download/${VER}/kmctl_${VER#v}_linux_amd64.tar.gz" \
  | tar -xz kmctl
sudo install -m 0755 kmctl /usr/local/bin/kmctl
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

Requires Go 1.26+.

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
