DEFINE DOMAIN kmctl
DESCRIPTION The Kubemoot CLI (Go + Cobra). Inherits ecosystem rules from the parent .claude/CLAUDE.md files.

# Role

DEFINE COMPONENT cli-role
DESCRIPTION kmctl is a convenience client, not a control plane

ASSERT kmctl is the command-line tool for working with crews and Kubemoot - the scriptable counterpart to the CrewForge IDE
ASSERT everything Kubemoot manages is a Kubernetes resource; kmctl is a typed client over those resources plus live-stream helpers
NEVER implement lifecycle logic in kmctl (namespace create/delete, Job management, cascading cleanup) - the operator owns that via finalizers and owner refs
WHEN a write is needed THEN apply/delete the Kubemoot CRDs (server-side apply); let the operator reconcile
ASSERT mirrors the CrewForge boundary (see parent architecture-boundaries) - a CRD editor + read helpers, nothing more

# UX consistency

DEFINE COMPONENT cli-ux
DESCRIPTION Look and feel consistent with kubectl / istioctl / helm

ASSERT command structure is HYBRID noun-verb (the istioctl/helm consensus): resources are noun-first and scoped (crew list/get, agent list/get, prompt/model, fitness list/get/scenarios/run, conversation ask/watch) so `kmctl <noun> --help` shows a clean per-resource action list and completion scales; cross-cutting actions are global verbs (create, apply -f, delete, status, info, version, completion)
NEVER add a top-level generic verb like `get`/`describe` that takes a resource type - it fractures the noun-verb model (you would have both `kmctl get crews` and `kmctl crew list`). apply/delete are the only kubectl-style verbs, and only because they are type-agnostic manifest ops (like `helm install`)
ASSERT use Cobra + genericclioptions; global flags --kubeconfig, --context, -n/--namespace, -A, -o {table|yaml|json|name|wide}, -v
ASSERT every command has a short + long description and runnable Examples in --help
ASSERT shell completion (bash/zsh/fish/powershell) stays enabled (Cobra built-in completion command)
ASSERT live discussion/fitness streaming is over HTTP/SSE using the active kubectl context + credentials - NEVER client-side NATS
ASSERT loosely bound - no homelab topology baked in; discover providers/models/namespaces from the cluster at runtime

# Quality and tests

DEFINE COMPONENT cli-quality
DESCRIPTION Gates from day one

ASSERT golangci-lint enforces gocyclo CC<=10 + dupl (see parent code-quality-gates)
ASSERT run gates: make lint (golangci-lint), make test (unit tests with coverage)
ASSERT every command lands WITH its unit tests (Cobra command tests + fake-dynamic-client coverage of its cluster code path) - see parent testing-policy
ASSERT three test tiers (see TESTING.md): (1) unit+mocks per-push CI - fast, no cluster, no operator, uses the client-go fake dynamic client for list/get/delete paths; (2) envtest scheduled/dispatch for CRD schema drift; (3) e2e dogfood in Kubemoot's own e2e, driving Kubemoot via kmctl
ASSERT per-push CI MUST stay fast, MUST NOT require a running operator, and MUST NEVER touch or disrupt the homelab cluster (there is no staging cluster) - mock the cluster, never reach for the real one in CI
ASSERT kmctl uses the dynamic client, so DRIFT (CRD changes) is the main test risk - the drift guard is Tier 2 envtest against current CRDs + Tier 3 dogfood, NOT per-push CI
ASSERT fix what SonarQube flags (.github/workflows/quality.yaml) - see parent quality-authority
ASSERT goreleaser publishes binaries on push to main (conventional commits drive the SemVer bump) - see parent gitops-versioning

# Types

DEFINE COMPONENT cli-types
DESCRIPTION One source of truth for CRD schemas

ASSERT import the operator's api/v1alpha1 Go types rather than redefining CRD schemas
WHEN the operator's API package is not importable THEN add the (additive) exposure in the operator, do not copy types
