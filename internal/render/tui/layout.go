package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// styles holds every visual token the screens share. Panels are built from
// rounded borders plus a title embedded in the top border, which is what gives
// the interface its "structured" look.
type styles struct {
	title    lipgloss.Style
	subtitle lipgloss.Style
	dim      lipgloss.Style
	ok       lipgloss.Style
	warn     lipgloss.Style
	fail     lipgloss.Style
	active   lipgloss.Style
	box      lipgloss.Style
	boxFocus lipgloss.Style
	key      lipgloss.Style
	barFull  lipgloss.Style
	barEmpty lipgloss.Style
	pending  lipgloss.Style
	unbound  lipgloss.Style
}

// palette: adaptive colours read well on both dark and light terminals.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "#0B5FFF", Dark: "#5FAFFF"}
	colOk     = lipgloss.AdaptiveColor{Light: "#0F7B0F", Dark: "#5FD75F"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#9A6400", Dark: "#FFC857"}
	colFail   = lipgloss.AdaptiveColor{Light: "#B4232A", Dark: "#FF6B6B"}
	colDim    = lipgloss.AdaptiveColor{Light: "#6C6C6C", Dark: "#8A8A8A"}
	colBorder = lipgloss.AdaptiveColor{Light: "#C9C9C9", Dark: "#4A4A4A"}
)

func newStyles(noColor bool) styles {
	if noColor {
		return styles{
			title:    lipgloss.NewStyle().Bold(true),
			subtitle: lipgloss.NewStyle(),
			dim:      lipgloss.NewStyle(),
			ok:       lipgloss.NewStyle(),
			warn:     lipgloss.NewStyle(),
			fail:     lipgloss.NewStyle(),
			active:   lipgloss.NewStyle().Bold(true),
			box:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
			boxFocus: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
			key:      lipgloss.NewStyle().Bold(true),
			barFull:  lipgloss.NewStyle(),
			barEmpty: lipgloss.NewStyle(),
			pending:  lipgloss.NewStyle(),
			unbound:  lipgloss.NewStyle(),
		}
	}
	return styles{
		title:    lipgloss.NewStyle().Bold(true).Foreground(colAccent),
		subtitle: lipgloss.NewStyle().Foreground(colDim),
		dim:      lipgloss.NewStyle().Foreground(colDim),
		ok:       lipgloss.NewStyle().Foreground(colOk),
		warn:     lipgloss.NewStyle().Foreground(colWarn),
		fail:     lipgloss.NewStyle().Foreground(colFail),
		active:   lipgloss.NewStyle().Bold(true).Foreground(colAccent),
		box:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder),
		boxFocus: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent),
		key:      lipgloss.NewStyle().Bold(true).Foreground(colAccent),
		barFull:  lipgloss.NewStyle().Foreground(colOk),
		barEmpty: lipgloss.NewStyle().Foreground(colBorder),
		pending:  lipgloss.NewStyle(),
		unbound:  lipgloss.NewStyle().Foreground(colWarn),
	}
}

// panel draws a bordered box whose top border carries a title.
//
// The title is injected into the top border rather than added as a line, so a
// panel always occupies content + 2 rows and panels line up with each other.
func panel(st styles, title, body string, width int, focused bool) string {
	if width < 12 {
		width = 12
	}
	style := st.box
	if focused {
		style = st.boxFocus
	}
	inner := width - 2
	box := style.Width(inner).Render(body)

	border := lipgloss.RoundedBorder()

	// The top border is one rune, then a 1-column dash, then the label and the
	// remainder filled with dashes, then the corner: total = 3 + label + fill.
	// Keeping the label within (avail-1) leaves at least one dash, so the line
	// always measures exactly `width`.
	avail := width - 3
	labelMax := avail - 1
	if labelMax < 3 {
		labelMax = 3
	}
	label := " " + title + " "
	if lipgloss.Width(label) > labelMax {
		label = " " + truncate(title, labelMax-3) + "… "
	}
	if lipgloss.Width(label) > labelMax {
		label = truncate(label, labelMax)
	}
	fill := avail - lipgloss.Width(label)
	if fill < 1 {
		fill = 1
	}
	top := border.TopLeft + border.Top + label + strings.Repeat(border.Top, fill) + border.TopRight

	lines := strings.Split(box, "\n")
	if len(lines) == 0 {
		return top
	}
	lines[0] = top
	return strings.Join(lines, "\n")
}

// progressBar renders a fixed-width bar, e.g. [████░░░░] 3/7.
func progressBar(st styles, done, total, width int) string {
	if width < 8 {
		width = 8
	}
	if total <= 0 {
		return st.dim.Render("[" + strings.Repeat("░", width) + "]")
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	return "[" + st.barFull.Render(strings.Repeat("█", filled)) +
		st.barEmpty.Render(strings.Repeat("░", width-filled)) + "]"
}

// screen assembles blocks into a full-screen frame: the footer is pinned to the
// last row and every line is padded to the terminal width so the alternate
// screen never leaves stale characters behind.
func screen(width, height int, footer string, blocks ...string) string {
	lines := make([]string, 0, height)
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		lines = append(lines, strings.Split(strings.TrimRight(block, "\n"), "\n")...)
	}
	if footer != "" {
		footerLines := strings.Split(strings.TrimRight(footer, "\n"), "\n")
		if height > 0 {
			// Reserve the last row(s) for the footer.
			room := height - len(footerLines)
			if room < 0 {
				room = 0
			}
			for len(lines) < room {
				lines = append(lines, "")
			}
			if len(lines) > room {
				lines = lines[:room]
			}
		}
		lines = append(lines, footerLines...)
	}
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		lines[i] = pad(l, width)
	}
	return strings.Join(lines, "\n")
}

// pad right-pads a line to the display width (CJK aware).
func pad(s string, width int) string {
	if width <= 0 {
		return s
	}
	w := lipgloss.Width(s)
	if w >= width {
		return truncate(s, width)
	}
	return s + strings.Repeat(" ", width-w)
}

// truncateLines keeps every line of a block on a single row, cutting it with an
// ellipsis. Panels render multi-line bodies; letting lipgloss wrap them breaks
// column alignment, which is worse than losing the tail of a long value.
func truncateLines(body string, width int) string {
	if width <= 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = truncate(line, width)
	}
	return strings.Join(lines, "\n")
}

// wrapLine soft-wraps one line to width display columns, breaking on spaces and
// falling back to a hard break for long tokens such as action names.
func wrapLine(s string, width int) []string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return []string{s}
	}
	var out []string
	remaining := s
	for lipgloss.Width(remaining) > width {
		cut := -1
		used := 0
		for i, r := range remaining {
			rw := lipgloss.Width(string(r))
			if used+rw > width {
				break
			}
			used += rw
			if r == ' ' {
				cut = i
			}
		}
		if cut <= 0 {
			// No space to break on: cut exactly at the width.
			used = 0
			for i, r := range remaining {
				rw := lipgloss.Width(string(r))
				if used+rw > width {
					cut = i - 1
					break
				}
				used += rw
			}
			if cut <= 0 {
				break
			}
		}
		out = append(out, remaining[:cut])
		remaining = strings.TrimLeft(remaining[cut:], " ")
	}
	if remaining != "" {
		out = append(out, remaining)
	}
	return out
}

// keyHints renders the footer legend. Hints that would overflow the width are
// dropped from the end, so the last row never gets cut mid-word.
func keyHints(st styles, width int, pairs [][2]string) string {
	separator := st.dim.Render("  ·  ")
	out := ""
	for _, p := range pairs {
		piece := st.key.Render(p[0]) + " " + p[1]
		candidate := piece
		if out != "" {
			candidate = out + separator + piece
		}
		if width > 0 && lipgloss.Width(candidate) > width {
			if out == "" {
				return truncate(candidate, width)
			}
			return out
		}
		out = candidate
	}
	return out
}
