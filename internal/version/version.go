// Package version holds build metadata. Release builds stamp it at link time
// (goreleaser); builds without ldflags, such as `go install`, fall back to the
// module and VCS metadata the Go toolchain embeds in the binary.
package version

import (
	"runtime/debug"
	"strings"
)

// Placeholder values used when neither ldflags nor build info supply a value.
const (
	devVersion  = "dev"
	noneCommit  = "none"
	unknownDate = "unknown"
	// develModule is what runtime/debug reports for the main module when the
	// binary was built from a local checkout rather than a versioned module.
	develModule = "(devel)"
	shortSHALen = 7
)

// These values are stamped in via -ldflags (see .goreleaser.yaml / Makefile).
var (
	// Version is the semantic version of the build.
	Version = devVersion
	// Commit is the git commit the build was produced from.
	Commit = noneCommit
	// Date is the build timestamp.
	Date = unknownDate
)

// Info is the resolved build metadata reported by `kmctl version`.
type Info struct {
	Version string
	Commit  string
	Date    string
}

// Get returns the build metadata, preferring ldflags values and filling any
// placeholder from the binary's embedded build info.
func Get() Info {
	bi, _ := debug.ReadBuildInfo() // bi is nil when build info is unavailable
	return resolve(Info{Version: Version, Commit: Commit, Date: Date}, bi)
}

// resolve fills placeholder fields of stamped from bi. A nil bi leaves stamped
// unchanged.
func resolve(stamped Info, bi *debug.BuildInfo) Info {
	if bi == nil {
		return stamped
	}
	if stamped.Version == devVersion {
		stamped.Version = moduleVersion(bi.Main.Version)
	}
	settings := vcsSettings(bi.Settings)
	if stamped.Commit == noneCommit {
		stamped.Commit = shortCommit(settings["vcs.revision"])
	}
	if stamped.Date == unknownDate && settings["vcs.time"] != "" {
		stamped.Date = settings["vcs.time"]
	}
	return stamped
}

// moduleVersion converts a module version such as "v0.12.0" into the
// goreleaser form "0.12.0". Empty and "(devel)" versions yield "dev".
func moduleVersion(v string) string {
	if v == "" || v == develModule {
		return devVersion
	}
	return strings.TrimPrefix(v, "v")
}

// shortCommit abbreviates a full revision; an empty revision yields "none".
func shortCommit(rev string) string {
	if rev == "" {
		return noneCommit
	}
	if len(rev) > shortSHALen {
		return rev[:shortSHALen]
	}
	return rev
}

func vcsSettings(settings []debug.BuildSetting) map[string]string {
	m := make(map[string]string, len(settings))
	for _, s := range settings {
		m[s.Key] = s.Value
	}
	return m
}
