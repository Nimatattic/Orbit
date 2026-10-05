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

// Provider is the single seam between the engine and a cloud vendor.
type Provider interface {
	// Name is the platform identifier, e.g. "tencent".
	Name() string
	// Capabilities exposes what the provider can do, for planning and UI.
	Capabilities() Capabilities
	// Preflight verifies credentials, roles and permissions for one account.
	Preflight(ctx context.Context, account string) ([]core.Check, error)
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
