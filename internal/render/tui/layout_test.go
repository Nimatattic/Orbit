package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func plainStyles() styles { return newStyles(true) }

func TestPanelLinesAreExactlyTheRequestedWidth(t *testing.T) {
	// CJK titles are double-width; every line must still match exactly so panels
	// stack without ragged edges.
	cases := []struct {
		name  string
		title string
		body  string
		width int
	}{
		{"ascii", "plan (7)", "▸ [x] CDN  a.example.com", 60},
		{"cjk title", "待处理（7）  已选 3", "▸ [x] CDN  a.example.com", 60},
		{"long cjk title truncates", "待处理（1234）已选 1000 个条目", "body", 30},
		{"empty body", "title", "", 40},
		{"multiline body with cjk", "账号", "证书 16（上传 16 / 在用 9）\n资源 38", 72},
	}
	st := plainStyles()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := panel(st, tc.title, tc.body, tc.width, true)
			lines := strings.Split(got, "\n")
			if len(lines) < 3 {
				t.Fatalf("panel should have a top border, content and a bottom border, got %q", got)
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w != tc.width {
					t.Fatalf("line %d width = %d, want %d: %q", i, w, tc.width, l)
				}
			}
			if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[0], "╮") {
				t.Fatalf("top border must be complete: %q", lines[0])
			}
		})
	}
}

func TestScreenPinsFooterAndPadsEveryLine(t *testing.T) {
	st := plainStyles()
	header := panel(st, "Orbit", "line", 40, false)
	body := panel(st, "body", "a\nb", 40, true)

	got := screen(40, 12, "FOOTER", header, body)
	lines := strings.Split(got, "\n")
	if len(lines) != 12 {
		t.Fatalf("screen should fill the requested height, got %d lines", len(lines))
	}
	if lines[len(lines)-1] != pad("FOOTER", 40) {
		t.Fatalf("footer must be the last line, got %q", lines[len(lines)-1])
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != 40 {
			t.Fatalf("line %d width = %d, want 40", i, w)
		}
	}
}

func TestScreenTruncatesWhenContentOverflows(t *testing.T) {
	st := plainStyles()
	body := panel(st, "body", strings.Repeat("x\n", 40), 30, true)
	got := screen(30, 8, "FOOTER", body)
	if lines := strings.Split(got, "\n"); len(lines) != 8 {
		t.Fatalf("overflow must be cut to the height, got %d lines", len(lines))
	}
}

func TestProgressBar(t *testing.T) {
	st := plainStyles()
	if got := progressBar(st, 0, 0, 10); !strings.Contains(got, strings.Repeat("░", 10)) {
		t.Fatalf("empty progress should be all empty cells, got %q", got)
	}
	if got := progressBar(st, 5, 10, 10); !strings.Contains(got, strings.Repeat("█", 5)) {
		t.Fatalf("half progress should be half filled, got %q", got)
	}
	if got := progressBar(st, 10, 10, 10); !strings.Contains(got, strings.Repeat("█", 10)) {
		t.Fatalf("complete progress should be full, got %q", got)
	}
	if got := progressBar(st, 99, 10, 10); strings.Count(got, "█") != 10 {
		t.Fatalf("done > total must clamp, got %q", got)
	}
}

func TestKeyHintsDropOverflowingHints(t *testing.T) {
	st := plainStyles()
	pairs := [][2]string{{"↑/↓", "move"}, {"space", "toggle"}, {"a", "all"}, {"q", "quit"}}
	got := keyHints(st, 18, pairs)
	if got == "" {
		t.Fatal("at least one hint must survive")
	}
	if lipgloss.Width(got) > 18 {
		t.Fatalf("footer must fit the width, got width %d: %q", lipgloss.Width(got), got)
	}
	if strings.Contains(got, "quit") {
		t.Fatalf("hints are dropped from the end, so 'quit' should be gone: %q", got)
	}
}

func TestKeyHintsRenderAllKeys(t *testing.T) {
	st := plainStyles()
	got := keyHints(st, 200, [][2]string{{"↑/↓", "move"}, {"space", "toggle"}})
	for _, want := range []string{"↑/↓", "move", "space", "toggle"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}
