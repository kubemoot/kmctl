#!/usr/bin/env bash
# Runs the scaffold-applies test: everything `kmctl create` writes (bundle and chart,
# 1 to 5 specialists) must apply, with strict field validation, to an API server
# loaded with Kubemoot's CRDs, and read back whole.
#
#   scripts/scaffold-applies.sh [CRD_DIR]
#
# CRD_DIR is the Kubemoot CRD bases directory to test against (a kubemoot checkout's
# operator/config/crd/bases). With no argument the CRDs come from the Kubemoot
# release go.mod pins (the operator module, which ships its CRDs), the Kubemoot this
# kmctl targets. Needs go and helm on PATH. A missing prerequisite fails the run.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "$#" -gt 0 ]; then
  crds="$1"
  echo "Testing against the CRDs in ${crds}" >&2
else
  # A fresh runner has not downloaded the module yet, and go list then reports no Dir.
  go mod download github.com/kubemoot/kubemoot/operator
  pin="$(go list -m -f '{{.Path}} {{.Version}}' github.com/kubemoot/kubemoot/operator)"
  dir="$(go list -m -f '{{.Dir}}' github.com/kubemoot/kubemoot/operator)"
  [ -n "${dir}" ] || { echo "ERROR: the operator module ${pin} could not be downloaded" >&2; exit 1; }
  crds="${dir}/config/crd/bases"
  echo "Testing against the CRDs of the pinned operator module: ${pin}" >&2
fi
[ -d "${crds}" ] || { echo "ERROR: no CRD directory at ${crds}" >&2; exit 1; }
command -v helm >/dev/null || { echo "ERROR: helm is not on PATH" >&2; exit 1; }

KUBEBUILDER_ASSETS="$(scripts/envtest-assets.sh)"
export KUBEBUILDER_ASSETS
export KMCTL_CRD_DIR="${crds}"
export KMCTL_REQUIRE_SCAFFOLD_APPLIES=1
exec go test -tags=integration -count=1 -v -run '^TestScaffoldApplies$' ./internal/integration/
