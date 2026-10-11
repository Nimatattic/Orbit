// Package audit appends one JSON object per executed change to a JSONL file.
//
// The cloud is the source of truth for state; the audit log answers a different
// question: "who changed what, when, and what did the tool report?" It is
// append-only and never stores credentials.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Kinds of records in the log.
const (
	KindChange  = "change"
	KindSummary = "summary"
)

// Record is one line of the audit log. A single shape keeps consumers simple;
// Kind tells a single change apart from the run summary.
type Record struct {
	Time    time.Time `json:"time"`
	RunID   string    `json:"run_id"`
	Version string    `json:"version,omitempty"`
	Kind    string    `json:"kind"`
	Command string    `json:"command"`
	// Platform and Account identify who was touched.
	Platform string `json:"platform"`
	Account  string `json:"account"`
	// Action is apply or delete.
	Action   string `json:"action"`
	Product  string `json:"product,omitempty"`
	Domain   string `json:"domain,omitempty"`
	CertID   string `json:"cert_id,omitempty"`
	FromCert string `json:"from_cert_id,omitempty"`
	Strategy string `json:"strategy,omitempty"`
	// Result is ok, unverified or failed.
	Result     string `json:"result,omitempty"`
	Verified   bool   `json:"verified,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	MessageKey string `json:"message_key,omitempty"`
	Error      string `json:"error,omitempty"`
	// Counts are only set on summary records.
	Total      int `json:"total,omitempty"`
	Applied    int `json:"applied,omitempty"`
	Unverified int `json:"unverified,omitempty"`
	Failed     int `json:"failed,omitempty"`
}

// Logger writes records to one append-only file.
type Logger struct {
	path string
	mu   sync.Mutex
	file *os.File
}

// Open creates dir if needed and opens (or creates) its audit.jsonl with 0600.
func Open(dir string) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "audit.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Logger{path: path, file: file}, nil
}

// Path returns the file being written.
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Write appends one record.
func (l *Logger) Write(rec Record) error {
	if l == nil {
		return nil
	}
	if rec.Kind == "" {
		rec.Kind = KindChange
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.file.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	return nil
}

// Close flushes and closes the file.
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.file.Close()
	l.file = nil
	return err
}

// NewRunID builds a sortable, unique-enough identifier for one run.
func NewRunID(now time.Time) string {
	return fmt.Sprintf("%s-%04x", now.UTC().Format("20060102T150405"), os.Getpid()&0xffff)
}
