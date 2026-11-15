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

## Documentation

The [kmctl reference](https://kubemoot.org/docs/reference/kmctl/) describes every command,
flag, and output format, and the [kmctl user guide](https://kubemoot.org/docs/user-guides/kmctl/)
walks through a first crew. `kmctl --help` and `kmctl <command> --help` show the same
information in the terminal.

## Contributing

To build kmctl from source, run its tests, or send a change, see
[CONTRIBUTING.md](CONTRIBUTING.md). Kubemoot is an independent open-source project under
the Apache License 2.0; contributing, support, governance, the code of conduct, security
reporting, and releases are in the [Community section of kubemoot.org](https://kubemoot.org/docs/community/).
Ask questions in [GitHub Discussions](https://github.com/orgs/kubemoot/discussions).

## License

Apache License 2.0. See [LICENSE](LICENSE).
