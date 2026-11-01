package tty

import (
	"os"
	"testing"
)

// fakeProbe builds a Probe whose streams report the requested terminal state, so
// the decision can be tested without a pty.
func fakeProbe(stdinTTY, stdoutTTY, stderrTTY bool, env map[string]string) Probe {
	file := func(on bool) *os.File {
		if !on {
			return nil // IsTerminal(nil) is false, which is the "not a tty" case
		}
		return os.Stdin
	}
	return Probe{
		Stdin:  file(stdinTTY),
		Stdout: file(stdoutTTY),
		Stderr: file(stderrTTY),
		Getenv: func(key string) string { return env[key] },
		IsTerminal: func(f *os.File) bool {
			return f != nil
		},
	}
}

func TestInteractiveRequiresEveryStreamToBeATerminal(t *testing.T) {
	tests := []struct {
		name                  string
		stdin, stdout, stderr bool
		want                  bool
	}{
		{name: "all terminals", stdin: true, stdout: true, stderr: true, want: true},
		{name: "stdout piped", stdin: true, stdout: false, stderr: true, want: false},
		// The regression this guards: the TUI draws on stderr, so a terminal
		// stderr is not enough when stdout is redirected, and vice versa.
		{name: "stderr redirected", stdin: true, stdout: true, stderr: false, want: false},
		{name: "stdin piped", stdin: false, stdout: true, stderr: true, want: false},
		{name: "nothing is a terminal", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := InteractiveWith(fakeProbe(tc.stdin, tc.stdout, tc.stderr, map[string]string{"TERM": "xterm-256color"}))
			if got != tc.want {
				t.Fatalf("InteractiveWith = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInteractiveHonoursTermAndOptOut(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "colour terminal", env: map[string]string{"TERM": "xterm-256color"}, want: true},
		{name: "TERM unset", env: map[string]string{}, want: true},
		{name: "dumb terminal", env: map[string]string{"TERM": "DUMB"}, want: false},
		{name: "dumb terminal with spaces", env: map[string]string{"TERM": " dumb "}, want: false},
		{name: "explicit opt-out", env: map[string]string{"TERM": "xterm", "ORBIT_NO_TUI": "1"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := InteractiveWith(fakeProbe(true, true, true, tc.env))
			if got != tc.want {
				t.Fatalf("InteractiveWith = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestProbeFallsBackToTheProcess keeps the nil-field contract honest: a nil
// Getenv/IsTerminal means "ask the real process", while nil streams never count
// as terminals.
func TestProbeFallsBackToTheProcess(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("ORBIT_NO_TUI", "")

	p := Probe{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		IsTerminal: func(*os.File) bool { return true },
	}
	if !InteractiveWith(p) {
		t.Fatal("a nil Getenv must fall back to the process environment")
	}
	t.Setenv("ORBIT_NO_TUI", "1")
	if InteractiveWith(p) {
		t.Fatal("ORBIT_NO_TUI must be honoured through the process environment")
	}

	if InteractiveWith(Probe{}) {
		t.Fatal("a zero Probe has no streams and must not be interactive")
	}
	if IsTerminal(nil) {
		t.Fatal("IsTerminal(nil) must be false")
	}
}

func TestIsDumbReadsTheProcessEnvironment(t *testing.T) {
	t.Setenv("TERM", "dumb")
	if !IsDumb() {
		t.Fatal("IsDumb must report TERM=dumb")
	}
	t.Setenv("TERM", "xterm-256color")
	if IsDumb() {
		t.Fatal("IsDumb must not reject a capable terminal")
	}
}
