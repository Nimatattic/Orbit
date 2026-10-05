package config

import (
	"os"
	"path/filepath"
)

// EnvHome overrides the global configuration directory.
const EnvHome = "ORBIT_HOME"

// Home returns the Orbit home directory (~/.orbit by default, decision A9/B1).
func Home() string {
	if v := os.Getenv(EnvHome); v != "" {
		return v
	}
	base, err := os.UserHomeDir()
	if err != nil || base == "" {
		base = "."
	}
	return filepath.Join(base, ".orbit")
}

// GlobalConfigPath returns <home>/config.yaml.
func GlobalConfigPath() string { return filepath.Join(Home(), "config.yaml") }

// LocalConfigPath returns <workdir>/.orbit.yaml.
func LocalConfigPath(workDir string) string {
	if workDir == "" {
		workDir = "."
	}
	return filepath.Join(workDir, ".orbit.yaml")
}

// CredentialsPath returns <home>/credentials.yaml (mode 0600).
func CredentialsPath() string { return filepath.Join(Home(), "credentials.yaml") }

// CacheDir returns <home>/cache.
func CacheDir() string { return filepath.Join(Home(), "cache") }

// AuditDir returns <home>/audit.
func AuditDir() string { return filepath.Join(Home(), "audit") }

// BackupsDir returns <home>/backups.
func BackupsDir() string { return filepath.Join(Home(), "backups") }

// EnsureHome creates the Orbit directories with safe permissions.
func EnsureHome() error {
	for _, dir := range []string{Home(), CacheDir(), AuditDir(), BackupsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}
