package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubemoot/kmctl/internal/version"
)

// testRelease is the release the fake operator reports; any string shows it is passed through.
const testRelease = "7.7.7-test"

func versionConfig(status map[string]any) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       "KubemootConfig",
		"metadata":   map[string]any{"name": kubemootConfigName},
	}
	if status != nil {
		obj["status"] = status
	}
	return &unstructured.Unstructured{Object: obj}
}

func TestVersionCommand_Default(t *testing.T) {
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"version", "--client"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version returned error: %v", err)
	}
	if !strings.Contains(buf.String(), version.Get().Version) {
		t.Errorf("version output %q missing version %q", buf.String(), version.Get().Version)
	}
}

func TestVersionCommand_Short(t *testing.T) {
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"version", "--short"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version --short returned error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != version.Get().Version {
		t.Errorf("version --short = %q, want %q", got, version.Get().Version)
	}
}

func TestVersionCommand_RejectsArgs(t *testing.T) {
	root := NewRootCommand()
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"version", "extra-arg"})

	if err := root.Execute(); err == nil {
		t.Fatal("expected error when passing positional args to version, got nil")
	}
}

func TestVersionCommand_ClientAndServerLines(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("kubemootconfigs", "", kubemootConfigName, map[string]any{"status": map[string]any{"release": testRelease, "ready": true}})

	out, _, err := s.kmctl(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want a client line and a server line, got %q", out)
	}
	if !strings.HasPrefix(lines[0], "Client Version: kmctl "+version.Get().Version) {
		t.Errorf("client line = %q", lines[0])
	}
	if lines[1] != "Server Version: Kubemoot "+testRelease {
		t.Errorf("server line = %q", lines[1])
	}
}

func TestVersionCommand_ClientFlagSkipsTheCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	s.refuse = func(*http.Request) (int, string) { t.Error("--client contacted the cluster"); return 500, "no" }

	out, _, err := s.kmctl(t, "version", "--client")
	if err != nil {
		t.Fatalf("version --client: %v", err)
	}
	if strings.Contains(out, "Server Version") {
		t.Errorf("--client printed a server line: %q", out)
	}
}

func TestVersionCommand_StructuredOutput(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("kubemootconfigs", "", kubemootConfigName, map[string]any{"status": map[string]any{"release": testRelease}})

	out, _, err := s.kmctl(t, "version", "-o", "json")
	if err != nil {
		t.Fatalf("version -o json: %v", err)
	}
	var got versionReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json output %q: %v", out, err)
	}
	if got.ClientVersion.Version != version.Get().Version || got.ServerVersion == nil || got.ServerVersion.Release != testRelease {
		t.Errorf("report = %+v", got)
	}

	yamlOut, _, err := s.kmctl(t, "version", "-o", "yaml")
	if err != nil {
		t.Fatalf("version -o yaml: %v", err)
	}
	if !strings.Contains(yamlOut, "release: "+testRelease) || !strings.Contains(yamlOut, "clientVersion:") {
		t.Errorf("yaml output = %q", yamlOut)
	}
}

func TestVersionCommand_RejectsUnknownFormat(t *testing.T) {
	s := newFakeAPIServer(t)
	if _, _, err := s.kmctl(t, "version", "-o", "nonsense"); err == nil {
		t.Fatal("expected an error for an unsupported output format")
	}
}

// With no cluster to reach, the command still prints the client line and says so on the server line.
func TestVersionCommand_NoCluster(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close() // nothing listens at url any more
	kc := filepath.Join(t.TempDir(), "kubeconfig")
	cfg := "apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n    server: " + url +
		"\nusers:\n- name: u\n  user:\n    token: t\ncontexts:\n- name: c\n  context:\n    cluster: c\n    user: u\ncurrent-context: c\n"
	if err := os.WriteFile(kc, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"--kubeconfig", kc, "--cache-dir", t.TempDir(), "version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("version without a cluster must still succeed: %v", err)
	}
	if !strings.Contains(out.String(), "Client Version: kmctl "+version.Get().Version) {
		t.Errorf("client line missing: %q", out.String())
	}
	if !strings.Contains(out.String(), "Server Version: unavailable (cannot reach the cluster") {
		t.Errorf("server line should say the cluster is unreachable: %q", out.String())
	}
}

func TestVersionCommand_NoKubeconfig(t *testing.T) {
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(new(bytes.Buffer))
	empty := filepath.Join(t.TempDir(), "none")
	root.SetArgs([]string{"--kubeconfig", empty, "version"})
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if err := root.Execute(); err != nil {
		t.Fatalf("version without a kubeconfig must still succeed: %v", err)
	}
	if !strings.Contains(out.String(), "Client Version:") || !strings.Contains(out.String(), "Server Version: unavailable (") {
		t.Errorf("output = %q", out.String())
	}
}

func TestVersionCommand_NoKubemoot(t *testing.T) {
	s := newFakeAPIServer(t)
	out, _, err := s.kmctl(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out, "Server Version: unavailable (Kubemoot is not installed") {
		t.Errorf("output = %q", out)
	}
}

func TestVersionCommand_JSONWithoutServerCarriesTheReason(t *testing.T) {
	s := newFakeAPIServer(t)
	out, _, err := s.kmctl(t, "version", "-o", "json")
	if err != nil {
		t.Fatalf("version -o json: %v", err)
	}
	var got versionReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.ServerVersion != nil || got.ServerMessage == "" {
		t.Errorf("report = %+v, want no serverVersion and a serverMessage", got)
	}
}

func TestServerReleaseFrom(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "kubemoot.ai", Resource: "kubemootconfigs"}, "default", errors.New("denied"))
	cases := []struct {
		name        string
		obj         *unstructured.Unstructured
		getErr      error
		wantRelease string
		wantNote    string
	}{
		{"release reported", versionConfig(map[string]any{"release": testRelease}), nil, testRelease, ""},
		{"status not written yet", versionConfig(nil), nil, "", "has not written its status yet"},
		{"empty status", versionConfig(map[string]any{}), nil, "", "has not written its status yet"},
		{"old operator without the field", versionConfig(map[string]any{"ready": true, "message": "ok"}), nil, "", "does not report a release"},
		{"empty release", versionConfig(map[string]any{"release": ""}), nil, "", "does not report a release"},
		{"no KubemootConfig", nil, nil, "", "Kubemoot is not installed"},
		{"forbidden", nil, forbidden, "", "not allowed to read the KubemootConfig"},
		{"unreachable", nil, errors.New("connection refused"), "", "cannot reach the cluster"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []runtime.Object
			if tc.obj != nil {
				objs = append(objs, tc.obj)
			}
			dc := fakeDyn(objs...)
			dc.PrependReactor("get", "kubemootconfigs", func(k8stesting.Action) (bool, runtime.Object, error) {
				return tc.getErr != nil, nil, tc.getErr
			})
			got, note := serverReleaseFrom(context.Background(), dc)
			if tc.wantRelease != "" {
				if got == nil || got.Release != tc.wantRelease || note != "" {
					t.Fatalf("got (%v, %q), want release %q", got, note, tc.wantRelease)
				}
				return
			}
			if got != nil || !strings.Contains(note, tc.wantNote) {
				t.Fatalf("got (%v, %q), want a note containing %q", got, note, tc.wantNote)
			}
		})
	}
}
