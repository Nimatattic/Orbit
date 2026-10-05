// Package core holds Orbit's domain model and the pure decision logic:
// domain matching, plan building and the cleanup safety gates. It performs no
// I/O and imports no UI package (plan §4.1).
package core

import (
	"sort"
	"time"
)

// Product identifies a cloud product that can hold a certificate.
type Product string

// Products supported by this batch (decision A3).
const (
	ProductCDN Product = "cdn"
	ProductTEO Product = "teo"
)

// CertSource values observed in Tencent Cloud's `From` field.
const (
	SourceUpload    = "upload" // user uploaded (1Panel pushes these)
	SourceBuy       = "buy"    // Tencent Cloud issued
	PackageFreeType = "83"     // TrustAsia C1 DV Free
)

// Certificate is the normalised view of a certificate in the SSL service.
type Certificate struct {
	ID          string
	Alias       string
	From        string // upload / trustasia / wosign / sheca / buy
	Domain      string
	SANs        []string
	NotBefore   time.Time // CertBeginTime
	NotAfter    time.Time // CertEndTime
	InsertTime  time.Time
	Status      uint64 // 0..15 per API docs
	Deployable  bool
	PackageType string // "" uploaded, "83" free, otherwise paid SKU
	// HostingStatus is nil when the API omitted the field. Semantics:
	// 0 hosting, 5 replacing resources, 10 hosting done, -1 not hosted.
	// A pointer is required because the zero value (0) means "hosting", so an
	// absent field must not be read as "not hosted" and vice versa.
	HostingStatus *int64
	AutoRenew     bool
	Tags          map[string]string
	Region        string // always empty for Tencent Cloud (reserved for Alibaba)
}

// IsUploaded reports whether the certificate came from the user.
func (c Certificate) IsUploaded() bool { return c.From == SourceUpload }

// IsFree reports whether the certificate uses the free SKU.
func (c Certificate) IsFree() bool { return c.PackageType == PackageFreeType }

// IsHosted reports whether the SSL service manages this certificate's renewals.
// An absent field is treated as "not hosted" so that the deletion gate does not
// depend on an optional field being present.
func (c Certificate) IsHosted() bool {
	return c.HostingStatus != nil && *c.HostingStatus != -1
}

// Valid reports whether the certificate is usable at now.
func (c Certificate) Valid(now time.Time) bool {
	if c.Status != 1 && c.Status != 0 && c.Status != 4 && c.Status != 5 && c.Status != 8 && c.Status != 13 {
		// Only issued/valid states reach deployment; the caller usually filters
		// by Deployable as well.
		if c.Status != 0 {
			return false
		}
	}
	if c.NotAfter.IsZero() {
		return true
	}
	return !now.Before(c.NotBefore) && now.Before(c.NotAfter)
}

// Resource is any object that can hold a certificate (a CDN domain or an
// EdgeOne site domain).
type Resource struct {
	Account   string
	Product   Product
	ZoneID    string // EdgeOne only
	ID        string // CDN: domain; EdgeOne: zone/domain pair
	Name      string
	Domain    string
	Status    string
	BoundCert string // currently bound certificate ID ("" when unbound)

	// BoundMode is EdgeOne's Certificate.Mode: sslcert / eofreecert /
	// eofreecert_manual / disable. Empty for CDN.
	BoundMode string
	// BoundForeign marks a bound certificate that is NOT in the SSL pool
	// (typically an EdgeOne free certificate, see decision T9).
	BoundForeign bool
	// BoundNotAfter is the expiry reported by the product side, used only for
	// cross-checking against the SSL pool (decision T10).
	BoundNotAfter time.Time
	// CertState is the product side certificate state, kept apart from the
	// resource status because the two use different enums (decision T8): CDN
	// Https.SslStatus (closed/deploying/deployed/failed) or EdgeOne
	// CertificateInfo.Status (deployed/processing/applying/failed/issued).
	CertState string

	// InstanceKey is the product specific identifier used by
	// DeployCertificateInstance, pre-rendered during enumeration.
	InstanceKey string
}

// BindingDetail is one product/region entry of the association query.
type BindingDetail struct {
	Product    string
	Region     string
	TotalCount int
	Err        string
}

// BindingEvidence answers "is this certificate used by any cloud resource?"
// It comes from SSL's CreateCertificateBindResourceSyncTask (decision T4).
type BindingEvidence struct {
	CertID     string
	Status     uint64 // 0 querying, 1 success, 2 failed
	Complete   bool   // Status == 1 and every detail has an empty Err
	TotalCount int
	Details    []BindingDetail
	QueriedAt  time.Time
	CacheTime  string
}

// ChangeKind classifies one planned change.
type ChangeKind string

// Change kinds.
const (
	KindApply       ChangeKind = "apply"
	KindDelete      ChangeKind = "delete"
	KindUpToDate    ChangeKind = "uptodate"
	KindNoCert      ChangeKind = "nocert"
	KindForeign     ChangeKind = "foreign"
	KindNeedsManual ChangeKind = "needs_manual"
	KindBlocked     ChangeKind = "blocked"
)

// Risk expresses how disruptive a change is.
type Risk string

// Risk levels.
const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// Strategy names an execution path (plan §2.2).
type Strategy string

// Strategies.
const (
	// StrategyProduct uses each product's native API:
	// CDN ModifyDomainConfig(Route=Https.CertInfo.CertId),
	// EdgeOne ModifyHostsCertificate(Mode=sslcert).
	StrategyProduct Strategy = "product"
	// StrategyDeploy uses SSL DeployCertificateInstance for both products.
	StrategyDeploy Strategy = "deploy"
	// StrategyReplace uses SSL UpdateCertificateInstance(old -> new). Not
	// verified yet, therefore gated behind --enable-experimental-write.
	StrategyReplace Strategy = "replace"
)

// CertRef is a compact certificate reference for plans and audit records.
type CertRef struct {
	ID       string
	Alias    string
	Source   string
	NotAfter time.Time
}

// Ref builds a CertRef from a certificate.
func Ref(c Certificate) *CertRef {
	return &CertRef{ID: c.ID, Alias: c.Alias, Source: c.From, NotAfter: c.NotAfter}
}

// Change is one row of a plan. Reasons carry message keys (`core.*`) so that
// presentation layers can translate them (plan §4.1).
type Change struct {
	Account      string
	Product      Product
	ZoneID       string
	ResourceID   string
	ResourceName string
	Domain       string
	Kind         ChangeKind
	Strategy     Strategy
	// InstanceKey is the product-specific identifier the unified deploy path
	// (DeployCertificateInstance) needs; empty when the product cannot supply it.
	InstanceKey string
	From        *CertRef
	To          *CertRef
	Risk        Risk
	Reasons     []string
}

// NeedsWrite reports whether the change mutates cloud state.
func (c Change) NeedsWrite() bool {
	return c.Kind == KindApply || c.Kind == KindDelete
}

// Plan is the full set of changes for one account.
type Plan struct {
	Account     string
	GeneratedAt time.Time
	Changes     []Change
}

// ApplyCount counts changes that would write to the cloud.
func (p Plan) ApplyCount() int {
	n := 0
	for _, c := range p.Changes {
		if c.NeedsWrite() {
			n++
		}
	}
	return n
}

// Count returns how many changes have the given kind.
func (p Plan) Count(kind ChangeKind) int {
	n := 0
	for _, c := range p.Changes {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

// Pending returns only the changes that require a write.
func (p Plan) Pending() []Change {
	out := make([]Change, 0, len(p.Changes))
	for _, c := range p.Changes {
		if c.NeedsWrite() {
			out = append(out, c)
		}
	}
	return out
}

// CleanCandidate is a certificate eligible for deletion.
type CleanCandidate struct {
	Cert         Certificate
	SupersededBy string
	Evidence     BindingEvidence
}

// CleanSkipped is a certificate kept back, with the reason message key.
type CleanSkipped struct {
	Cert   Certificate
	Reason string
}

// CleanPlan is the outcome of the cleanup gates.
type CleanPlan struct {
	Account     string
	GeneratedAt time.Time
	Candidates  []CleanCandidate
	Skipped     []CleanSkipped
}

// Result records the outcome of executing one change.
type Result struct {
	Change         Change
	OK             bool
	ErrCode        string
	Err            error
	DeployRecordID string
	Verified       bool
	MessageKey     string
}

func sortChanges(changes []Change) {
	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.Product != b.Product {
			return a.Product < b.Product
		}
		if a.Domain != b.Domain {
			return a.Domain < b.Domain
		}
		return a.ResourceID < b.ResourceID
	})
}
