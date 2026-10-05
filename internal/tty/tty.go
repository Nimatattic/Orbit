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
func IsDumb() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb")
}

// Interactive reports whether Orbit may draw an interactive TUI on stdout.
func Interactive() bool {
	return IsTerminal(os.Stdout) && IsTerminal(os.Stdin) && !IsDumb() && os.Getenv("ORBIT_NO_TUI") == ""
}
