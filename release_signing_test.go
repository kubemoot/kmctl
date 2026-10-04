package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// The release signing contract: goreleaser signs checksums.txt keylessly and leaves the
// release a draft, Promote Release attests the provenance and attaches it before the
// draft is published, and only the job that signs holds an OIDC token.

type wfStep struct {
	Name string         `json:"name"`
	ID   string         `json:"id"`
	If   string         `json:"if"`
	Uses string         `json:"uses"`
	Run  string         `json:"run"`
	With map[string]any `json:"with"`
}

type wfJob struct {
	Needs       any               `json:"needs"`
	If          string            `json:"if"`
	Permissions map[string]string `json:"permissions"`
	Steps       []wfStep          `json:"steps"`
}

type workflow struct {
	Permissions map[string]string `json:"permissions"`
	Jobs        map[string]wfJob  `json:"jobs"`
}

type goreleaserConfig struct {
	Signs []struct {
		Cmd       string   `json:"cmd"`
		Artifacts string   `json:"artifacts"`
		Signature string   `json:"signature"`
		Args      []string `json:"args"`
	} `json:"signs"`
	Release struct {
		Draft bool `json:"draft"`
	} `json:"release"`
}

func loadYAML(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, into); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func loadWorkflow(t *testing.T, name string) workflow {
	t.Helper()
	var wf workflow
	loadYAML(t, ".github/workflows/"+name, &wf)
	return wf
}

// stepIndex is the position of the named step, failing the test when it is missing.
func stepIndex(t *testing.T, job wfJob, name string) int {
	t.Helper()
	for i, s := range job.Steps {
		if s.Name == name {
			return i
		}
	}
	t.Fatalf("no step %q", name)
	return -1
}

func TestGoreleaserSignsChecksumsKeylessly(t *testing.T) {
	var cfg goreleaserConfig
	loadYAML(t, ".goreleaser.yaml", &cfg)
	if len(cfg.Signs) != 1 {
		t.Fatalf("want one signs entry, got %d", len(cfg.Signs))
	}
	sign := cfg.Signs[0]
	if sign.Cmd != "cosign" || sign.Artifacts != "checksum" || sign.Signature != "${artifact}.sigstore.json" {
		t.Errorf("unexpected signs entry: %+v", sign)
	}
	args := strings.Join(sign.Args, " ")
	for _, want := range []string{"sign-blob", "--bundle=${signature}", "${artifact}", "--yes"} {
		if !strings.Contains(args, want) {
			t.Errorf("cosign args %q lack %q", args, want)
		}
	}
	if strings.Contains(args, "--key") {
		t.Errorf("cosign args %q use a stored key; signing must be keyless", args)
	}
	if !cfg.Release.Draft {
		t.Error("the release must stay a draft so the provenance is attached before publishing")
	}
}

func TestPromoteSignsVerifiesAndAttestsBeforePublishing(t *testing.T) {
	promote := loadWorkflow(t, "promote-release.yaml").Jobs["promote"]
	install := stepIndex(t, promote, "Install cosign")
	snapshot := stepIndex(t, promote, "Build a snapshot (dry run)")
	publish := stepIndex(t, promote, "Publish the release with goreleaser")
	verify := stepIndex(t, promote, "Verify the checksums signature")
	attest := stepIndex(t, promote, "Attest the build provenance")
	if install >= snapshot || install >= publish || verify <= publish || attest <= verify {
		t.Errorf("step order: install %d, snapshot %d, publish %d, verify %d, attest %d", install, snapshot, publish, verify, attest)
	}
	if v := promote.Steps[verify]; v.If != "" || !strings.Contains(v.Run, "cosign verify-blob dist/checksums.txt") {
		t.Errorf("the signature must be verified on every run: %+v", v)
	}
	a := promote.Steps[attest]
	if !strings.Contains(a.If, "!inputs.dry_run") || fmt.Sprint(a.With["subject-checksums"]) != "dist/checksums.txt" {
		t.Errorf("provenance must cover the released archives on a real run only: %+v", a)
	}
	if !strings.HasPrefix(a.Uses, "actions/attest@") {
		t.Errorf("attest step uses %q", a.Uses)
	}
	name := promote.Steps[stepIndex(t, promote, "Name the provenance for the release")]
	if !strings.Contains(name.Run, "kmctl_${FINAL_TAG#v}.intoto.jsonl") {
		t.Errorf("the provenance asset must end in .intoto.jsonl: %q", name.Run)
	}
}

func TestPublishAttachesProvenanceThenPublishesTheDraft(t *testing.T) {
	job := loadWorkflow(t, "promote-release.yaml").Jobs["publish"]
	if job.Needs != "promote" || !strings.Contains(job.If, "!inputs.dry_run") {
		t.Errorf("publish must follow promote on a real run only: needs %v, if %q", job.Needs, job.If)
	}
	verify := stepIndex(t, job, "Verify the draft's archives against the provenance")
	attach := stepIndex(t, job, "Attach the provenance and publish the release")
	if verify >= attach || !strings.Contains(job.Steps[verify].Run, "gh attestation verify") {
		t.Error("the archives must verify against the provenance before the release is published")
	}
	run := job.Steps[attach].Run
	upload, edit := strings.Index(run, "gh release upload"), strings.Index(run, "--draft=false")
	if upload < 0 || edit < 0 || upload > edit {
		t.Errorf("the provenance must be uploaded before the draft is published: %q", run)
	}
}

func TestOnlyThePromoteJobHoldsAnOIDCToken(t *testing.T) {
	wf := loadWorkflow(t, "promote-release.yaml")
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("workflow permissions %v, want contents: read only", wf.Permissions)
	}
	for name, job := range wf.Jobs {
		holds := job.Permissions["id-token"] == "write"
		if holds != (name == "promote") {
			t.Errorf("job %s id-token write: %v", name, holds)
		}
	}
	if p := wf.Jobs["promote"].Permissions; p["attestations"] != "write" {
		t.Errorf("promote permissions %v lack attestations: write", p)
	}
	if p := wf.Jobs["publish"].Permissions; len(p) != 1 || p["contents"] != "write" {
		t.Errorf("publish permissions %v, want contents: write only", p)
	}
}

func TestCandidateBuildSignsNothing(t *testing.T) {
	job := loadWorkflow(t, "release.yaml").Jobs["release"]
	if _, ok := job.Permissions["id-token"]; ok {
		t.Error("the candidate build must not request an OIDC token")
	}
	build := job.Steps[stepIndex(t, job, "Build the candidate with goreleaser (no publish)")]
	if args := fmt.Sprint(build.With["args"]); !strings.Contains(args, "--skip=publish,announce,sign") {
		t.Errorf("candidate goreleaser args %q must skip signing", args)
	}
}

// stepUsing is the first step whose action starts with prefix.
func stepUsing(t *testing.T, job wfJob, prefix string) wfStep {
	t.Helper()
	for _, s := range job.Steps {
		if strings.HasPrefix(s.Uses, prefix) {
			return s
		}
	}
	t.Fatalf("no step uses %s", prefix)
	return wfStep{}
}

func TestProvenanceHandOffUsesOneArtifactAndOneFileName(t *testing.T) {
	wf := loadWorkflow(t, "promote-release.yaml")
	promote, publish := wf.Jobs["promote"], wf.Jobs["publish"]
	up := stepUsing(t, promote, "actions/upload-artifact@")
	down := stepUsing(t, publish, "actions/download-artifact@")
	if fmt.Sprint(up.With["name"]) != fmt.Sprint(down.With["name"]) {
		t.Errorf("artifact names differ: upload %v, download %v", up.With["name"], down.With["name"])
	}
	const file = "kmctl_${TAG#v}.intoto.jsonl"
	named := promote.Steps[stepIndex(t, promote, "Name the provenance for the release")].Run
	if !strings.Contains(strings.ReplaceAll(named, "FINAL_TAG", "TAG"), file) {
		t.Errorf("promote names the provenance differently: %q", named)
	}
	for _, name := range []string{"Verify the draft's archives against the provenance", "Attach the provenance and publish the release"} {
		if run := publish.Steps[stepIndex(t, publish, name)].Run; !strings.Contains(run, fmt.Sprint(down.With["path"])+"/"+file) {
			t.Errorf("step %q does not use %s/%s: %q", name, down.With["path"], file, run)
		}
	}
}

func TestPromoteRunsOnlyFromMain(t *testing.T) {
	// The signing identity in the README is promote-release.yaml@refs/heads/main.
	if got := loadWorkflow(t, "promote-release.yaml").Jobs["promote"].If; got != "github.ref == 'refs/heads/main'" {
		t.Errorf("promote job guard %q", got)
	}
}
