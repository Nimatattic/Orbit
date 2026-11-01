// Package version holds build metadata injected at link time via -ldflags.
package version

// These values are overridden by the Makefile:
//
//	-X github.com/Nimatattic/Orbit/internal/version.Version=...
var (
	// Version is the semantic version or git describe output. It tracks the
	// release tag; the Makefile and scripts/build-dist.sh stamp it at link time so
	// that a plain "go build" still reports a meaningful version.
	Version = "v0.1.0"
	// Commit is the short git commit hash.
	Commit = "none"
	// Date is the build timestamp (RFC3339).
	Date = "unknown"
)

// String returns a human readable one-line version string.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
