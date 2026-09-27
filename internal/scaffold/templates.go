package scaffold

// The generated scaffold is a minimal, valid starting point. Loose model
// coupling is preserved throughout: no Agent names a model; capabilities +
// CrewSchedulingPolicy drive selection at reconcile time.

const crewTemplate = `apiVersion: kubemoot.ai/v1alpha1
kind: Crew
metadata:
  name: {{ .Name }}
  labels:
    kubemoot.ai/crew: {{ .Name }}
  annotations:
    kubemoot.ai/manage-namespace: "true"
spec:
  description: "Scaffolded by kmctl create - customize before relying on it."
  discussion:
    enabled: true
---
apiVersion: kubemoot.ai/v1alpha1
kind: CrewSchedulingPolicy
metadata:
  name: {{ .Name }}-scheduling
  labels:
    kubemoot.ai/crew: {{ .Name }}
spec:
  crewRef: "{{ .Name }}"
  archetypeRef: consent-3
  # Per-capability quality bias in [0,1]: lower -> speed-lean, higher -> quality-lean.
  # Agents never name a model; the scheduler matches Models by capability label.
  qualityBias:
    reasoning: "0.7"
    tool-calling: "0.3"
    default: "0.4"
  rules:
    - phase: mulling
      require:
        matchLabels:
          capability/tool-calling: "true"
    - phase: triage
      require:
        matchLabels:
          capability/tool-calling: "true"
{{- if .HasFamily }}
  # Preferred model family chosen at scaffold time: {{ .ModelFamily }}.
  # To pin it, label your Models (e.g. model/family: {{ .ModelFamily }}) and add
  # that label under require.matchLabels above.
{{- end }}
{{- if .Providers }}
  # Providers selected at scaffold time:{{ range .Providers }} {{ . }}{{ end }}.
  # Provider allow-listing is a tune-up; by default agents use any ready provider.
{{- end }}
`

const agentsTemplate = `apiVersion: kubemoot.ai/v1alpha1
kind: Agent
metadata:
  name: {{ .Name }}-coordinator
  labels:
    kubemoot.ai/crew: {{ .Name }}
    kubemoot.ai/role: coordinator
spec:
  type: chat
  description: "Coordinator for the {{ .Name }} crew - facilitates the discussion."
  discussRole: coordinator
  capabilities: [tool-calling, reasoning]
  discussChannels: [general{{ range .Members }}, {{ .Channel }}{{ end }}]
  promptRefs: [advisory-prompt, coordinator-decision-logic, synthesis-prompt]
  temperature: "0.1"
  maxTokens: 4096
  deployment:
    replicas: 1
    port: 8080
{{- range .Members }}
---
apiVersion: kubemoot.ai/v1alpha1
kind: Agent
metadata:
  name: {{ .Name }}
  labels:
    kubemoot.ai/crew: {{ $.Name }}
    kubemoot.ai/role: tooler
spec:
  type: chat
  description: "Tooler {{ .Name }} - replace with a real responsibility."
  discussRole: tooler
  capabilities: [tool-calling]
  discussChannels: [{{ .Channel }}]
  promptRefs: [discussion-protocol, response-style, {{ .Name }}-system]
  temperature: "0.3"
  maxTokens: 4096
  deployment:
    replicas: 1
    port: 8080
{{- end }}
`

const promptModulesTemplate = `apiVersion: kubemoot.ai/v1alpha1
kind: PromptModule
metadata:
  name: discussion-protocol
  labels:
    kubemoot.ai/crew: {{ .Name }}
spec:
  order: 10
  content: |
    DEFINE DOMAIN discussion-protocol
    DESCRIPTION How agents take part in a consensus discussion over NATS

    ALWAYS publish a signal: agree, concern, block, or stand_aside
    WHEN you have nothing to add THEN publish stand_aside
    NEVER answer outside your declared responsibility
---
apiVersion: kubemoot.ai/v1alpha1
kind: PromptModule
metadata:
  name: response-style
  labels:
    kubemoot.ai/crew: {{ .Name }}
spec:
  order: 20
  content: |
    DEFINE DOMAIN response-style
    DESCRIPTION How to phrase contributions

    ALWAYS be concise and concrete
    ALWAYS report tool errors and empty results honestly
    NEVER fabricate names, values, or status
---
apiVersion: kubemoot.ai/v1alpha1
kind: PromptModule
metadata:
  name: advisory-prompt
  labels:
    kubemoot.ai/crew: {{ .Name }}
spec:
  order: 5
  content: |
    DEFINE DOMAIN coordinator-advisory
    DESCRIPTION Inline triage advice the coordinator forms before delegating

    WHEN a question arrives THEN identify which toolers are relevant
    NEVER wake a tooler whose responsibility does not match the question
    WHEN no tooler is relevant AND you can answer from your own stable knowledge THEN select NO toolers and put your COMPLETE, user-facing answer in the brief - it is returned to the user as-is
    WHEN the question is a meta-question about this crew itself (what it can do, its capabilities, how it can help) THEN select NO toolers and put a concise enumeration of the crew's domains, from the capability catalog, in the brief - the toolers cannot describe the crew
    NEVER select a tooler just to have someone respond - an unrelated question is answered directly or declined outright, not routed to an irrelevant tooler
---
apiVersion: kubemoot.ai/v1alpha1
kind: PromptModule
metadata:
  name: coordinator-decision-logic
  labels:
    kubemoot.ai/crew: {{ .Name }}
spec:
  order: 45
  content: |
    DEFINE DOMAIN coordinator-decision-logic
    DESCRIPTION How the coordinator settles a discussion and decides how to answer

    WHEN all woken toolers have responded or expired THEN move to synthesis
    NEVER conclude on a fixed timer - settle on signals
    WHEN no tooler is relevant AND the question is answerable from stable general knowledge (facts, concepts, definitions) THEN answer it directly from your own knowledge
    WHEN no tooler is relevant AND the question needs live cluster data or real-time external data this crew has no source for THEN say so honestly and NEVER fabricate a value
---
apiVersion: kubemoot.ai/v1alpha1
kind: PromptModule
metadata:
  name: synthesis-prompt
  labels:
    kubemoot.ai/crew: {{ .Name }}
spec:
  order: 50
  content: |
    DEFINE DOMAIN synthesis
    DESCRIPTION How the coordinator composes the final answer

    ALWAYS synthesize the toolers' contributions into one clear answer for the user
    WHEN no tooler contributed AND the question is stable general knowledge THEN answer it directly from your own knowledge
    WHEN no tooler contributed AND the question needs data this crew cannot obtain THEN say so plainly and NEVER fabricate a value
    ALWAYS write the answer as prose addressed directly to the user
    NEVER mention agent names, internal signals (agree, concern, stand_aside, block), or discussion mechanics in the answer
{{- range .Members }}
---
apiVersion: kubemoot.ai/v1alpha1
kind: PromptModule
metadata:
  name: {{ .Name }}-system
  labels:
    kubemoot.ai/crew: {{ $.Name }}
spec:
  order: 30
  content: |
    DEFINE DOMAIN {{ .Name }}
    DESCRIPTION Responsibility for tooler {{ .Name }} - replace this

    ALWAYS call a tool to get real data before answering
    ALWAYS answer only the specific question asked
    NEVER fabricate resource names, namespaces, or status values
{{- end }}
`

const modelsTemplate = `# Model CRs for the crew. The scheduler binds agents to these just-in-time, so a
# crew needs at least one Model in its namespace to run a discussion. Each Model
# binds a model name to a ModelProvider and is labeled by capability + latencyClass.
# These are starting defaults for the chosen family; edit model names/VRAM to match.
{{- if .HasModels }}
{{- range .Models }}
---
apiVersion: kubemoot.ai/v1alpha1
kind: Model
metadata:
  name: {{ .Name }}
  labels:
    kubemoot.ai/crew: {{ $.Name }}
    family: "{{ .Family }}"
    params: "{{ .Params }}"
    latencyClass: "{{ .LatencyClass }}"
    contextWindow: "{{ .ContextWindow }}"
    capability/tool-calling: "true"
    capability/reasoning: "true"
spec:
  model: "{{ .Model }}"
  providerRef: "{{ .ProviderRef }}"
  vramMib: {{ .VRAMMiB }}
{{- end }}
{{- else }}
# No Models were generated (no providers selected, or an unrecognized model family).
# Without Models the crew comes Ready but cannot run a discussion. Add at least one,
# pointing providerRef at one of your ModelProvider CRs (kmctl model list):
#
#   apiVersion: kubemoot.ai/v1alpha1
#   kind: Model
#   metadata:
#     name: {{ .Name }}-default
#     labels:
#       capability/tool-calling: "true"
#       capability/reasoning: "true"
#       latencyClass: "low"
#   spec:
#     model: "qwen3:8b"
#     providerRef: "<your-modelprovider>"
#     vramMib: 5120
{{- end }}
`

const fitnessTemplate = `apiVersion: kubemoot.ai/v1alpha1
kind: CrewFitnessSuite
metadata:
  name: {{ .Name }}-starter
  labels:
    kubemoot.ai/crew: {{ .Name }}
spec:
  crewRef: "{{ .Name }}"
  description: "Starter fitness suite scaffolded by kmctl create."
  iterations: 1
  scripts:
    - testRef: smoke-hello
      testContent: |
        DESCRIPTION Smoke test - the crew responds and the coordinator synthesizes.
        DEFINE CONST QUESTION AS "Introduce this crew and what it is for."
        DEFINE CONST MAX_DURATION AS 240 seconds
        ASSERT(POST to discussion endpoint returns 200 with conversationId)
        ASSERT(discussion completes with "done" event within MAX_DURATION)
        ASSERT(coordinator produces synthesis)
        ASSERT(synthesis is non-empty)
    - testRef: general-knowledge
      testContent: |
        DESCRIPTION General knowledge - the coordinator answers directly, no tools needed.
        DEFINE CONST QUESTION AS "What is the capital of France?"
        DEFINE CONST MAX_DURATION AS 240 seconds
        ASSERT(POST to discussion endpoint returns 200 with conversationId)
        ASSERT(discussion completes with "done" event within MAX_DURATION)
        ASSERT(synthesis CONTAINS "Paris")
    - testRef: honest-no-fabrication
      testContent: |
        DESCRIPTION Real-time external data the crew has no source for - it must decline, not invent a number.
        DEFINE CONST QUESTION AS "What is the current price of Bitcoin in US dollars right now?"
        DEFINE CONST MAX_DURATION AS 240 seconds
        ASSERT(POST to discussion endpoint returns 200 with conversationId)
        ASSERT(discussion completes with "done" event within MAX_DURATION)
        ASSERT(DEFER synthesis REFLECTS "The crew has no live market-data source and honestly says it cannot provide a current price; it does not fabricate a number.")
`

const chartTemplate = `apiVersion: v2
name: {{ .Name }}
description: The {{ .Name }} Kubemoot crew, scaffolded by kmctl create.
type: application
version: 0.1.0
annotations:
  kubemoot.ai/crew-chart: "true"
`

const valuesTemplate = `# The crew's manifests are plain YAML under templates/; Helm installs them into the
# release namespace. Move a setting here when you want it to vary per deployment.
`

const readmeTemplate = `# {{ .Name }} crew

Scaffolded by ` + "`kmctl create`" + `. This is a minimal, valid starting point -
customize it before relying on it.
{{- if .Chart }}

It is a Helm chart: the manifests are in ` + "`templates/`" + `, the starter fitness suite in
` + "`fitness/`" + `.
{{- end }}

## What is here

- ` + "`crew.yaml`" + ` - the Crew and its CrewSchedulingPolicy (loose model coupling:
  agents declare capabilities, the policy picks models at reconcile time).
- ` + "`agents.yaml`" + ` - a coordinator plus {{ len .Members }} tooler(s). Replace each
  tooler's description and responsibility with something real.
- ` + "`promptmodules.yaml`" + ` - shared protocol/style modules, the coordinator's
  modules, and a per-tooler system module (ADL). Flesh these out.
- ` + "`models.yaml`" + ` - Model CRs (the chosen family's sizes bound to your
  ModelProviders, labeled by capability + latencyClass). The scheduler binds agents
  to these; a crew needs at least one to run. Edit model names/VRAM to taste.
- ` + "`fitness.yaml`" + ` - a starter CrewFitnessSuite: a capability smoke test,
  a general-knowledge question the crew answers directly, and an honest
  don't-fabricate test (real-time data it has no source for).
{{- if .HasFamily }}
- Preferred model family: ` + "`{{ .ModelFamily }}`" + ` (see the policy comment to pin it).
{{- end }}
{{- if .Providers }}
- Selected providers:{{ range .Providers }} ` + "`{{ . }}`" + `{{ end }}.
{{- end }}

## Next steps

` + "```bash" + `
{{- if .Chart }}
helm upgrade --install {{ .Name }} . --namespace {{ .Name }} --create-namespace
kmctl crew status {{ .Name }} -n {{ .Name }}
kubectl apply -n {{ .Name }} -f fitness/
{{- else }}
kmctl apply -f .                 # create the resources
kmctl crew status {{ .Name }}    # watch it come Ready
kmctl fitness run {{ .Name }}-starter
{{- end }}
` + "```" + `
`
