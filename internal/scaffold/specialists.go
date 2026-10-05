package scaffold

import (
	"fmt"
	"strings"
)

// MaxMembers is the largest starter crew: four toolers and the reviewer.
const MaxMembers = 5

// specialist is one member of the starter crew beside the coordinator. The crew
// is a read-only guide to its own namespace, so every specialist owns one slice
// of that namespace and carries only the tools for it.
type specialist struct {
	Key          string   // short name; the Agent is <crew>-<Key>
	Role         string   // discussRole: tooler or analyst
	Summary      string   // what the specialist covers, for people (Agent description, README, CrewForge)
	Triage       string   // what the coordinator's selection step reads
	Capabilities []string // declared capabilities; the scheduler binds a Model by these
	Tools        []string // enabledTools on the crew's Kubernetes MCP server
	Keywords     []string // discussKeywords: words added to the resume the coordinator matches by meaning, not a routing gate
	Namespaced   scope    // what it covers and how it gathers when the crew reads its own namespace
	ClusterWide  scope    // the same with access.clusterWide; zero when its rules do not depend on scope (see scoped)
	Rules        string   // the rest of its <crew>-<Key>-system PromptModule, the same in either scope
}

// scoped reports whether the specialist's prompt depends on how much of the cluster
// the crew reads. One that is not (the reviewer, which has no tools) keeps its
// one DESCRIPTION in Namespaced and renders it in either scope.
func (s specialist) scoped() bool { return s.ClusterWide != scope{} }

// scope is the part of a specialist's prompt that depends on how much of the
// cluster the crew reads: its DESCRIPTION and its gather rules.
type scope struct {
	Description string
	Gather      string // the rules of its gather component; "" for none
}

// specialists is the starter crew in the order members adds them: --members N
// takes the first N, so every size from 1 to MaxMembers is a working crew.
var specialists = []specialist{workloads, events, networking, config, reviewer}

var workloads = specialist{
	Key:          "workloads",
	Role:         "tooler",
	Summary:      "Pods, Deployments, ReplicaSets, StatefulSets, and Jobs.",
	Triage:       "Lists and inspects Pods, Deployments, ReplicaSets, StatefulSets, and Jobs: phase, readiness, replicas, conditions, and logs.",
	Capabilities: []string{"tool-calling", "kubernetes"},
	Tools:        []string{"pods_list_in_namespace", "pods_get", "pods_log", "resources_list", "resources_get"},
	Keywords:     []string{"pod", "pods", "deployment", "deployments", "replicaset", "statefulset", "job", "jobs", "replicas", "workload", "logs", "running"},
	Namespaced: scope{
		Description: "Pods, Deployments, ReplicaSets, StatefulSets, and Jobs in this crew's namespace",
		Gather: `WHEN asked about pods THEN call pods_list_in_namespace with this crew's namespace
WHEN asked about one pod THEN call pods_get with its name and namespace
WHEN asked about Deployments, ReplicaSets, or StatefulSets THEN call resources_list with apiVersion "apps/v1", the kind, and the namespace
WHEN asked about Jobs THEN call resources_list with apiVersion "batch/v1", kind "Job", and the namespace
WHEN asked about one named workload THEN call resources_get for it and read its status
WHEN a pod is not Running or not ready THEN read its last 20 log lines with pods_log`,
	},
	ClusterWide: scope{
		Description: "Pods, Deployments, ReplicaSets, StatefulSets, and Jobs in every namespace of the cluster",
		Gather: `WHEN asked about pods THEN call resources_list with apiVersion "v1" and kind "Pod"
WHEN asked about one pod THEN call pods_get with its name and namespace
WHEN asked about Deployments, ReplicaSets, or StatefulSets THEN call resources_list with apiVersion "apps/v1" and the kind
WHEN asked about Jobs THEN call resources_list with apiVersion "batch/v1" and kind "Job"
WHEN asked about one named workload THEN call resources_get for it in its namespace and read its status
WHEN a pod is not Running or not ready THEN read its last 20 log lines with pods_log`,
	},
	Rules: `DEFINE COMPONENT report
WHEN reporting a pod THEN give its name, phase, and ready containers (for example 1/1)
WHEN reporting a Deployment THEN give desired, ready, updated, and available replicas, and its Available and Progressing conditions with their reasons
ASSERT a Deployment is healthy when its ready replicas equal its desired replicas and Available is True
NEVER report a pod or workload the tools did not return`,
}

var events = specialist{
	Key:          "events",
	Role:         "tooler",
	Summary:      "Warning events, container restarts, and recent failures.",
	Triage:       "Reads Warning events, container restart counts, and the reasons for recent failures (crashes, failed probes, failed pulls).",
	Capabilities: []string{"tool-calling", "kubernetes"},
	Tools:        []string{"events_list", "pods_list_in_namespace", "pods_get", "pods_log"},
	Keywords:     []string{"event", "events", "warning", "warnings", "restart", "restarts", "crash", "failing", "failed", "error", "unhealthy", "problem"},
	Namespaced: scope{
		Description: "Warning events, container restarts, and recent failures in this crew's namespace",
		Gather: `WHEN asked what is wrong, failing, unhealthy, or recent THEN call events_list with this crew's namespace and keep the Warning events
WHEN you report Warning events THEN also call pods_list_in_namespace, so each warning sits beside the current state of its pod
WHEN asked about restarts THEN call pods_list_in_namespace, then pods_get for each pod, and read restartCount and lastState in its containerStatuses
WHEN a container has restarted THEN read its previous log with pods_log and previous set to true
WHEN a Warning event names a pod THEN call pods_get for that pod and report whether it has recovered`,
	},
	ClusterWide: scope{
		Description: "Warning events, container restarts, and recent failures in every namespace of the cluster",
		Gather: `WHEN asked what is wrong, failing, unhealthy, or recent THEN call events_list and keep the Warning events
WHEN a Warning event names a pod THEN call pods_get for that pod in the event's namespace, so the warning sits beside the pod's current state, and report whether it has recovered
WHEN asked about restarts in one namespace THEN call pods_list_in_namespace with that namespace, then pods_get for each pod, and read restartCount and lastState in its containerStatuses
WHEN asked about restarts across the cluster THEN call events_list, keep the BackOff events, and call pods_get for each pod they name to read restartCount and lastState
WHEN a container has restarted THEN read its previous log with pods_log and previous set to true`,
	},
	Rules: `DEFINE COMPONENT report
ASSERT an event records something that happened; whether it is still happening is the pod's current state
ALWAYS report each Warning event as: object, reason, message, count, last seen, and whether its pod is Running and ready now
ALWAYS report each restarted container as: pod, container, restart count, last termination reason
WHEN a Warning names a pod that is now Running and ready THEN report it as past and cleared (a startup probe, a slow image pull), not as a current failure
WHEN there are no Warning events and no restarts THEN say so plainly; that is a healthy finding
NEVER treat a Normal event as a problem`,
}

var networking = specialist{
	Key:          "networking",
	Role:         "tooler",
	Summary:      "Services, endpoints, Ingresses or HTTPRoutes, and NetworkPolicies.",
	Triage:       "Lists Services and whether they have ready endpoints, Ingresses, HTTPRoutes, and NetworkPolicies.",
	Capabilities: []string{"tool-calling", "kubernetes"},
	Tools:        []string{"resources_list", "resources_get"},
	Keywords:     []string{"service", "services", "endpoint", "endpoints", "port", "ports", "ingress", "httproute", "route", "networkpolicy", "network", "traffic", "dns"},
	Namespaced: scope{
		Description: "Services, endpoints, Ingresses, HTTPRoutes, and NetworkPolicies in this crew's namespace",
		Gather: `WHEN asked about Services THEN call resources_list with apiVersion "v1", kind "Service", and this crew's namespace
WHEN asked whether a Service has backends THEN call resources_list with apiVersion "discovery.k8s.io/v1", kind "EndpointSlice", and the namespace, and count the ready endpoints labeled with that Service's name
WHEN asked about ingress or routes THEN list kind "Ingress" (apiVersion "networking.k8s.io/v1") AND kind "HTTPRoute" (apiVersion "gateway.networking.k8s.io/v1")
WHEN asked about NetworkPolicies THEN call resources_list with apiVersion "networking.k8s.io/v1", kind "NetworkPolicy", and the namespace
WHEN a tool says a kind is not known to the cluster THEN report that this cluster does not serve that API; that is a finding, not an error`,
	},
	ClusterWide: scope{
		Description: "Services, endpoints, Ingresses, HTTPRoutes, and NetworkPolicies in every namespace of the cluster",
		Gather: `WHEN asked about Services THEN call resources_list with apiVersion "v1" and kind "Service"
WHEN asked whether a Service has backends THEN call resources_list with apiVersion "discovery.k8s.io/v1", kind "EndpointSlice", and the Service's namespace, and count the ready endpoints labeled with that Service's name
WHEN asked about ingress or routes THEN list kind "Ingress" (apiVersion "networking.k8s.io/v1") AND kind "HTTPRoute" (apiVersion "gateway.networking.k8s.io/v1")
WHEN asked about NetworkPolicies THEN call resources_list with apiVersion "networking.k8s.io/v1" and kind "NetworkPolicy"
WHEN a tool says a kind is not known to the cluster THEN report that this cluster does not serve that API; that is a finding, not an error`,
	},
	Rules: `DEFINE COMPONENT report
ALWAYS report a Service as: name, type, ports, ready endpoints
ASSERT a Service with zero ready endpoints sends its traffic nowhere; report it as a problem
WHEN a namespace has no NetworkPolicy THEN say that traffic in that namespace is not restricted
NEVER report a Service, route, or policy the tools did not return`,
}

var config = specialist{
	Key:          "config",
	Role:         "tooler",
	Summary:      "ConfigMaps, ServiceAccounts, and the Secrets workloads reference.",
	Triage:       "Lists ConfigMaps and their keys, ServiceAccounts and the pods that use them, and the names of the Secrets workloads reference.",
	Capabilities: []string{"tool-calling", "kubernetes"},
	Tools:        []string{"resources_list", "resources_get"},
	Keywords:     []string{"configmap", "configmaps", "config", "configuration", "secret", "secrets", "serviceaccount", "serviceaccounts", "account", "env", "volume"},
	Namespaced: scope{
		Description: "ConfigMaps, ServiceAccounts, and the Secrets workloads reference in this crew's namespace",
		Gather: `WHEN asked about ConfigMaps THEN call resources_list with apiVersion "v1", kind "ConfigMap", and this crew's namespace
WHEN asked about ServiceAccounts THEN call resources_list with apiVersion "v1", kind "ServiceAccount", and the namespace, then match each to the pods whose spec.serviceAccountName names it
WHEN asked about Secrets THEN read the pods with resources_list (apiVersion "v1", kind "Pod") and name the Secrets they reference: volumes, env secretKeyRef, envFrom secretRef, and imagePullSecrets`,
	},
	ClusterWide: scope{
		Description: "ConfigMaps, ServiceAccounts, and the Secrets workloads reference in every namespace of the cluster",
		Gather: `WHEN asked about ConfigMaps THEN call resources_list with apiVersion "v1" and kind "ConfigMap"
WHEN asked about ServiceAccounts THEN call resources_list with apiVersion "v1" and kind "ServiceAccount", then match each to the pods in its namespace whose spec.serviceAccountName names it
WHEN asked about Secrets THEN read the pods with resources_list (apiVersion "v1", kind "Pod") and name the Secrets they reference: volumes, env secretKeyRef, envFrom secretRef, and imagePullSecrets`,
	},
	Rules: `DEFINE COMPONENT secrets
ASSERT this crew has no permission on Secrets: Kubernetes cannot grant names without data, so it grants nothing
ALWAYS say that Secret names come from the references in workloads, not from reading the Secrets
NEVER attempt to read a Secret, and NEVER guess a secret value

DEFINE COMPONENT report
ALWAYS report a ConfigMap as its name and its data keys; give values only when asked
ALWAYS report a ServiceAccount as its name and the pods that use it
NEVER report a ConfigMap, ServiceAccount, or Secret the tools did not show`,
}

var reviewer = specialist{
	Key:          "reviewer",
	Role:         "analyst",
	Summary:      "Checks the answer against the data the other specialists gathered and flags unsupported claims.",
	Triage:       "Reviews the findings the other specialists gathered: checks that each claim is supported by tool output, reconciles contradictions, and draws the conclusion (healthy or not, and why).",
	Capabilities: []string{"reasoning"},
	Keywords:     []string{"healthy", "unhealthy", "why", "check", "review", "summary", "overall"},
	Namespaced: scope{
		Description: "Checks the specialists' findings before the answer goes to the user",
	},
	Rules: `ASSERT you are an analyst: you have NO tools, and the findings earlier in this discussion are your only data
ASSERT a finding that is only an [ARTIFACT ...] marker holds data you cannot see; the coordinator reads it when it writes the answer
WHEN a finding is only an [ARTIFACT ...] marker THEN name that specialist as unseen, and NEVER conclude that its resources are missing, healthy, or unhealthy
ASSERT a Warning event records something that happened; a resource is unhealthy now only when its current state says so (not Running, containers not ready, replicas short)
WHEN a Warning event names a pod AND no finding you can read shows that pod's current state THEN say the warning's current impact is unconfirmed; NEVER call the pod unhealthy from the event alone
WHEN the findings answer the question THEN confirm that in one line and state the conclusion they support (a count, what is healthy, what is not and why)
WHEN two findings contradict each other THEN name both and say which one the tool output supports
WHEN a claim names a resource, number, or status that no finding contains THEN flag it as unsupported
WHEN the findings are not enough to answer THEN say exactly what is missing
NEVER add a resource, number, or status of your own
NEVER respond NOTHING_TO_ADD only because the question needs live data: the live data is already in the findings`,
}

// agent is a specialist as the templates render it, named for its crew.
type agent struct {
	specialist
	Name string
}

// CapabilityList, ToolList, and KeywordList render as YAML flow sequences.
func (a agent) CapabilityList() string { return flow(a.Capabilities) }
func (a agent) ToolList() string       { return flow(a.Tools) }
func (a agent) KeywordList() string    { return flow(a.Keywords) }

// IsAnalyst reports whether the specialist reasons over gathered data with no tools.
func (a agent) IsAnalyst() bool { return a.Role == "analyst" }

// Prompt is the content of the specialist's <crew>-<Key>-system PromptModule,
// indented for its block scalar. A bundle reads one namespace, so it gets the
// namespaced rules; a chart holds both scopes behind access.clusterWide, so a
// widened crew's specialists gather across namespaces and a namespaced crew's do not.
func (a agent) Prompt(chart bool) string {
	head := "DEFINE DOMAIN " + a.Key + "\n"
	rules := "\n\n" + indent(promptIndent, a.Rules)
	if !chart || !a.scoped() {
		return indent(promptIndent, head) + indent(promptIndent, a.Namespaced.text()) + rules
	}
	return indent(promptIndent, head) +
		helmIfClusterWide + "\n" + indent(promptIndent, a.ClusterWide.text()) + "\n" +
		helmElse + "\n" + indent(promptIndent, a.Namespaced.text()) + "\n" +
		helmEnd + rules
}

// The Helm actions of the access switch, as the chart's prompts write them, one per line.
const (
	helmIfClusterWide    = "{{- if .Values.access.clusterWide }}"
	helmIfNotClusterWide = "{{- if not .Values.access.clusterWide }}"
	helmElse             = "{{- else }}"
	helmEnd              = "{{- end }}"
)

// promptIndent is the depth of a PromptModule's content block scalar.
const promptIndent = 4

// text renders the scope as prompt lines: the DESCRIPTION, then the gather component.
func (s scope) text() string {
	out := "DESCRIPTION " + s.Description
	if s.Gather != "" {
		out += "\n\nDEFINE COMPONENT gather\n" + s.Gather
	}
	return out
}

func flow(items []string) string {
	return "[" + strings.Join(items, ", ") + "]"
}

// crewAgents returns the first n specialists, named for the crew.
func crewAgents(crew string, n int) []agent {
	out := make([]agent, 0, n)
	for _, s := range specialists[:n] {
		out = append(out, agent{specialist: s, Name: fmt.Sprintf("%s-%s", crew, s.Key)})
	}
	return out
}

// SizeSummary describes the starter crew of each size, one line per specialist, for
// prompts and help text: "workloads: Pods, Deployments, ...".
func SizeSummary(n int) []string {
	n = min(max(n, 0), MaxMembers)
	lines := make([]string, 0, n)
	for _, s := range specialists[:n] {
		lines = append(lines, s.Key+": "+s.Summary)
	}
	return lines
}
