// Package tencent will hold the Tencent Cloud implementation of cloud.Provider.
//
// M0 ships only the seam: every method reports ErrNotImplemented so that the CLI
// fails with an actionable message instead of a stack trace. M1 fills this in
// with:
//
//	ssl:  DescribeCertificates, CreateCertificateBindResourceSyncTask,
//	      DescribeCertificateBindResourceTaskResult,
//	      DescribeHostCdnInstanceList, DescribeHostTeoInstanceList,
//	      DeployCertificateInstance, DeleteCertificates, ...
//	cdn:  DescribeDomainsConfig, ModifyDomainConfig(Route=Https.CertInfo.CertId)
//	teo:  DescribeZones, DescribeAccelerationDomains,
//	      ModifyHostsCertificate(Mode=sslcert)
//
// Facts and field names are recorded in docs/TENCENT_API_NOTES.md.
package tencent

import (
	"context"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/core"
)

// Options configures the provider.
type Options struct {
	// SecretID and SecretKey are never logged; use credential.Redact for output.
	SecretID  string
	SecretKey string
	// Region is required by the SDK client even though SSL and the CDN/EdgeOne
	// deployment paths do not need it (decision T6).
	Region string
	// Endpoint overrides the default API endpoint (tests only).
	Endpoint string
}

// Provider is the Tencent Cloud implementation.
type Provider struct {
	account string
	opts    Options
}

// New returns a provider bound to one account.
func New(account string, opts Options) *Provider {
	if opts.Region == "" {
		opts.Region = "ap-guangzhou"
	}
	return &Provider{account: account, opts: opts}
}

// Name implements cloud.Provider.
func (p *Provider) Name() string { return "tencent" }

// Capabilities implements cloud.Provider.
func (p *Provider) Capabilities() cloud.Capabilities {
	return cloud.Capabilities{
		CertGlobal: true,
		Products: map[core.Product]cloud.ProductCapability{
			core.ProductCDN: {
				CanEnumerate: true, CanReadBoundCert: true, CanApplyByCertID: true,
				CanVerifyAfterDeploy: true, Granularity: "domain",
				OwnCertField: "Https.CertInfo.CertId",
			},
			core.ProductTEO: {
				CanEnumerate: true, CanReadBoundCert: true, CanApplyByCertID: true,
				CanVerifyAfterDeploy: true, Granularity: "domain",
				OwnCertField: "Certificate.List[].CertId",
			},
		},
		DeployNeedsRegion:   map[core.Product]bool{core.ProductCDN: false, core.ProductTEO: false},
		AsyncDeploy:         true,
		DeploySerialPerPair: true,
		SupportsUnbind:      false,
		VerifyAfterDeploy:   true,
		ExtraScopes:         []string{"zone"},
	}
}

// Preflight implements cloud.Provider.
func (p *Provider) Preflight(context.Context, string) ([]core.Check, error) {
	return nil, cloud.ErrNotImplemented
}

// ListCertificates implements cloud.Provider.
func (p *Provider) ListCertificates(context.Context, string, cloud.ScanScope) ([]core.Certificate, error) {
	return nil, cloud.ErrNotImplemented
}

// ListResources implements cloud.Provider.
func (p *Provider) ListResources(context.Context, string, cloud.ScanScope) ([]core.Resource, error) {
	return nil, cloud.ErrNotImplemented
}

// ListBindings implements cloud.Provider.
func (p *Provider) ListBindings(context.Context, string, []string) (map[string]core.BindingEvidence, error) {
	return nil, cloud.ErrNotImplemented
}

// Apply implements cloud.Provider.
func (p *Provider) Apply(context.Context, string, core.Plan, cloud.ApplyOptions) ([]core.Result, error) {
	return nil, cloud.ErrNotImplemented
}

// DeleteCertificates implements cloud.Provider.
func (p *Provider) DeleteCertificates(context.Context, string, []string, cloud.DeleteOptions) ([]core.Result, error) {
	return nil, cloud.ErrNotImplemented
}

// UploadCertificate implements cloud.Provider (reserved for decision A7).
func (p *Provider) UploadCertificate(context.Context, string, cloud.LocalCert) (string, error) {
	return "", cloud.ErrNotImplemented
}
