package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAboutDrawsLogoAndFacts checks the identity card renders the artwork and the
// facts, and stays free of ANSI codes when stdout is not a terminal.
func TestAboutDrawsLogoAndFacts(t *testing.T) {
	app := testApp(t, &stubProvider{})
	if err := app.runAbout(); err != nil {
		t.Fatalf("about: %v", err)
	}
	out := app.testOut.String()

	for _, line := range logoArt {
		if want := strings.TrimSpace(line); want != "" && !strings.Contains(out, want) {
			t.Fatalf("about output is missing artwork line %q:\n%s", want, out)
		}
	}
	// Values that do not depend on the resolved language.
	for _, want := range []string{"Orbit", "Tencent Cloud", "main", "go", "~/.orbit"} {
		if !strings.Contains(out, want) {
			t.Fatalf("about output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatal("a non-terminal must get plain text, with no ANSI escapes")
	}
}

// TestAboutLogoMatchesREADME keeps the two copies of the artwork identical: the
// tagline in the README and the glyph drawn by `orbit about` must not drift.
func TestAboutLogoMatchesREADME(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("README should exist: %v", err)
	}
	present := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		present[strings.TrimSpace(line)] = true
	}
	for _, line := range logoArt {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !present[trimmed] {
			t.Fatalf("logoArt line %q is missing from README.md", trimmed)
		}
	}
}

// TestAboutWorksWithoutConfiguration guards the "fresh machine" promise: the card
// degrades to labels instead of failing when nothing is configured.
func TestAboutWorksWithoutConfiguration(t *testing.T) {
	app := testApp(t, &stubProvider{})
	app.Loaded = nil
	if err := app.runAbout(); err != nil {
		t.Fatalf("about must survive a missing config: %v", err)
	}
	if out := app.testOut.String(); !strings.Contains(out, "ssl") {
		t.Fatalf("products should fall back to the supported set:\n%s", out)
	}
}
