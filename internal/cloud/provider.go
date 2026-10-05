// Package cloud defines the provider abstraction. It is modelled on
// capabilities rather than on one vendor's API shape, so that Alibaba Cloud can
// be added later without touching the engine or the TUI (plan §4.2).
package cloud

import (
	"context"
	"errors"

	"github.com/Crequency/orbit/internal/core"
)

// ErrNotImplemented is returned by providers or operations that this batch does
// not implement yet.
var ErrNotImplemented = errors.New("cloud: not implemented yet")

// ScanScope narrows an enumeration.
type ScanScope struct {
	Products []string
	Domains  []string
	// CachedEvidence asks the provider to accept the cloud's short-lived
	// association cache. `status` sets it (fast, up to 30 minutes old);
	// `update` and `clean` must leave it false because they act on the data.
	CachedEvidence bool
}

// Progress is one human-readable scan event. The provider only emits message
// keys and arguments; translation happens in the presentation layer.
type Progress struct {
	Key   string
	Args  []any
	Done  int
	Total int
}

// ApplyOptions configures a write run.
type ApplyOptions struct {
	DryRun            bool
	ExperimentalWrite bool
	Concurrency       int
}

// DeleteOptions configures certificate deletion.
type DeleteOptions struct {
	DryRun    bool
	BackupDir string
	// CheckBindings must always be true: SSL's DeleteCertificates defaults to
	// NOT checking resource associations (decision T5).
	CheckBindings bool
}

// LocalCert is a certificate material pair, used by the reserved push flow (A7).
type LocalCert struct {
	Domain   string
	CertPEM  []byte
	KeyPEM   []byte
	Alias    string
	NotAfter string
}

// ProductCapability describes what Orbit may do with one product.
type ProductCapability struct {
	CanEnumerate         bool
	CanReadBoundCert     bool
	CanApplyByCertID     bool
	CanVerifyAfterDeploy bool
	Granularity          string // domain
	OwnCertField         string // cdn: Https.CertInfo.CertId; teo: Certificate.List[].CertId
	Experimental         bool
}

// Capabilities summarises a provider.
type Capabilities struct {
	Products            map[core.Product]ProductCapability
	CertGlobal          bool
	DeployNeedsRegion   map[core.Product]bool
	AsyncDeploy         bool
	DeploySerialPerPair bool
	SupportsUnbind      bool
	VerifyAfterDeploy   bool
	ExtraScopes         []string
}

// ScanResult is a full snapshot of one account.
type ScanResult struct {
	Certificates []core.Certificate
	Resources    []core.Resource
	Evidence     map[string]core.BindingEvidence
	Warnings     []ScanWarning
}

// Complete reports whether the scan produced usable evidence, i.e. whether no
// blocking warning was raised.
func (r ScanResult) Complete() bool { return len(r.BlockingWarnings()) == 0 }

// BlockingWarnings returns only the warnings that make the evidence incomplete.
func (r ScanResult) BlockingWarnings() []ScanWarning {
	out := make([]ScanWarning, 0, len(r.Warnings))
	for _, w := range r.Warnings {
		if !w.Informational {
			out = append(out, w)
		}
	}
	return out
}

// ScanWarning records a per-product failure that did not abort the whole scan.
//
// Warnings are what make a partial scan visible: `clean` must refuse to act on
// an incomplete evidence set, while `status` may still report everything else.
type ScanWarning struct {
	Product    string // ssl / cdn / teo
	Action     string // e.g. cdn:DescribeDomainsConfig
	Code       string // cloud error code, empty for network errors
	MessageKey string // actionable hint
	// Scope names the narrower target that failed, e.g. an EdgeOne site.
	Scope string
	// Informational marks a warning that does NOT make the evidence incomplete:
	// the target is structurally out of scope (an EdgeOne Pages site is not
	// certificate-deployable). The zero value is deliberately the strict one, so
	// forgetting to set this keeps a warning blocking.
	Informational bool
	Err           error
}

// Provider is the single seam between the engine and a cloud vendor.
type Provider interface {
	// Name is the platform identifier, e.g. "tencent".
	Name() string
	// Capabilities exposes what the provider can do, for planning and UI.
	Capabilities() Capabilities
	// Preflight verifies credentials, roles and permissions for one account.
	Preflight(ctx context.Context, account string) ([]core.Check, error)
	// Scan collects certificates, resources and association evidence in one go.
	// A product that cannot be scanned is reported as a warning instead of
	// failing the whole call, unless the certificate pool itself is unavailable.
	Scan(ctx context.Context, account string, scope ScanScope) (ScanResult, error)
	// ListCertificates returns the certificate pool of one account.
	ListCertificates(ctx context.Context, account string, scope ScanScope) ([]core.Certificate, error)
	// ListResources returns domains that can hold a certificate.
	ListResources(ctx context.Context, account string, scope ScanScope) ([]core.Resource, error)
	// ListBindings answers "which resources use these certificates".
	ListBindings(ctx context.Context, account string, certIDs []string) (map[string]core.BindingEvidence, error)
	// Apply executes the pending changes of a plan.
	Apply(ctx context.Context, account string, plan core.Plan, opts ApplyOptions) ([]core.Result, error)
	// DeleteCertificates removes certificates after the gates have passed.
	DeleteCertificates(ctx context.Context, account string, certIDs []string, opts DeleteOptions) ([]core.Result, error)
	// UploadCertificate is reserved for `orbit push` (decision A7).
	UploadCertificate(ctx context.Context, account string, cert LocalCert) (string, error)
}
