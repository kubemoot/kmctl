// Package version holds build metadata, overridden at link time by goreleaser.
package version

// These values are stamped in via -ldflags (see .goreleaser.yaml / Makefile).
// They default to development placeholders when built with a plain `go build`.
var (
	// Version is the semantic version of the build.
	Version = "dev"
	// Commit is the git commit the build was produced from.
	Commit = "none"
	// Date is the build timestamp.
	Date = "unknown"
)
