# Testing strategy

kmctl is a thin client over Kubemoot's Kubernetes CRDs (via the dynamic client, no
compile-time dependency on the operator's CRD Go types; the `memory` commands import only
the operator's public crew-memory packages). The risk that comes with that
freedom is **drift**: if Kubemoot's CRDs change, kmctl can break silently. The testing
tiers below are designed around that risk and around one hard constraint:

> **Per-push CI must stay fast, must not require a running Kubemoot operator, and must
> never touch or disrupt the homelab cluster.** There is no staging cluster.

## Tier 1 - Unit + mocks (per-push CI)

Runs on every push/PR via `go test ./...` (and `make test`). Fast, fully isolated.

- Pure logic: output formatting, table rendering, field extraction, manifest parsing,
  API-group detection.
- CLI mechanics: command construction, flag/arg validation, help, completion presence.
- **Mocked cluster paths**: the client-go **fake dynamic client** + a static RESTMapper
  exercise the real `resource.List`/`Get` and `deleteByKindName` code paths in memory.

- **Commands that change state**: patches go to the fake dynamic client, which records
  the patch and its options, so a test proves the exact spec change, that `--dry-run`
  sends a server-side dry run and stores nothing, and that a Forbidden answer reads as
  "you are not allowed to patch ...". The `memory` write commands run against an
  in-memory bucket, and one test runs them against an in-process NATS server with
  JetStream (`nats-server`, a test-only dependency), so the key-value calls are real.

This is where coverage is maximized. Mocks let us test the cluster-calling code without a
cluster. What mocks can NOT model: real CRD schema validation and server-side-apply
upsert (the fake does not upsert on apply).

## Tier 2 - envtest (optional; scheduled/dispatch, NOT per-push)

`go test -tags=integration` against an **ephemeral, throwaway apiserver+etcd** started by
controller-runtime envtest. It loads the **current** Kubemoot CRDs (fetched read-only at
run time), so it catches schema/GVR drift and validates real server-side apply. It needs
no operator and never touches the homelab cluster, but it is not instant, so the drift
tests run on a **schedule + manual dispatch**, not on every push. Status: running nightly.

One envtest test is the exception and gates releases: `TestScaffoldApplies`
(`scripts/scaffold-applies.sh`, `make test-scaffold-applies`). It applies everything
`kmctl create` writes (bundle and chart, one to five specialists) with strict field
validation and reads each object back, so it proves the scaffold makes a crew the Kubemoot
API accepts. It runs on every push against Kubemoot `main`'s CRDs and in Publish Release
(dry runs too) against the CRDs of the Kubemoot `go.mod` pins. The scripts set
`KMCTL_REQUIRE_SCAFFOLD_APPLIES`, so a missing prerequisite (helm, CRDs, API server
binaries) fails the gate instead of skipping it.

The scaffold's editor files have a second gate, `scripts/scaffold-in-editor.sh OPERATOR_DIR`.
It runs Red Hat's yaml-language-server (the engine of the Red Hat YAML extension) over a
bundle and a chart with the settings kmctl wrote, against the JSON Schemas generated from a
Kubemoot checkout's CRDs, and requires zero Problems. Two controls must show Problems (a
misspelled field, and a chart's templates read as plain YAML), so a clean result is not
vacuous. The schema is served from 127.0.0.1 and nothing else leaves the machine. It runs on
every push against Kubemoot `main`.

## Tier 3 - e2e dogfood (manual; lives in Kubemoot's e2e)

The real "does kmctl still work against real Kubemoot" check belongs in **Kubemoot's own
e2e suite**, which already has a cluster context. Those tests should **drive Kubemoot via
kmctl** (create a crew, run a fitness suite to completion, then delete all) - eating our
own dog food. This is long-running, runs against a live cluster deliberately (never in
kmctl's per-push CI), and is self-isolating (own namespace + cleanup). Status: planned
(a card in the kubemoot repo).

## Summary

| Tier | Where | When | Needs cluster? | Needs operator? | Catches |
|---|---|---|---|---|---|
| 1 unit + mocks | kmctl CI | every push | no | no | logic, mechanics, client code paths |
| 2 envtest | kmctl (scheduled; scaffold test every push and release) | nightly/dispatch | no (ephemeral) | no | CRD schema drift, real apply |
| 3 e2e dogfood | kubemoot e2e | manual | yes (live) | yes | end-to-end reality, real drift |
