#!/usr/bin/env bash
# Proves a fresh `kmctl create` scaffold opens in VS Code with zero Problems, offline.
#
#   scripts/scaffold-in-editor.sh OPERATOR_DIR
#
# OPERATOR_DIR is the operator module of a kubemoot checkout (its config/crd/bases holds
# the CRDs and cmd/crd-schema writes their JSON Schemas, which a Kubemoot release
# publishes). The script scaffolds a bundle and a chart with five specialists and runs
# Red Hat's yaml-language-server, the engine of the Red Hat YAML extension, over each
# with the settings kmctl wrote to .vscode/settings.json (scripts/editor-check/check.mjs).
# Both must show no Problem. Two controls must show Problems, so a clean result means
# something: a misspelled field in an Agent, and the chart's templates read as plain
# YAML (what happens without the Helm association). Needs go and node (24) on PATH, and
# npm for the language server on a first run, and GNU sed. The schema is served from 127.0.0.1.
set -euo pipefail
cd "$(dirname "$0")/.."

operator="${1:?usage: scaffold-in-editor.sh OPERATOR_DIR}"
[ -d "${operator}/config/crd/bases" ] || { echo "ERROR: no CRDs at ${operator}/config/crd/bases" >&2; exit 1; }
command -v node >/dev/null || { echo "ERROR: node is not on PATH" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
check=scripts/editor-check

(cd "$check" && npm ci --ignore-scripts --no-audit --no-fund >/dev/null)
go build -o "${work}/kmctl" .
(cd "$operator" && go run ./cmd/crd-schema -crds config/crd/bases -out "${work}/schemas") >/dev/null

# shown CREW_DIR FLAGS...: runs the check, then prints its first Problems and the count.
shown() {
  local dir="$1" out
  shift
  out="$(node "${check}/check.mjs" "$dir" "${work}/schemas" "$@")"
  printf '%s\n' "$out" | sed -n '1,3p;$p'
}

for form in bundle chart; do
  flag=""
  [ "$form" = chart ] && flag="--chart"
  # shellcheck disable=SC2086 # $flag is empty or one word
  "${work}/kmctl" create "demo-${form}" --members 5 --no-input --dir "$work" $flag >/dev/null
  echo "== ${form}: expect no Problems"
  node "${check}/check.mjs" "${work}/demo-${form}" "${work}/schemas" --expect-clean
done

echo "== control: a misspelled field is a Problem"
sed -i '0,/^  capabilities:/s//  capabilitiez:/' "${work}/demo-bundle/agents.yaml"
shown "${work}/demo-bundle" --expect-errors

echo "== control: a chart's templates read as plain YAML are Problems"
shown "${work}/demo-chart" --expect-errors --no-helm-association
echo "scaffold-in-editor: ok"
