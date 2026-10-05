package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeAPIServer is an in-memory stand-in for the Kubernetes API server: discovery
// for the kubemoot.ai group, reads and writes of its resources, Services, and the
// service proxy. Commands run against it end to end through a kubeconfig, so the
// tests cover the path from flags to clients to output that the fake dynamic
// client skips.
type fakeAPIServer struct {
	mu         sync.Mutex
	objs       map[string]map[string]any   // by objKey
	services   map[string][]map[string]any // by namespace
	proxy      map[string]http.HandlerFunc // by "<service>/<path>"
	noKubemoot bool                        // the cluster does not serve kubemoot.ai
	// react, when set, stands in for the operator: it sees each object the server
	// stores on create or apply and may change it (set a status) before it is stored.
	react func(obj map[string]any)

	srv        *httptest.Server
	kubeconfig string
}

// kubemootKinds maps each kubemoot.ai resource the fake serves to its kind.
var kubemootKinds = map[string]string{
	"crews":             "Crew",
	"agents":            "Agent",
	"promptmodules":     "PromptModule",
	"modelproviders":    "ModelProvider",
	"models":            "Model",
	"crewfitnesssuites": "CrewFitnessSuite",
	"crewfitnesses":     "CrewFitness",
}

const kubemootAPIPath = "/apis/kubemoot.ai/v1alpha1"

// contextNS is the namespace of the kubeconfig's current context.
const contextNS = "team-a"

// newFakeAPIServer starts a fake API server and writes a kubeconfig whose current
// context points at it with namespace contextNS.
func newFakeAPIServer(t *testing.T) *fakeAPIServer {
	t.Helper()
	s := &fakeAPIServer{
		objs:     map[string]map[string]any{},
		services: map[string][]map[string]any{},
		proxy:    map[string]http.HandlerFunc{},
	}
	s.srv = httptest.NewServer(s)
	t.Cleanup(s.srv.Close)
	s.kubeconfig = filepath.Join(t.TempDir(), "kubeconfig")
	kc := `apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: ` + s.srv.URL + `
users:
- name: fake
  user:
    token: fake-token
contexts:
- name: fake
  context:
    cluster: fake
    user: fake
    namespace: ` + contextNS + `
current-context: fake
`
	if err := os.WriteFile(s.kubeconfig, []byte(kc), 0o600); err != nil {
		t.Fatal(err)
	}
	return s
}

// kmctl runs one kmctl command line against the server and returns what it wrote
// to stdout and stderr.
func (s *fakeAPIServer) kmctl(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCommand()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--kubeconfig", s.kubeconfig, "--cache-dir", t.TempDir()}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// add stores a kubemoot.ai object, keyed by its resource, namespace, and name.
func (s *fakeAPIServer) add(plural, ns, name string, fields map[string]any) {
	obj := map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       kubemootKinds[plural],
		"metadata":   map[string]any{"name": name, "namespace": ns, "creationTimestamp": "2026-01-01T00:00:00Z"},
	}
	for k, v := range fields {
		obj[k] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objs[objKey(plural, ns, name)] = obj
}

// get returns a kubemoot.ai object stored in the context's namespace, or nil.
func (s *fakeAPIServer) get(plural, name string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objs[objKey(plural, contextNS, name)]
}

func objKey(plural, ns, name string) string { return plural + "/" + ns + "/" + name }

func (s *fakeAPIServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if doc, ok := s.discovery()[p]; ok {
		writeJSON(w, http.StatusOK, doc)
		return
	}
	switch {
	case strings.HasPrefix(p, kubemootAPIPath+"/") && !s.noKubemoot:
		s.serveKubemoot(w, r, strings.TrimPrefix(p, kubemootAPIPath+"/"))
	case strings.HasPrefix(p, "/api/v1/namespaces/"):
		s.serveCore(w, r, strings.Split(strings.TrimPrefix(p, "/api/v1/namespaces/"), "/"))
	default:
		writeStatus(w, http.StatusNotFound, "NotFound", "no route for "+p)
	}
}

// discovery returns the discovery documents by path: the server version, the
// core group, and the kubemoot.ai group unless the cluster lacks it.
func (s *fakeAPIServer) discovery() map[string]any {
	groups := []any{}
	docs := map[string]any{
		"/version": map[string]any{"major": "1", "minor": "33", "gitVersion": "v1.33.1"},
		"/api":     map[string]any{"kind": "APIVersions", "versions": []string{"v1"}},
		"/api/v1": map[string]any{"kind": "APIResourceList", "groupVersion": "v1", "resources": []any{
			map[string]any{"name": "services", "singularName": "service", "namespaced": true, "kind": "Service", "verbs": []string{"get", "list"}},
		}},
	}
	if !s.noKubemoot {
		gv := map[string]any{"groupVersion": "kubemoot.ai/v1alpha1", "version": "v1alpha1"}
		groups = append(groups, map[string]any{"name": "kubemoot.ai", "versions": []any{gv}, "preferredVersion": gv})
		docs[kubemootAPIPath] = kubemootResourceList()
	}
	docs["/apis"] = map[string]any{"kind": "APIGroupList", "apiVersion": "v1", "groups": groups}
	return docs
}

func kubemootResourceList() map[string]any {
	resources := []any{}
	for plural, kind := range kubemootKinds {
		resources = append(resources, map[string]any{
			"name": plural, "singularName": strings.ToLower(kind), "namespaced": true, "kind": kind,
			"verbs": []string{"get", "list", "create", "patch", "delete"},
		})
	}
	return map[string]any{"kind": "APIResourceList", "groupVersion": "kubemoot.ai/v1alpha1", "resources": resources}
}

// serveKubemoot serves namespaces/<ns>/<plural>[/<name>] and <plural> (every namespace).
func (s *fakeAPIServer) serveKubemoot(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	ns := ""
	if len(parts) >= 3 && parts[0] == "namespaces" {
		ns, parts = parts[1], parts[2:]
	}
	plural, name := parts[0], ""
	if len(parts) > 1 {
		name = parts[1]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		s.read(w, plural, ns, name)
	case http.MethodPost:
		s.create(w, r, plural, ns)
	case http.MethodPatch:
		s.apply(w, r, plural, ns)
	case http.MethodDelete:
		s.remove(w, plural, ns, name)
	default:
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (s *fakeAPIServer) read(w http.ResponseWriter, plural, ns, name string) {
	if name != "" {
		obj, ok := s.objs[objKey(plural, ns, name)]
		if !ok {
			writeNotFound(w, plural, name)
			return
		}
		writeJSON(w, http.StatusOK, obj)
		return
	}
	keys := []string{}
	for key := range s.objs {
		if strings.HasPrefix(key, plural+"/") && (ns == "" || strings.HasPrefix(key, plural+"/"+ns+"/")) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys) // a stable order, as the API server lists by key
	items := make([]any, 0, len(keys))
	for _, key := range keys {
		items = append(items, s.objs[key])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1", "kind": kubemootKinds[plural] + "List",
		"metadata": map[string]any{}, "items": items,
	})
}

// create stores a POSTed object, naming it from generateName as the API server does.
func (s *fakeAPIServer) create(w http.ResponseWriter, r *http.Request, plural, ns string) {
	obj, ok := decodeBody(w, r)
	if !ok {
		return
	}
	meta := obj["metadata"].(map[string]any)
	if meta["name"] == nil {
		meta["name"] = meta["generateName"].(string) + "x7k2p"
	}
	meta["namespace"] = ns
	s.store(plural, obj)
	writeJSON(w, http.StatusCreated, obj)
}

// apply stands in for server-side apply: the patch is the whole object. A dry run
// answers without storing.
func (s *fakeAPIServer) apply(w http.ResponseWriter, r *http.Request, plural, ns string) {
	obj, ok := decodeBody(w, r)
	if !ok {
		return
	}
	obj["metadata"].(map[string]any)["namespace"] = ns
	if r.URL.Query().Get("dryRun") == "" {
		s.store(plural, obj)
	}
	writeJSON(w, http.StatusOK, obj)
}

func (s *fakeAPIServer) store(plural string, obj map[string]any) {
	if s.react != nil {
		s.react(obj)
	}
	meta := obj["metadata"].(map[string]any)
	s.objs[objKey(plural, meta["namespace"].(string), meta["name"].(string))] = obj
}

func (s *fakeAPIServer) remove(w http.ResponseWriter, plural, ns, name string) {
	key := objKey(plural, ns, name)
	if _, ok := s.objs[key]; !ok {
		writeNotFound(w, plural, name)
		return
	}
	delete(s.objs, key)
	writeJSON(w, http.StatusOK, map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Success"})
}

// serveCore serves <ns>/services[/<name>] and the service proxy,
// <ns>/services/<name>:<port>/proxy/<path>.
func (s *fakeAPIServer) serveCore(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 2 || parts[1] != "services" {
		writeStatus(w, http.StatusNotFound, "NotFound", "only services are served")
		return
	}
	ns := parts[0]
	if len(parts) >= 4 && parts[3] == "proxy" {
		service, _, _ := strings.Cut(parts[2], ":")
		s.serveProxy(w, r, service+"/"+strings.Join(parts[4:], "/"))
		return
	}
	s.mu.Lock()
	items := s.services[ns]
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"kind": "ServiceList", "apiVersion": "v1", "metadata": map[string]any{}, "items": items})
}

func (s *fakeAPIServer) serveProxy(w http.ResponseWriter, r *http.Request, key string) {
	s.mu.Lock()
	h, ok := s.proxy[key]
	s.mu.Unlock()
	if !ok {
		writeStatus(w, http.StatusServiceUnavailable, "ServiceUnavailable", "no endpoints for "+key)
		return
	}
	h(w, r)
}

func decodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body, err := io.ReadAll(r.Body)
	var obj map[string]any
	if err == nil {
		err = json.Unmarshal(body, &obj)
	}
	if err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return nil, false
	}
	return obj, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeStatus(w http.ResponseWriter, code int, reason, message string) {
	writeJSON(w, code, map[string]any{
		"kind": "Status", "apiVersion": "v1", "status": "Failure",
		"message": message, "reason": reason, "code": code,
	})
}

func writeNotFound(w http.ResponseWriter, plural, name string) {
	writeStatus(w, http.StatusNotFound, "NotFound", plural+`.kubemoot.ai "`+name+`" not found`)
}
