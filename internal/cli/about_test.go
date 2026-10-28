package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// TestAboutLogoMatchesBanner keeps the drawn wordmark and the pixel-art poster in
// sync: both encode the same 5x7 bitmap, so a change in either copy is a change of
// the brand mark. scripts/gen-banner.py regenerates the asset.
func TestAboutLogoMatchesBanner(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "assets", "banner.svg"))
	if err != nil {
		t.Fatalf("banner asset should exist: %v", err)
	}
	svg := string(raw)

	group := regexp.MustCompile(`(?s)<g id="wordmark"([^>]*)>(.*?)</g>`).FindStringSubmatch(svg)
	if group == nil {
		t.Fatal("banner.svg must contain <g id=\"wordmark\">")
	}
	// The poster declares its own grid metrics, so the artwork can be re-scaled
	// without touching this test; only the bitmap has to stay in sync.
	advanceMatch := regexp.MustCompile(`data-advance="(\d+)"`).FindStringSubmatch(group[1])
	if advanceMatch == nil {
		t.Fatal("the wordmark group must declare data-advance")
	}
	advance, err := strconv.Atoi(advanceMatch[1])
	if err != nil || advance <= 0 {
		t.Fatalf("invalid data-advance: %q", advanceMatch[1])
	}
	rects := regexp.MustCompile(`<rect x="(\d+)" y="(\d+)"`).FindAllStringSubmatch(group[2], -1)
	if len(rects) == 0 {
		t.Fatal("the wordmark group must contain pixel rects")
	}
	minX, minY := 1<<30, 1<<30
	for _, m := range rects {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		if x < minX {
			minX = x
		}
		if y < minY {
			minY = y
		}
	}

	filled := map[[2]int]bool{}
	for _, m := range rects {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		filled[[2]int{(y - minY) / advance, (x - minX) / advance}] = true
	}

	for row, line := range logoArt {
		var built strings.Builder
		for col := 0; col < len(line); col++ {
			if filled[[2]int{row, col}] {
				built.WriteByte('#')
			} else {
				built.WriteByte(' ')
			}
		}
		if strings.TrimRight(built.String(), " ") != strings.TrimRight(line, " ") {
			t.Fatalf("banner row %d does not match the CLI logo:\n banner %q\n cli    %q",
				row, built.String(), line)
		}
	}

	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "assets/banner.svg") {
		t.Fatal("README.md should embed assets/banner.svg")
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
