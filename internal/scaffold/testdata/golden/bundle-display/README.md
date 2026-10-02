# Lab-Ops "Crew" #2

Its Kubernetes name is `lab-ops-crew-2`: the crew's objects are named after it.

A small, complete Kubemoot crew scaffolded by `kmctl create`: a read-only guide to its own
Kubernetes namespace. Ask it what is running, what is wrong, and why; it reads the
namespace with real tools and answers from what it found. It never changes anything.

It works on any cluster with Kubemoot installed and needs no configuration: everything
it reads is in the namespace you install it into, starting with its own pods.

## The crew

- **coordinator** (`lab-ops-crew-2-coordinator`) picks the specialists for each question and writes the answer.
- **workloads** (`lab-ops-crew-2-workloads`, tooler): Pods, Deployments, ReplicaSets, StatefulSets, and Jobs in the crew's namespace.

Scaffold with `--members 5` for the full crew of five specialists: workloads, events,
networking, config, and a reviewer.

## Your first five minutes

**1. Install it.**

This bundle is written for the namespace `crew-lab-ops-crew-2`: its prompts and its RoleBinding name it.
For another namespace, scaffold again with `kmctl create lab-ops-crew-2 -n <namespace>`.

```bash
kubectl create namespace crew-lab-ops-crew-2
kubectl apply -n crew-lab-ops-crew-2 -f access/
kmctl apply -n crew-lab-ops-crew-2 -f .
kmctl crew get lab-ops-crew-2 -n crew-lab-ops-crew-2   # wait for Ready
```

**2. Ask it something.**

```bash
kmctl conversation ask lab-ops-crew-2 "List the pods in this namespace and their status." -n crew-lab-ops-crew-2
```

The answer names the crew's own pods: you can check it with `kubectl get pods -n crew-lab-ops-crew-2`.
Ask it to delete something and it declines: it is read-only by its prompts and by its RBAC.

**3. Run its fitness suite.**

```bash
kmctl fitness run -f fitness/fitness.yaml -n crew-lab-ops-crew-2
kmctl fitness get lab-ops-crew-2-starter -n crew-lab-ops-crew-2
```

Each scenario asks a question whose answer the namespace itself proves. Delete the suite
before running it again: `kubectl delete crewfitnesssuite lab-ops-crew-2-starter -n crew-lab-ops-crew-2`.

**4. Change one rule and see the difference.** The crew's behavior is the ADL in
`promptmodules.yaml`. In the `synthesis-prompt` module, which shapes
the coordinator's answer, add one rule:

```text
ALWAYS end with a one-line summary that starts "In short:"
```

Apply it again (`kmctl apply -n crew-lab-ops-crew-2 -f .`); the operator rolls the agents. Ask the
same question: the answer now ends with that line.

## What is here

- `crew.yaml`: the Crew and its CrewSchedulingPolicy.
- `agents.yaml`: the coordinator and the specialists. Agents declare capabilities, never a
  model; the policy binds Models to them.
- `promptmodules.yaml`: every prompt, in ADL.
- `tools.yaml`: the Kubernetes MCP server, read-only, and the MCP gateway the agents reach it through.
- `access/rbac.yaml`: the read-only Role the tool server runs with (apply it with kubectl).
- `models.yaml`: the Models the scheduler binds agents to.
- `fitness/fitness.yaml`: the fitness suite.

Model family: `qwen`.
Model providers: `ollama`.

## Read-only by design

The tool server runs with `--read-only`, so it offers no tool that changes the cluster, and its
Role grants only get, list, and watch. Secrets are not readable at all: Kubernetes cannot
grant a Secret's name without its data, so the crew names Secrets from the references in
pod specs instead.
