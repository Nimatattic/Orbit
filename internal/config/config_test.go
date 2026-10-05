package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// decodeAndValidate runs the same decode+validate path Load uses after merging.
func decodeAndValidate(t *testing.T, doc map[string]any) (*Config, error) {
	t.Helper()
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	return cfg, cfg.Validate()
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMergesLocalOverGlobalAndAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "config.yaml"), "language: en-US\ndefaults:\n  clean:\n    older_than: 14d\n")
	write(t, filepath.Join(dir, ".orbit.yaml"), "defaults:\n  clean:\n    older_than: 3d\n    include_free: true\n")

	loaded, err := Load(Options{WorkDir: dir, Home: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg := loaded.Config

	if cfg.Language != "en-US" {
		t.Errorf("language: got %q, want en-US (from global)", cfg.Language)
	}
	if cfg.Defaults.Clean.OlderThan != "3d" {
		t.Errorf("older_than: got %q, want 3d (local overrides global)", cfg.Defaults.Clean.OlderThan)
	}
	if !cfg.Defaults.Clean.IncludeFree {
		t.Error("include_free: local value must win")
	}
	// Defaults that neither file mentions must come from code.
	if !cfg.Defaults.EdgeOne.ReplaceFreeCert {
		t.Error("edgeone.replace_free_cert should default to true (decision B2b)")
	}
	if cfg.Defaults.Strategy != "auto" {
		t.Errorf("strategy default: got %q", cfg.Defaults.Strategy)
	}
	if got := cfg.Defaults.Products; len(got) != 2 || got[0] != "cdn" || got[1] != "teo" {
		t.Errorf("products default: got %v", got)
	}
	if !cfg.Defaults.Clean.RequireFullEvidence {
		t.Error("clean.require_full_evidence should default to true")
	}
}

func TestExplicitConfigBeatsLocal(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.yaml")
	write(t, filepath.Join(dir, "config.yaml"), "language: zh-CN\n")
	write(t, filepath.Join(dir, ".orbit.yaml"), "language: zh-CN\n")
	write(t, explicit, "language: en-US\n")

	loaded, err := Load(Options{Explicit: explicit, WorkDir: dir, Home: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Config.Language != "en-US" {
		t.Fatalf("got %q, want en-US", loaded.Config.Language)
	}
}

func TestUseLocalRequiresFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(Options{WorkDir: dir, Home: dir, UseLocal: true}); err == nil {
		t.Fatal("expected an error when --local is set but no project file exists")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "7d", want: 7 * 24 * time.Hour},
		{in: "1d12h", want: 36 * time.Hour},
		{in: "30m", want: 30 * time.Minute},
		{in: "", want: 0},
		{in: "soon", wantErr: true},
	}
	for _, tc := range tests {
		got, err := ParseDuration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: expected an error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"version": 1,
			"defaults": map[string]any{
				"strategy": "auto",
				"products": []string{"cdn"},
				"timeouts": map[string]any{"api": "15s", "deploy_poll": "5m"},
				"clean":    map[string]any{"older_than": "7d"},
			},
			"platforms": map[string]any{
				"tencent": map[string]any{
					"accounts": []map[string]any{
						{"alias": "main"},
					},
				},
			},
		}
	}

	t.Run("unknown product", func(t *testing.T) {
		doc := base()
		doc["defaults"].(map[string]any)["products"] = []string{"clb"}
		if _, err := decodeAndValidate(t, doc); err == nil || !strings.Contains(err.Error(), "unsupported product") {
			t.Fatalf("got %v, want an unsupported product error", err)
		}
	})

	t.Run("unknown strategy", func(t *testing.T) {
		doc := base()
		doc["defaults"].(map[string]any)["strategy"] = "yolo"
		if _, err := decodeAndValidate(t, doc); err == nil || !strings.Contains(err.Error(), "unknown strategy") {
			t.Fatalf("got %v, want an unknown strategy error", err)
		}
	})

	t.Run("duplicate alias", func(t *testing.T) {
		doc := base()
		doc["platforms"].(map[string]any)["tencent"] = map[string]any{
			"accounts": []map[string]any{{"alias": "main"}, {"alias": "main"}},
		}
		if _, err := decodeAndValidate(t, doc); err == nil || !strings.Contains(err.Error(), "duplicate account") {
			t.Fatalf("got %v, want a duplicate account error", err)
		}
	})

	t.Run("env credential needs variable names", func(t *testing.T) {
		doc := base()
		doc["platforms"].(map[string]any)["tencent"] = map[string]any{
			"accounts": []map[string]any{{"alias": "main", "credential": map[string]any{"source": "env"}}},
		}
		if _, err := decodeAndValidate(t, doc); err == nil || !strings.Contains(err.Error(), "env") {
			t.Fatalf("got %v, want a credential source error", err)
		}
	})
}
