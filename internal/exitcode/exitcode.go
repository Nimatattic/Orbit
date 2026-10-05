// Package exitcode defines Orbit's process exit-code contract.
//
// The contract is part of the CLI's public interface (cron jobs depend on it):
//
//	0  success, including "no changes" and "dry-run completed"
//	1  fatal error (configuration, credentials, network, arguments)
//	2  partial failure (some planned items failed)
//	3  plan mode found pending changes (only with --fail-on-changes)
//	4  a safety gate refused to execute
package exitcode

import (
	"errors"
	"fmt"
)

// Exit codes; see the package comment for their meaning.
const (
	OK             = 0
	Fatal          = 1
	Partial        = 2
	PendingChanges = 3
	GateRefused    = 4
)

// Error carries an exit code plus an i18n message key. The CLI layer is the
// only place that translates Key, keeping core logic free of presentation.
type Error struct {
	Code int
	Key  string
	Args []any
	Err  error
}

// Error implements error. The text is deliberately language-neutral: it is for
// logs and debugging, while Key/Args drive the user-facing message.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Key, e.Err)
	}
	return fmt.Sprintf("%s %v", e.Key, e.Args)
}

// Unwrap exposes the wrapped error for errors.Is/As.
func (e *Error) Unwrap() error { return e.Err }

// New builds an Error with the given exit code and message key.
func New(code int, key string, args ...any) *Error {
	return &Error{Code: code, Key: key, Args: args}
}

// Wrap attaches an exit code and message key to an existing error.
func Wrap(err error, code int, key string, args ...any) *Error {
	return &Error{Code: code, Key: key, Args: args, Err: err}
}

// CodeOf extracts the exit code carried by err, defaulting to Fatal.
func CodeOf(err error) int {
	if err == nil {
		return OK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Fatal
}
