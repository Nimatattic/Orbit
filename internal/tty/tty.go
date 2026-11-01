// Package tty centralises terminal detection so that every command agrees on
// when interactive output is allowed (see plan §3.5, decision B6).
package tty

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// IsTerminal reports whether f is attached to a terminal.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// IsDumb reports whether TERM indicates a non-capable terminal.
func IsDumb() bool { return isDumb(os.Getenv) }

func isDumb(getenv func(string) string) bool {
	return strings.EqualFold(strings.TrimSpace(getenv("TERM")), "dumb")
}

// Probe carries every fact interactive detection depends on. It is a struct so
// the decision can be exercised in tests without a real terminal; a nil field or
// a nil function falls back to the real process, which keeps call sites trivial.
type Probe struct {
	Stdin      *os.File
	Stdout     *os.File
	Stderr     *os.File
	Getenv     func(string) string
	IsTerminal func(*os.File) bool
}

// SystemProbe returns the probe describing the current process.
func SystemProbe() Probe {
	return Probe{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Getenv:     os.Getenv,
		IsTerminal: IsTerminal,
	}
}

// Interactive reports whether Orbit may draw an interactive TUI.
func Interactive() bool { return InteractiveWith(SystemProbe()) }

// InteractiveWith reports whether an interactive TUI may be drawn for p.
//
// The TUI draws on stderr (tui.Run and tui.RunGuide pass tea.WithOutput(os.Stderr))
// while the text path keeps stdout for reports, so BOTH streams must be terminals:
// piping stdout into a pager or a file must not put a full-screen UI on the
// terminal, and piping stderr away must not hide the screen from the operator.
// stdin must be a terminal as well, because the key handling reads from it.
func InteractiveWith(p Probe) bool {
	isTTY := p.IsTerminal
	if isTTY == nil {
		isTTY = IsTerminal
	}
	getenv := p.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if !isTTY(p.Stdin) || !isTTY(p.Stdout) || !isTTY(p.Stderr) {
		return false
	}
	if isDumb(getenv) {
		return false
	}
	// Explicit opt-out, for terminals that do look interactive but must stay
	// line-oriented (CI logs, recorded demos).
	return getenv("ORBIT_NO_TUI") == ""
}
