// Package version holds build metadata injected at link time via -ldflags.
package version

// These values are overridden by the Makefile:
//
//	-X github.com/Crequency/orbit/internal/version.Version=...
var (
	// Version is the semantic version or git describe output.
	Version = "dev"
	// Commit is the short git commit hash.
	Commit = "none"
	// Date is the build timestamp (RFC3339).
	Date = "unknown"
)

// String returns a human readable one-line version string.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
