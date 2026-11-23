package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -o is the output format wherever it is a flag: no command uses it for a path
// except the deprecated spelling, which is hidden and says what replaced it.
func TestOutputFlag_PathCommandsUseTheirOwnFlag(t *testing.T) {
	for _, c := range []struct{ path, flag string }{
		{"kmctl create", "dir"}, {"kmctl memory export", "dir"},
		{"kmctl fitness download", "output-file"}, {"kmctl conversation export", "output-file"},
	} {
		cmd, _, err := NewRootCommand().Find(strings.Fields(c.path)[1:])
		if err != nil {
			t.Fatal(err)
		}
		if cmd.Flags().Lookup(c.flag) == nil {
			t.Errorf("%s has no --%s", c.path, c.flag)
		}
		old := cmd.Flags().Lookup("output")
		if old == nil || !old.Hidden {
			t.Errorf("%s: -o/--output must be a hidden alias, got %+v", c.path, old)
		}
	}
}

func TestOutputFlag_DeprecatedSpellingStillWorksAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	got, code := runKmctl(t, runOpts{}, "create", "demo", "--no-input", "-o", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, got)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo")); err != nil {
		t.Errorf("the scaffold was not written to the -o directory: %v", err)
	}
	if !strings.Contains(got, "deprecated") || !strings.Contains(got, "--dir") {
		t.Errorf("no deprecation message naming --dir: %q", got)
	}
}

// A dash is not standard output for these commands; it must not become a file or
// directory named "-".
func TestOutputFlag_DashIsRejectedAndCreatesNothing(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	for _, args := range [][]string{
		{"memory", "export", "demo", "-o", "-"},
		{"memory", "export", "demo", "--dir", "-"},
		{"create", "demo", "--no-input", "--dir", "-"},
		{"fitness", "download", "s", "--output-file", "-"},
		{"conversation", "export", "demo", exportID1, "-o", "-"},
	} {
		got, code := runKmctl(t, runOpts{}, args...)
		if code != 1 || !strings.Contains(got, "cannot be -") {
			t.Errorf("%v: exit %d, stderr %q", args, code, got)
		}
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Errorf("created %v", entries)
	}
}

func TestPathFlagCheck(t *testing.T) {
	for _, ok := range []string{"", ".", "out", "./-", "a-b"} {
		if err := outputDirFlag.check(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	if err := outputFileFlag.check("-"); err == nil {
		t.Error("- accepted")
	}
}
