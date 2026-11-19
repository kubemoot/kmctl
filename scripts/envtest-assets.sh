#!/usr/bin/env bash
# Prints the directory of the envtest API server binaries (KUBEBUILDER_ASSETS),
# installing setup-envtest into ./bin and downloading the binaries on first use.
# go.mod is the version source: setup-envtest comes from the release branch of the
# controller-runtime kmctl builds with, and the API server is the Kubernetes minor
# of its client-go.
set -euo pipefail
cd "$(dirname "$0")/.."

minor_of() { go list -m -f '{{.Version}}' "$1" | sed -E 's/^v([0-9]+)\.([0-9]+)\..*/\1.\2/'; }

GOBIN="$PWD/bin" go install "sigs.k8s.io/controller-runtime/tools/setup-envtest@release-$(minor_of sigs.k8s.io/controller-runtime)" >&2
k8s="1.$(minor_of k8s.io/client-go | cut -d. -f2)"
./bin/setup-envtest use "${k8s}" --bin-dir "$PWD/bin" -p path
