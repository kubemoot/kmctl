<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/assets/kubemoot-horizontal-white-text.png">
  <img src=".github/assets/kubemoot-horizontal-color.png" alt="Kubemoot" height="64">
</picture>

# kmctl

[![Latest release](https://img.shields.io/github/v/release/kubemoot/kmctl?sort=semver)](https://github.com/kubemoot/kmctl/releases/latest) [![Build status](https://github.com/kubemoot/kmctl/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/kubemoot/kmctl/actions/workflows/ci.yaml?query=branch%3Amain) [![License: Apache 2.0](https://img.shields.io/github/license/kubemoot/kmctl)](https://github.com/kubemoot/kmctl/blob/main/LICENSE) [![OpenSSF Scorecard](https://img.shields.io/ossf-scorecard/github.com/kubemoot/kmctl?label=openssf%20scorecard)](https://scorecard.dev/viewer/?uri=github.com/kubemoot/kmctl)

`kmctl` is the command-line tool for working with crews and Kubemoot: the scriptable
counterpart to [CrewForge for VS Code](https://github.com/kubemoot/vscode-crewforge). It
is a client of the Kubernetes resources the Kubemoot operator manages, with the same
flags and output formats as `kubectl`.

## Install

Install the latest release. Each [release](https://github.com/kubemoot/kmctl/releases)
holds a static binary per platform (`linux` and `darwin` on `amd64` and `arm64`, as
`.tar.gz`; `windows` as `.zip`), `checksums.txt`, and its signature.

### Linux and macOS

```bash
V=$(curl -fsSLo /dev/null -w '%{url_effective}' https://github.com/kubemoot/kmctl/releases/latest | sed 's|.*/v||')
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
BASE="https://github.com/kubemoot/kmctl/releases/download/v${V}"
cd "$(mktemp -d)" \
  && curl -fsSLO "${BASE}/kmctl_${V}_${OS}_${ARCH}.tar.gz" \
  && curl -fsSLO "${BASE}/checksums.txt" \
  && grep " kmctl_${V}_${OS}_${ARCH}.tar.gz\$" checksums.txt | if command -v sha256sum >/dev/null; then sha256sum -c -; else shasum -a 256 -c -; fi \
  && tar xzf "kmctl_${V}_${OS}_${ARCH}.tar.gz" kmctl \
  && sudo install kmctl /usr/local/bin/
kmctl version
```

### Windows

In PowerShell, in a new empty folder. The checksum is checked before the archive is extracted, and the command stops if it differs:

```powershell
$V = (Invoke-RestMethod https://api.github.com/repos/kubemoot/kmctl/releases/latest).tag_name.TrimStart('v')
$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$Zip = "kmctl_${V}_windows_$Arch.zip"
$Base = "https://github.com/kubemoot/kmctl/releases/download/v$V"
Invoke-WebRequest "$Base/$Zip" -OutFile $Zip
Invoke-WebRequest "$Base/checksums.txt" -OutFile checksums.txt
$Want = ((Get-Content checksums.txt | Where-Object { $_ -match ('\s' + [regex]::Escape($Zip) + '$') }) -split '\s+')[0]
if ((Get-FileHash $Zip -Algorithm SHA256).Hash -ne $Want) { throw "checksum mismatch for $Zip" }
Expand-Archive $Zip -DestinationPath .
.\kmctl.exe version
```

Then move `kmctl.exe` to a folder on your `PATH`.

### Verify the signature

Releases are signed without a stored key. With [cosign](https://docs.sigstore.dev/cosign/system_config/installation/),
run this in the directory the Linux and macOS commands left you in:

```bash
curl -fsSLO "${BASE}/checksums.txt.sigstore.json" && cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/kubemoot/kmctl/\.github/workflows/publish-release\.yaml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

A verified `checksums.txt` vouches for every archive in it. The release also carries
build provenance (`kmctl_<version>.intoto.jsonl`); verify it with
`gh attestation verify <archive> --repo kubemoot/kmctl`.

## First use

Point your kubeconfig at a cluster that runs Kubemoot, then:

```bash
kmctl status                 # is the cluster reachable and is Kubemoot installed?
kmctl version                # kmctl's version and the cluster's Kubemoot release
kmctl create demo            # scaffold a starter crew in ./demo
```

Shell completion for bash, zsh, fish, and PowerShell comes from `kmctl completion <shell>`;
the [user guide](https://kubemoot.org/docs/user-guides/kmctl/#shell-completion) shows how
to install it for each shell.

## Crew status and waiting

A crew reports two separate things. It is **reconciled** when its agents, gateways, and
policies exist as declared (`READY`). It **can answer** when a model in the cluster catalog
can run its agents now (`CAPACITY`, the `CapacityAvailable` condition). Accelerators come and
go, so a crew can be reconciled and still wait for one (phase `WaitingForCapacity`).

```bash
kmctl crew get demo -o wide    # READY, PHASE, CAPACITY, and the capacity reason
kmctl crew get demo            # also the capacity message and the last retention cleanup
kmctl crew wait demo           # wait until the crew can answer
kmctl crew wait demo --for reconciled   # wait only until it is reconciled as declared
```

While no model fits, `crew wait` prints `waiting for an accelerator:` and the scheduler's
reason, and keeps waiting; the crew answers when a model returns. `conversation ask` prints the
same note before it asks a crew that is waiting, and asks anyway. Against a Kubemoot that does
not report `CapacityAvailable` (0.95), `CAPACITY` shows `<none>` and a crew can answer when it
is reconciled and its phase is `Ready`.

## Changing things

The Kubemoot dashboard changes nothing in Kubernetes: it acts as you when you sign in with a
token (`kmctl dashboard token --token-service-account NAMESPACE/NAME` prints an eight-hour one) and
asks a crew through its gateway, nothing more. A change is made with your own credentials, and
kmctl makes the ones the dashboard used to. Each command that changes state takes `--dry-run`,
and each one that cannot be undone asks first unless you pass `--yes`. That includes
`kmctl delete`: it names what it will delete (`Delete crew/demo in namespace crew-demo?`) and
waits for `y`; `--yes` skips the question for scripts and `--dry-run` has the API server check
the delete and changes nothing. A mistyped subcommand in a group (`kmctl crew lst`) is an error
with a suggestion and exit code 1, not help.

`-o` means the output format (`table`, `yaml`, `json`, ...) on every command that has it.
A command that writes a file or a directory names it with its own flag: `--dir` for
`create` and `memory export`, `--output-file` for `fitness download` and
`conversation export`. The old `-o`/`--output` spelling on those four still works for one
release, prints a deprecation message, and is hidden from help. A value of `-` is refused:
these commands write a file or directory, never standard output.

Every command in this section, and `apply`, `delete`, `conversation ask` and `model add`,
needs a usable Kubernetes context and fails before doing anything without one: no
kubeconfig, no current context, a `--context` that is not in the kubeconfig, a context
whose user has no credentials, a cluster that cannot be reached, or credentials the API
server refuses. The error says what is missing; fix it with `kubectl config use-context NAME`,
`--context NAME` or `--kubeconfig PATH`. These commands refuse `--nats-url`, which would
write to a server that does not check who you are. Reading commands, and `create`,
`version` and `completion`, keep working as before (`memory export --nats-url` still reads
without a context).

| To do this | Run | It writes through |
|------------|-----|-------------------|
| Run a tier of a crew's fitness scenarios | `kmctl fitness run --tier smoke\|standard\|full --crew CREW` | a create of a `CrewFitnessSuite` with `spec.tier` (you need `create` on `crewfitnesssuites`); the operator chooses the scenarios when it starts the suite |
| Pause, resume or stop a fitness suite | `kmctl fitness pause\|resume\|stop SUITE` | a merge patch of the suite's spec (you need `patch` on `crewfitnesssuites`) |
| Pin or remove an MCP server report verdict | `kmctl mcp report pin REPORT --verdict use\|caution\|avoid`, `kmctl mcp report unpin REPORT` | a merge patch of the report's spec (you need `patch` on `mcpserverreports`) |
| Add, change, delete or clear crew memory | `kmctl memory add\|edit\|delete\|clear CREW ...` | the crew-memory NATS bucket, through a port-forward to the NATS Service (you need `create` on `pods/portforward` there) |
| Stop, pause or resume a conversation, or erase it | `kmctl conversation stop\|pause\|resume\|delete CREW ID` | the crew's discussion gateway, as the caller |
| Ask a conversation's first question again | `kmctl conversation replay CREW ID` | the crew's discussion gateway, like `conversation ask` |
| Remove a crew or a suite run (asks first; `--yes`, `--dry-run`) | `kmctl delete crew NAME`, `kmctl delete crewfitnesssuite NAME` | a delete of the resource; the operator cleans up |

```bash
kmctl fitness run --tier smoke --crew demo -n crew-demo   # a few scenarios that take every call path
kmctl fitness stop demo-starter --dry-run            # the API server checks it and your access
kmctl memory add demo hardware gpu0 "RTX 5090" -n crew-demo
kmctl memory clear demo -n crew-demo                 # asks, then deletes every fact of the crew
```

A value that looks like a credential is refused for `memory add` and `memory edit`.

Stopping, pausing, resuming and deleting a conversation go through the crew's discussion
gateway, which checks who is asking; kmctl does not publish raw messages to the bus or
purge a stream. The owner of a conversation may do all four. An operator needs the
ClusterRole `kubemoot-conversation-controller` and a `--reason`, which the gateway audits.

```bash
kmctl conversation stop|pause|resume homelab-pilot 6733c361 -n crew-homelab-pilot
kmctl conversation delete homelab-pilot 6733c361     # asks, then erases and prints what it removed
kmctl conversation delete homelab-pilot 6733c361 --force --yes --reason "erasure request 88"
```

`delete` removes the conversation from every store that holds it and prints the gateway's
report: what it removed per store, and what it cannot remove (logs and backups). A
conversation with a running discussion is refused (409) unless you pass `--force`, which stops
it first. If a store could not be cleared the report names it; repeat the delete. The gateway
has no dry run, so `delete` has no `--dry-run`; `conversation export --report` shows what a
conversation holds. Unloading a model is still not a kmctl command.

## Taking conversations away

```bash
kmctl conversation export homelab-pilot 6733c361                    # ./homelab-pilot-conversation-<id>.zip
kmctl conversation export homelab-pilot 6733c361 9f1e22a0 --output-file review.zip   # a zip of zips plus a manifest
kmctl conversation export homelab-pilot 6733c361 9f1e22a0 --report  # the manifest only, no zip
```

The operator's report service builds the zip on request and keeps no copy. kmctl port-forwards
to it (`kubemoot/kubemoot-operator-report`; `--operator-namespace` and `--operator-service`
name another) with your kubeconfig token, and you need `get` on the Crew. After a download of
several conversations, kmctl prints each ID the manifest lists as refused, expired or failed.
The file is written whole or not at all and never overwrites an existing one.

## Who is asking

`kmctl conversation` reaches a crew's discussion gateway through a port-forward to its pod
and sends your kubeconfig's bearer token with each request. A crew that sets
`spec.discussion.access` uses the token to know who you are: each conversation belongs to
the caller that started it, and `conversation list` shows yours. A crew that sets nothing
stays open and ignores the token. kmctl never prints or logs the token.

**Operators.** Reading another caller's conversation needs `get` (to read one) or `list`
(to list all) on the crew's virtual `crews/conversations` resource in the `kubemoot.ai`
group, which the Kubemoot chart's ClusterRole `kubemoot-conversation-reader` grants, and a
reason of 1 to 200 characters, which the gateway audits:

```bash
kmctl conversation list homelab-pilot --all --reason "weekly review of unanswered questions"
kmctl conversation watch homelab-pilot 6733c361-aa0c-4bde-85c6-e143af23b251 --reason "ticket 1234: wrong answer"
```

Without the permission, another caller's conversation answers 404, the same as one that does
not exist.

**Client-certificate kubeconfigs** (Talos, kind, k3s) carry no bearer token. When the crew
requires one, name a ServiceAccount you may `create` on `serviceaccounts/token`:

```bash
kmctl conversation ask homelab-pilot "hello" --token-service-account ops/crew-operator
```

kmctl requests a ten-minute token for that ServiceAccount (TokenRequest) and sends it. The
crew sees the ServiceAccount as the caller, and the Kubernetes audit log records who
requested the token. When the crew declares `tokenAudiences`, add `--token-audience`.
Without `--token-service-account`, kmctl sends no token, and a crew that requires one
answers 401 with a message that names this flag.

## Documentation

The [kmctl reference](https://kubemoot.org/docs/reference/kmctl/) describes every command,
flag, and output format, and the [kmctl user guide](https://kubemoot.org/docs/user-guides/kmctl/)
walks through a first crew. `kmctl --help` and `kmctl <command> --help` show the same
information in the terminal.

## Migrating to the workspace names

What Kubemoot called discussion artifacts is now the discussion's workspace. Update kmctl
together with Kubemoot, then:

1. Replace `kmctl conversation artifact CREW KEY` with `kmctl conversation workspace CREW KEY` in
   scripts. The old command still works, is hidden, and prints a deprecation warning on stderr; it
   is removed in the next release.
2. Read `workspace` (per event and per agent) and `workspaces` (in the summary) from
   `kmctl conversation -o json`. The old `artifact` and `artifacts` keys carry the same content for
   one release.
3. Nothing changes for `kmctl fitness download`: the results workbook is generated work and keeps
   its name.

kmctl asks the gateway's workspace route first and falls back to the old route when the gateway
does not have it, so a newer kmctl works against an older Kubemoot.

## Migrating to the accelerator names

Kubemoot names the accelerator, not the GPU. Update kmctl together with Kubemoot, then read
`accelerator` (per event, per activity and per agent) and `waitedForAccelerator` (in the summary)
from `kmctl conversation -o json`. The old `gpu` and `waitedForGpu` keys carry the same content
for one release and are removed in the next. kmctl reads `accelerator` from a gateway and falls
back to `gpu` from an older one. The `kmctl create` scaffold check flags `vramMib` (now
`acceleratorMemoryMiB`) and the `gpu-busy` reason (now `accelerator-busy`) through Kubemoot's
published deprecated names.

## Contributing

To build kmctl from source, run its tests, or send a change, see
[CONTRIBUTING.md](CONTRIBUTING.md). Kubemoot is an independent open-source project under
the Apache License 2.0; contributing, support, governance, the code of conduct, security
reporting, and releases are in the [Community section of kubemoot.org](https://kubemoot.org/docs/community/).
Ask questions in [GitHub Discussions](https://github.com/orgs/kubemoot/discussions).

## License

Apache License 2.0. See [LICENSE](LICENSE).
