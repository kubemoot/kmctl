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
  conversation (conv)          ask | watch | list | placements | artifact
                               (stream a live discussion; -o full|plain|json|quiet)
  fitness                      run | list | get | scenarios | download
  prompt                       list | get         (PromptModules)
  model                        list | add | server (list | get) | footprint
                               (the cluster's model catalog, model servers and their calibration)
  version                      print the kmctl version (--short for just the number)
  completion                   generate shell completion (bash, zsh, fish, powershell)
  help                         help about any command
```

The UX mirrors `kubectl` / `helm`: the standard kubeconfig, `--context`, and
`-n/--namespace` flags, `-v/--verbose` for the error underneath a failure, `-o yaml|json|kyaml` output (a table by default; `kyaml` is the
strict, quoted YAML subset `kubectl -o kyaml` prints), full `--help`,
and generated shell completion.

Planned, not yet implemented: `crew status`, `conversation get`, and a `config`
command.

### Crews ship no models

A crew made by `kmctl create` ships no Model objects and names no model server. Its
CrewSchedulingPolicy states what the crew needs: every phase requires a model that can
call tools (`capability/tool-calling: "true"`), mulling ranks the allowed models by
`qualityBias` (which reads the `kubemoot.ai/size` label: small, medium, large), and
triage sets `preferFaster` to favor the model calibration measured fastest. The
scheduler matches those rules against the cluster's model catalog, so the same crew
installs on any cluster that has at least one model that can call tools. `kmctl create`
reads the current cluster's catalog and warns when it has none; scaffolding itself needs
no cluster.

### The model catalog

`kmctl model list` shows the cluster's model catalog, one row per model per model
server, as each ModelServer reports it in `status.models`. It reads every namespace, or
only the one `-n` names, and `-o yaml|json|kyaml` prints the entries as a list:

```
MODEL      SERVER      SIZE     ABILITIES               CONTEXT   PRESENT
qwen3:14b  vllm-gpu    medium   tool-calling            40960     yes
qwen3:8b   ollama-gpu  small    reasoning,tool-calling  40960     yes
```

`SIZE` is `unknown` until the parameter count is known; `PRESENT` is `no` for a model
the cluster owner asked for that the model server does not have yet. When no model can
call tools, `kmctl model list` (and `kmctl create`) warns that crews requiring one stay
Unschedulable.

Cluster owners add a model with `kmctl model add`, which writes a Model into the
namespace of the model server `--model-server` names. The Model is named
`<server>-<model>`, with a short hash added when the model name holds characters a
Kubernetes name cannot (`qwen3:14b`), so adding it again updates it. Without `-n`,
the model server must be in exactly one namespace. On an Ollama model server the
operator downloads the model; for a vLLM model server, declare what the server cannot
report:

```bash
kmctl model add qwen3:14b --model-server ollama-gpu
kmctl model add qwen3-14b --model-server vllm-gpu --parameters 14B --capabilities tool-calling,reasoning
```

### Model servers

`kmctl model server list` shows each ModelServer's calibration phase.
`kmctl model server get NAME` adds what calibration measured, one row per model:

```
Calibration: Calibrated, measured 3 of 3 models, engine 0.34.4, completed 2026-10-17T03:37:10Z

MODEL      FITS  LOAD   GENERATE   PROMPT       PARALLEL       MEMORY    REASON
qwen3:14b  yes   2.5s   86 tok/s   6313 tok/s   2 x 162 tok/s  13839 MiB
qwen3:32b  no    29.5s  <none>     <none>       <none>         <none>    spills to the CPU: 22265 of 27771 MiB on the accelerator
qwen3:8b   yes   3.1s   152 tok/s  11748 tok/s  2 x 241 tok/s  9475 MiB
```

### Conversation output

`kmctl conversation ask` and `kmctl conversation watch` take `-o full|plain|json|quiet`:

| Mode | What it shows |
|------|---------------|
| `full` (default) | A live view: one row per agent with a spinner while it works, its step, the accelerator it runs on, and how long triage and evaluation took. Each row settles to a colored signal (agree, concern, block, failure, stand aside). The answer is rendered as markdown. |
| `plain` | One line per event with the time since the turn began, no color or animation, for logs, CI, and screen readers. The answer is raw markdown. |
| `json` | Newline-delimited JSON: a `conversation` line, one line per event with `elapsedMs` and any step duration, and a final `result` line with `answer`, `summary`, and `next`. Every line carries `schemaVersion` (1). |

`full` falls back to `plain` when stdout is not a terminal, `NO_COLOR` is set, or
`TERM=dumb`. `-o quiet` prints only the answer; `-q` is the same, and `-q` with a different `-o` is an error.

Every mode ends with a short summary right after the answer's own notes, then the
command that asks a follow-up in the same conversation. The summary has the total
time and time to the first signal, the crew's own time from the thread's start to
the answer, the subcommittee the coordinator convened (with each agent's reason
when the coordinator gave one), the agents that took part and those that stood
aside, signals by kind, tokens in and out, accelerators, models, agents that waited
for GPU capacity, tool errors, and the command that fetches each attached artifact:

```
[   3.0s] subcommittee: pilot-metrics (node metrics), pilot-proxmox (proxmox inventory)
[   8.0s] pilot-gpu              stood aside (qwen3:8b, 419 tokens): not in the subcommittee
[  10.0s] pilot-proxmox          agree (evaluation 10.4s, qwen3:14b, 5,850 tokens): The cluster has three Proxmox nodes, all online.
[  11.0s] pilot-metrics          agree (evaluation 12.0s, qwen3:14b, 8,230 tokens): attached artifact (8.3 KB); fetch command below
[  14.0s] done

ANSWER:
...

**How this answer was made**

- Total time (as received by kmctl): 14.0s; first signal after 8.0s
- Crew time (thread start to answer, as the crew published): 31.0s
- Subcommittee: 3 convened (pilot-metrics (node metrics), pilot-proxmox (proxmox inventory), pilot-storage)
- Signals: 2 agree, 1 failure, 1 stand_aside
- Tokens: 15,740 in, 2,059 out
- Models: qwen3:14b, qwen3:8b
- Tool errors: pilot-storage: zfs_pool_status failed 3 times: connection refused
- Artifact from pilot-metrics (8.3 KB): `kmctl conversation artifact homelab-pilot crew-homelab-pilot/homelab-pilot/t-42/pilot-metrics/agree-1b2c -n crew-homelab-pilot`

Next: kmctl conversation ask homelab-pilot "<next question>" -n crew-homelab-pilot --conversation-id 6733c361-aa0c-4bde-85c6-e143af23b251
```

The total time is when kmctl received each event; for `watch` the clock starts when
kmctl attached, and the summary says so. Step durations come from the timestamps
the crew published when the gateway forwards them (`timeSource` is `published` in
`-o json`), else from when kmctl received each event (`received`). The summary
shows only what the stream carries: a gateway older than these fields sends no
timestamps, tokens, models, subcommittee, or tool errors, and they are left out
rather than estimated. Retries are not published by the crew, so the summary never
shows them.

### Short conversation IDs, the conversation list, and artifacts

`kmctl conversation list <crew>` lists the crew's recent conversations, newest
first, with each one's first question (`-o json|yaml` for scripts). Wherever kmctl
takes a conversation ID (`--conversation-id` on `ask`, the ID argument of `watch`),
a unique prefix works, as with git hashes: `--conversation-id 6733c`. An ambiguous
prefix fails and lists the matches; a prefix that matches nothing says so. Shell
completion offers the recent conversations with their first questions. A gateway
that cannot list conversations needs the full ID, and kmctl says so.

`kmctl conversation artifact <crew> <key>` writes an attached artifact to stdout,
or to a file with `-f`. The coordinator deletes a thread's artifacts soon after the
thread closes, so fetch them while the crew works on the question or right after
it answers; after that kmctl reports the artifact as not found.

### Placement decisions

`kmctl conversation placements <crew> <conversation-id>` shows where the scheduler
placed each model call of the conversation's latest turn and why. It reads the
receipt the crew's coordinator puts on the turn's closing message, so it needs a
discussion gateway that forwards the receipt on the closing event. A turn that has
closed replays from the discussion stream, which keeps about a day of discussions;
for a turn still running the command waits until it closes, on the same terms as
`watch`: `--idle-timeout` (default `5m`) and `--timeout` (default `0`, no cap), described
under "How long kmctl waits". When the wait ends or the stream is cut before the
turn closes, kmctl prints the conversation and thread IDs and the `kmctl conversation
watch` command that reattaches. If the stream holds more than one closed thread for
the turn, the receipt of the latest one is used.

```
#   AGENT                   PHASE        PLACEMENT                     POLICY      PREDICTED   ACTUAL   ERROR
1   coordinator             advisory     qwen3:14b@ollama-gpu          learned     2.9s        3.4s     +500ms (15%)
2   obs-metrics             triage       qwen3:8b@ollama-cpu           shadow      -           31.0s    -
3   kubernetes-specialist   evaluation   qwen3:14b@ollama-gpu (cold)   algorithm   11.4s       13.8s    +2.4s (17%)

[1] coordinator advisory on qwen3:14b@ollama-gpu
    reason: predicted 2.9s (wait 0.0, load 0.0, prompt 0.6, generate 2.3)
    alternatives:
      ollama-cpu  predicted 61.0s  (warm, starts now)
...

3 calls: 1 algorithm, 1 learned, 1 shadow. Mean absolute error 1.4s over the 2 calls with a measured error.
```

One row per call, in the order the calls were sent. The policy says who placed the
call: `learned` (chosen from predicted completion time), `algorithm` (the
hand-written placement), or `shadow` (the algorithm placed it while the learned
policy only recorded what it would have chosen). `(cold)` marks a call whose model
was not loaded when it was placed. ERROR is the measured time minus the predicted
time, positive when the call took longer than predicted, with its size relative to
the measured time; a call with no prediction shows `-`, and a call that failed
shows `failed` under ACTUAL. Below the table, each call gives the reason for its
placement, the predicted and measured time to the first token when the receipt has
them, and the other servers the scheduler weighed, with their predicted times and
whether the model was loaded there and a slot was free. The last line counts the
calls per policy and gives the mean absolute error over the calls that have a
measured error.

`-o json`, `-o yaml`, or `-o kyaml` print `conversationId`, `crew`, `calls` (the
calls exactly as the receipt carried them), and `summary` (the counts and the
mean absolute error). A receipt without placement calls (agents older than this
feature do not report them) prints one line saying so, and `calls` is an empty list
in the structured formats. A turn that closed without a receipt says that instead.

### How long kmctl waits

kmctl waits as long as the crew is working. While anyone on the crew is busy (an
agent triaging or evaluating, waiting for an accelerator, a model loading, the
coordinator synthesizing the answer), the discussion gateway sends an `activity`
event every 15 seconds. `plain` prints it as a `busy:` line, `json` as a line of type
`activity` with the busy `agents` and the time `at`, and `full` keeps the agents'
spinners turning and names who is busy in the header:

```
[  4m37s] busy: compute evaluating on ollama-rig0, obs-advisor triaging
```

`kmctl conversation placements` waits the same way.

The only default clock is a no-activity clock: `--idle-timeout` (default `5m`) runs
only while the crew shows no activity and starts again with every event from the
crew. The gateway's connection heartbeat does not count. `--timeout` (default `0`,
no cap) is an overall limit for scripts. When either one stops the wait, kmctl
prints the conversation and thread IDs and the command that reattaches, because
the crew may still answer:

```
Error: the crew showed no activity for 5m0s; the discussion may still finish.
Conversation 6733c361-aa0c-4bde-85c6-e143af23b251, thread 8f0c2a1e
Reattach: kmctl conversation watch homelab-pilot 6733c361-aa0c-4bde-85c6-e143af23b251 -n crew-homelab-pilot
```

A gateway that does not send `activity` events yet sends nothing between an
agent's signals; there the idle clock restarts only on signals, and the 5-minute
default covers a cold model load followed by a long evaluation. Setting both
`--idle-timeout 0` and `--timeout 0` waits for as long as the stream stays open.

### Errors

When a command fails, kmctl prints what went wrong and, on a second line, what to
do next, and exits with status 1:

```
$ kmctl conversation ask homelab-pilot "List the pods in the system namespace."
Error: no crew "homelab-pilot" in namespace "default"; found in "crew-homelab-pilot"
Hint: Pass -n crew-homelab-pilot
```

| Failure | Message |
|---------|---------|
| No kubeconfig or no current context | `no Kubernetes cluster is configured (the kubeconfig has no current context)` |
| `--context` not in the kubeconfig | `context "NAME" is not in the kubeconfig` |
| API server unreachable | `cannot reach the Kubernetes API server (context "NAME")` |
| Credentials rejected | `the Kubernetes API server did not accept your credentials (context "NAME")` |
| Missing permission (RBAC) | `you are not allowed to VERB RESOURCE in namespace "NS"` |
| Kubemoot not installed | `the cluster does not serve the Kubemoot API (kubemoot.ai)` |
| `get` of a missing object | `no crew "NAME" in namespace "NS"` (and the same for the other nouns) |
| Crew in another namespace | `no crew "NAME" in namespace "NS"; found in "OTHER"` |
| Gateway missing, not ready, or failing | `the discussion gateway of crew "NAME" was not found in namespace "NS"`, `... is not ready`, `... answered HTTP CODE` |
| Stream cut before the answer | `the discussion stream closed before the crew answered; the discussion may still finish` |
| Bad flag or arguments | cobra's message, then the command's usage line |

When a crew is not in the namespace, kmctl looks for a crew of that name in every
namespace and names the ones it finds. Without permission to list crews across the
cluster, it suggests `-n` and `kmctl crew list -A` instead. `-n` always decides the
namespace; kmctl never switches it on its own.

`-v` (`--verbose`) or `KMCTL_DEBUG=1` adds a third line, `Cause:`, with the error
from the Kubernetes client underneath. The `Error:` and `Hint:` labels are colored
only when stderr is a terminal, `NO_COLOR` is unset, and `TERM` is not `dumb`.

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
cd "$(mktemp -d)"
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
cd "$(mktemp -d)"
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
the `publish-release.yaml` workflow on `main` of `kubemoot/kmctl`, and the signature is
recorded in Sigstore's public transparency log. Check that `checksums.txt` was signed by
that workflow with [cosign](https://docs.sigstore.dev/cosign/system_config/installation/),
then check the archive against it:

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity https://github.com/kubemoot/kmctl/.github/workflows/publish-release.yaml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum -c --ignore-missing checksums.txt          # macOS: shasum -a 256 -c --ignore-missing
```

The build provenance states which workflow run built each archive from which commit.
Verify it with the GitHub CLI (add `--bundle kmctl_<version>.intoto.jsonl` to use the
downloaded copy instead of the repository's attestations):

```bash
gh attestation verify kmctl_<version>_linux_amd64.tar.gz --repo kubemoot/kmctl \
  --signer-workflow kubemoot/kmctl/.github/workflows/publish-release.yaml
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
A maintainer runs Publish Release to tag the final version, and `goreleaser` publishes
cross-platform binaries to a GitHub Release. See
[Releases and Versioning](https://kubemoot.org/docs/community/releases/).

## Community and contributing

Kubemoot is an independent open-source project under the Apache License 2.0. Contributing, support, governance, the code of conduct, security reporting, and releases are documented in one place: the [Community section of kubemoot.org](https://kubemoot.org/docs/community/). Ask questions and share ideas in [GitHub Discussions](https://github.com/orgs/kubemoot/discussions). Write to moot@kubemoot.org for anything else. Use security@kubemoot.org only to report a vulnerability, privately.

## License

Apache License 2.0. See [LICENSE](LICENSE).
