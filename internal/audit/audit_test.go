package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoggerAppendsJSONLinesWithSafeMode(t *testing.T) {
	dir := t.TempDir()
	logger, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := logger.Write(Record{RunID: "run-1", Kind: KindChange, Command: "update", Platform: "tencent", Account: "main", Action: "apply", Domain: "a.example.com", CertID: "c-1", Result: "ok", Verified: true}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := logger.Write(Record{RunID: "run-1", Kind: KindSummary, Command: "update", Platform: "tencent", Account: "main", Action: "apply", Total: 1, Applied: 1}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("audit file mode = %04o, want 0600", got)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, line := range splitLines(raw) {
		lines++
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", lines, err)
		}
		if rec.Time.IsZero() {
			t.Fatalf("line %d has no timestamp", lines)
		}
	}
	if lines != 2 {
		t.Fatalf("expected 2 lines, got %d", lines)
	}

	// Appending again must keep the previous content.
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Write(Record{RunID: "run-2"}); err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
	raw, _ = os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if got := len(splitLines(raw)); got != 3 {
		t.Fatalf("append lost history: got %d lines", got)
	}
}

func TestNilLoggerIsUsable(t *testing.T) {
	var logger *Logger
	if err := logger.Write(Record{}); err != nil {
		t.Fatalf("a nil logger must be a no-op, got %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if logger.Path() != "" {
		t.Fatal("nil logger has no path")
	}
}

func TestNewRunIDIsSortable(t *testing.T) {
	early := NewRunID(time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC))
	late := NewRunID(time.Date(2026, 10, 5, 11, 0, 0, 0, time.UTC))
	if !(early < late) {
		t.Fatalf("run ids should sort by time: %q vs %q", early, late)
	}
}

func splitLines(raw []byte) []string {
	var out []string
	start := 0
	for i, b := range raw {
		if b != '\n' {
			continue
		}
		if i > start {
			out = append(out, string(raw[start:i]))
		}
		start = i + 1
	}
	if start < len(raw) {
		out = append(out, string(raw[start:]))
	}
	return out
}
