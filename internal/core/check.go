package core

// CheckStatus is the outcome of one preflight check.
type CheckStatus string

// Check statuses.
const (
	CheckOK   CheckStatus = "ok"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

// Check is one preflight/doctor result. Key is a message key so that the
// presentation layer can translate it.
type Check struct {
	Key    string
	Status CheckStatus
	Detail string
	// MessageKey is an actionable hint that the presentation layer translates.
	MessageKey string
	Err        error
}
