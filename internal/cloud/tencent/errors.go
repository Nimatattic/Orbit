package tencent

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
)

// Message keys emitted for cloud-side failures. The presentation layer turns
// them into actionable text (plan §4.6).
const (
	MsgRoleMissing     = "cloud.err.role_missing"
	MsgCertMismatch    = "cloud.err.cert_mismatch"
	MsgCertExpired     = "cloud.err.cert_expired"
	MsgNoICP           = "cloud.err.no_icp"
	MsgPendingRecord   = "cloud.err.pending_record"
	MsgRateLimited     = "cloud.err.rate_limited"
	MsgRegionRequired  = "cloud.err.region_required"
	MsgRegionInvalid   = "cloud.err.region_invalid"
	MsgDeployFailed    = "cloud.err.deploy_failed"
	MsgVerifyFailed    = "cloud.err.verify_failed"
	MsgNotFound        = "cloud.err.not_found"
	MsgDeleteKeptBound = "cloud.err.delete_kept_bound"
	MsgDeleteKeptQuery = "cloud.err.delete_kept_timeout"
	MsgDeleteRoleMiss  = "cloud.err.delete_role_missing"
	MsgNoRealName      = "cloud.err.no_real_name"
	MsgUnknown         = "cloud.err.unknown"
	MsgAuthFailed      = "cloud.err.auth_failed"
	MsgNoPermission    = "cloud.err.no_permission"
)

// retryableCodes are cloud error codes worth retrying: transient limits and
// stateful conflicts that clear once the in-flight task finishes.
var retryableCodes = map[string]bool{
	"LimitExceeded.RateLimitExceeded":                     true,
	"RequestLimitExceeded":                                true,
	"InternalError":                                       true,
	"InternalError.ConfigLocked":                          true,
	"InvalidParameter.ActionInProgress":                   true,
	"FailedOperation.CertificateDeployHasPendingRecord":   true,
	"ResourceInUse.CdnOpInProgress":                       true,
	"LimitExceeded.CdnHostOpTooOften":                     true,
	"UnauthorizedOperation.OperationTooOften":             true,
	"FailedOperation.CertificateHostResourceInstanceHuge": false, // deterministic, not transient
}

// messageKeys maps a Tencent Cloud error code to an actionable message key.
var messageKeys = map[string]string{
	"AuthFailure":                                          MsgAuthFailed,
	"AuthFailure.SecretIdNotFound":                         MsgAuthFailed,
	"AuthFailure.SignatureFailure":                         MsgAuthFailed,
	"AuthFailure.SignatureExpire":                          MsgAuthFailed,
	"AuthFailure.TokenFailure":                             MsgAuthFailed,
	"UnauthorizedOperation":                                MsgNoPermission,
	"UnauthorizedOperation.CamUnauthorized":                MsgNoPermission,
	"FailedOperation.RoleNotFoundAuthorization":            MsgRoleMissing,
	"FailedOperation.NoRealNameAuth":                       MsgNoRealName,
	"FailedOperation.CertificateMismatch":                  MsgCertMismatch,
	"FailedOperation.CertificateHasExpired":                MsgCertExpired,
	"InvalidParameter.CertNotMatchDomain":                  MsgCertMismatch,
	"InvalidParameter.CrtDateOverdue":                      MsgCertExpired,
	"InvalidParameter.SslCertMatchError":                   MsgCertMismatch,
	"InvalidParameter.CdnCertInfoNotFound":                 MsgCertMismatch,
	"FailedOperation.CertificateNotFound":                  MsgNotFound,
	"FailedOperation.SslCertNotFound":                      MsgNotFound,
	"ResourceUnavailable.CertNotFound":                     MsgNotFound,
	"ResourceUnavailable.HostNotFound":                     MsgNotFound,
	"ResourceUnavailable.ZoneNotFound":                     MsgNotFound,
	"ResourceUnavailable.CdnHostNoIcp":                     MsgNoICP,
	"OperationDenied.DomainNoICP":                          MsgNoICP,
	"InvalidParameter.RegionNotEmpty":                      MsgRegionRequired,
	"InvalidParameterValue.InvalidRegion":                  MsgRegionInvalid,
	"FailedOperation.CertificateDeployHasPendingRecord":    MsgPendingRecord,
	"LimitExceeded.RateLimitExceeded":                      MsgRateLimited,
	"FailedOperation.CertificateDeployInstanceFormatError": MsgDeployFailed,
	"FailedOperation.CertificateNotAvailable":              MsgDeployFailed,
	"FailedOperation.CertificateNotDeployInstance":         MsgDeployFailed,
	"FailedOperation.CertificateHostResourceTypeInvalid":   MsgDeployFailed,
}

// Code extracts the Tencent Cloud error code, or "" for local/network errors.
func Code(err error) string {
	var sdkErr *tcerr.TencentCloudSDKError
	if errors.As(err, &sdkErr) {
		return sdkErr.GetCode()
	}
	return ""
}

// RequestID exposes the cloud request id, useful for support tickets.
func RequestID(err error) string {
	var sdkErr *tcerr.TencentCloudSDKError
	if errors.As(err, &sdkErr) {
		return sdkErr.GetRequestId()
	}
	return ""
}

// MessageKey maps an error to an actionable message key.
func MessageKey(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return MsgUnknown
	}
	if key, ok := messageKeys[Code(err)]; ok {
		return key
	}
	return MsgUnknown
}

// messageKeyer is implemented by errors that already carry a resolved key.
type messageKeyer interface{ MessageKey() string }

// messageKeyOf prefers a key attached by Orbit over the code-derived one, so
// locally generated conditions keep their specific hint.
func messageKeyOf(err error) string {
	if err == nil {
		return ""
	}
	var mk messageKeyer
	if errors.As(err, &mk) {
		if key := mk.MessageKey(); key != "" && key != MsgUnknown {
			return key
		}
	}
	return MessageKey(err)
}

// IsRetryable reports whether the call should be attempted again.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	code := Code(err)
	if code == "" {
		// Network-level failures carry no cloud code and are usually transient.
		return true
	}
	return retryableCodes[code]
}

// wrap annotates a cloud error with its code and message key while preserving
// the original error for errors.As/Is.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	return &cloudError{err: err, code: Code(err), key: MessageKey(err), requestID: RequestID(err)}
}

type cloudError struct {
	err       error
	code      string
	key       string
	requestID string
}

func (e *cloudError) Error() string { return e.err.Error() }
func (e *cloudError) Unwrap() error { return e.err }

// Code returns the cloud error code.
func (e *cloudError) Code() string { return e.code }

// MessageKey returns the actionable message key.
func (e *cloudError) MessageKey() string { return e.key }

// RequestID returns the cloud request id.
func (e *cloudError) RequestID() string { return e.requestID }

// ErrorInfo extracts code/key/request id from any error.
func ErrorInfo(err error) (code, key, requestID string) {
	var ce *cloudError
	if errors.As(err, &ce) {
		return ce.code, ce.key, ce.requestID
	}
	return Code(err), MessageKey(err), RequestID(err)
}

// limiter paces calls to one API. The official limit for SSL is 10 QPS and for
// CDN/EdgeOne 20 QPS, measured per API + endpoint region + sub-account.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	sleep    func(context.Context, time.Duration) error
	now      func() time.Time
}

func newLimiter(qps int, now func() time.Time, sleep func(context.Context, time.Duration) error) *limiter {
	if qps <= 0 {
		qps = 10
	}
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepCtx
	}
	return &limiter{interval: time.Second / time.Duration(qps), now: now, sleep: sleep}
}

func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := l.now()
	if l.next.Before(now) {
		l.next = now
	}
	wait := l.next.Sub(now)
	l.next = l.next.Add(l.interval)
	l.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	return l.sleep(ctx, wait)
}

// call applies pacing plus bounded exponential backoff with jitter.
func call[T any](ctx context.Context, l *limiter, maxAttempts int, fn func() (T, error)) (T, error) {
	var zero T
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := 1; ; attempt++ {
		if l != nil {
			if err := l.wait(ctx); err != nil {
				return zero, err
			}
		}
		resp, err := fn()
		if err == nil {
			return resp, nil
		}
		if attempt >= maxAttempts || !IsRetryable(err) {
			return zero, wrap(err)
		}
		backoff := time.Duration(200*(1<<(attempt-1))) * time.Millisecond
		backoff += time.Duration(rand.Int63n(int64(100 * time.Millisecond)))
		if err := sleepCtx(ctx, backoff); err != nil {
			return zero, err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// actionError annotates which API call failed, for which account. Without it a
// cloud failure is untraceable: "UnauthorizedOperation" alone does not say
// whether SSL, CDN or EdgeOne refused the call.
type actionError struct {
	action  string
	account string
	err     error
}

func (e *actionError) Error() string {
	if e.account == "" {
		return fmt.Sprintf("%s (action %s)", e.err, e.action)
	}
	return fmt.Sprintf("%s (action %s, account %s)", e.err, e.action, e.account)
}

func (e *actionError) Unwrap() error      { return e.err }
func (e *actionError) MessageKey() string { return messageKeyOf(e.err) }

// withAction wraps err with the failing API action and account.
func withAction(err error, account, action string) error {
	if err == nil {
		return nil
	}
	return &actionError{action: action, account: account, err: err}
}

// ActionOf returns the API action that failed, or "" when unknown.
func ActionOf(err error) string {
	var ae *actionError
	if errors.As(err, &ae) {
		return ae.action
	}
	return ""
}
