package version

import (
	"runtime/debug"
	"testing"
)

var placeholders = Info{Version: devVersion, Commit: noneCommit, Date: unknownDate}

func buildInfo(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Path: "github.com/kubemoot/kmctl", Version: mainVersion}, Settings: settings}
}

func TestResolve_GoInstallUsesModuleVersion(t *testing.T) {
	got := resolve(placeholders, buildInfo("v0.12.3"))
	if got.Version != "0.12.3" {
		t.Errorf("Version = %q, want %q", got.Version, "0.12.3")
	}
	if got.Commit != noneCommit || got.Date != unknownDate {
		t.Errorf("Commit/Date = %q/%q, want placeholders when no VCS settings", got.Commit, got.Date)
	}
}

func TestResolve_PseudoVersionKeptVerbatimMinusPrefix(t *testing.T) {
	got := resolve(placeholders, buildInfo("v0.0.0-20260929120000-abcdef123456"))
	if got.Version != "0.0.0-20260929120000-abcdef123456" {
		t.Errorf("Version = %q", got.Version)
	}
}

func TestResolve_LdflagsWin(t *testing.T) {
	stamped := Info{Version: "1.2.3", Commit: "abc1234", Date: "2026-09-29T00:00:00Z"}
	bi := buildInfo("v9.9.9",
		debug.BuildSetting{Key: "vcs.revision", Value: "ffffffffffffffff"},
		debug.BuildSetting{Key: "vcs.time", Value: "2000-01-01T00:00:00Z"})
	if got := resolve(stamped, bi); got != stamped {
		t.Errorf("resolve overrode ldflags values: got %+v, want %+v", got, stamped)
	}
}

func TestResolve_LocalBuildUsesVCSSettings(t *testing.T) {
	bi := buildInfo(develModule,
		debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef"},
		debug.BuildSetting{Key: "vcs.time", Value: "2026-09-29T12:00:00Z"})
	got := resolve(placeholders, bi)
	want := Info{Version: devVersion, Commit: "0123456", Date: "2026-09-29T12:00:00Z"}
	if got != want {
		t.Errorf("resolve = %+v, want %+v", got, want)
	}
}

func TestResolve_UnexpectedInputs(t *testing.T) {
	cases := map[string]struct {
		bi   *debug.BuildInfo
		want Info
	}{
		"nil build info":    {nil, placeholders},
		"empty main":        {buildInfo(""), placeholders},
		"devel main":        {buildInfo(develModule), placeholders},
		"empty vcs values":  {buildInfo("", debug.BuildSetting{Key: "vcs.revision"}, debug.BuildSetting{Key: "vcs.time"}), placeholders},
		"short revision":    {buildInfo("", debug.BuildSetting{Key: "vcs.revision", Value: "abc"}), Info{devVersion, "abc", unknownDate}},
		"unrelated setting": {buildInfo("", debug.BuildSetting{Key: "GOOS", Value: "linux"}), placeholders},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := resolve(placeholders, tc.bi); got != tc.want {
				t.Errorf("resolve = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestGet_NeverEmpty(t *testing.T) {
	got := Get()
	if got.Version == "" || got.Commit == "" || got.Date == "" {
		t.Errorf("Get returned an empty field: %+v", got)
	}
}
