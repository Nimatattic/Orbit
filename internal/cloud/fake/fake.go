// Package fake provides an in-memory provider used by tests and by
// `orbit --demo`, so the whole pipeline (config → provider → match → plan) can
// be exercised before the Tencent Cloud implementation lands in M1.
package fake

import (
	"context"
	"time"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/core"
)

// Provider is an in-memory Provider.
type Provider struct {
	// progress receives scan events; set by SetProgress.
	progress  func(cloud.Progress)
	Account   string
	certs     []core.Certificate
	resources []core.Resource
	evidence  map[string]core.BindingEvidence
}

// NewDemo builds the demo dataset described in testdata/demo.md.
func NewDemo() *Provider {
	now := time.Now()
	i64 := func(v int64) *int64 { return &v }
	mk := func(id, alias, cn string, sans []string, from, pkg string, beginAgo, endIn time.Duration, status uint64, hosting int64) core.Certificate {
		return core.Certificate{
			ID:            id,
			Alias:         alias,
			From:          from,
			Domain:        cn,
			SANs:          sans,
			NotBefore:     now.Add(-beginAgo),
			NotAfter:      now.Add(endIn),
			InsertTime:    now.Add(-beginAgo),
			Status:        status,
			Deployable:    true,
			PackageType:   pkg,
			HostingStatus: i64(hosting),
		}
	}

	certs := []core.Certificate{
		// Expired predecessor still bound on CDN: produces a high-risk apply.
		mk("c-1001", "1panel-example-com-2025", "example.com", []string{"example.com", "www.example.com"}, "upload", "", 400*24*time.Hour, -30*24*time.Hour, 1, -1),
		// Current, covers the wildcard: the preferred target.
		mk("c-1002", "1panel-example-com-2026", "example.com", []string{"example.com", "www.example.com", "*.example.com"}, "upload", "", 20*24*time.Hour, 340*24*time.Hour, 1, -1),
		mk("c-1003", "1panel-api-example-com-2026", "api.example.com", []string{"api.example.com"}, "upload", "", 10*24*time.Hour, 350*24*time.Hour, 1, -1),
		// Superseded and unused: the cleanup candidate.
		mk("c-1004", "1panel-test-example-com-2025", "test.example.com", []string{"test.example.com"}, "upload", "", 60*24*time.Hour, 10*24*time.Hour, 1, -1),
		mk("c-1005", "1panel-test-example-com-2026", "test.example.com", []string{"test.example.com"}, "upload", "", 5*24*time.Hour, 360*24*time.Hour, 1, -1),
		// Tencent-issued free certificate: must never be a deletion candidate (G1).
		mk("c-1006", "tencent-free-example-org", "free.example.org", []string{"free.example.org"}, "trustasia", core.PackageFreeType, 15*24*time.Hour, 75*24*time.Hour, 1, -1),
		// Hosted certificate: excluded from cleanup.
		mk("c-1007", "1panel-hosted-example-org", "hosted.example.org", []string{"hosted.example.org"}, "upload", "", 40*24*time.Hour, 300*24*time.Hour, 1, 0),
	}

	resources := []core.Resource{
		{
			Account: "demo", Product: core.ProductCDN, ID: "www.example.com", Name: "www.example.com",
			Domain: "www.example.com", Status: "online", BoundCert: "c-1001",
			BoundNotAfter: now.Add(-30 * 24 * time.Hour), InstanceKey: "www.example.com|on",
		},
		{
			Account: "demo", Product: core.ProductCDN, ID: "static.example.com", Name: "static.example.com",
			Domain: "static.example.com", Status: "online", InstanceKey: "static.example.com|on",
		},
		{
			Account: "demo", Product: core.ProductTEO, ZoneID: "zone-1", ID: "zone-1/cdn.example.com",
			Name: "cdn.example.com", Domain: "cdn.example.com", Status: "online",
			BoundCert: "teo-285inl1otbtd", BoundMode: "eofreecert", BoundForeign: true,
			BoundNotAfter: now.Add(70 * 24 * time.Hour), InstanceKey: "cdn.example.com",
		},
		{
			Account: "demo", Product: core.ProductTEO, ZoneID: "zone-1", ID: "zone-1/api.example.com",
			Name: "api.example.com", Domain: "api.example.com", Status: "online",
			BoundMode: "disable", InstanceKey: "api.example.com",
		},
	}

	complete := func(id string, total int) core.BindingEvidence {
		return core.BindingEvidence{
			CertID: id, Status: 1, Complete: true, TotalCount: total, QueriedAt: now,
			Details: []core.BindingDetail{
				{Product: "cdn", TotalCount: total},
				{Product: "teo", TotalCount: 0},
			},
		}
	}
	evidence := map[string]core.BindingEvidence{
		"c-1001": complete("c-1001", 1),
		"c-1002": complete("c-1002", 1),
		"c-1003": complete("c-1003", 1),
		"c-1004": complete("c-1004", 0),
		"c-1005": complete("c-1005", 0),
		"c-1006": complete("c-1006", 1),
		"c-1007": complete("c-1007", 0),
	}

	return &Provider{Account: "demo", certs: certs, resources: resources, evidence: evidence}
}

// SetProgress installs the progress sink (used by --demo).
func (p *Provider) SetProgress(fn func(cloud.Progress)) { p.progress = fn }

func (p *Provider) emit(key string, kv ...any) {
	if p.progress != nil {
		p.progress(cloud.Progress{Key: key, Args: kv})
	}
}

// Name implements cloud.Provider.
func (p *Provider) Name() string { return "tencent" }

// Capabilities implements cloud.Provider.
func (p *Provider) Capabilities() cloud.Capabilities {
	return cloud.Capabilities{
		CertGlobal: true,
		Products: map[core.Product]cloud.ProductCapability{
			core.ProductCDN: {CanEnumerate: true, CanReadBoundCert: true, CanApplyByCertID: true, CanVerifyAfterDeploy: true, Granularity: "domain", OwnCertField: "Https.CertInfo.CertId"},
			core.ProductTEO: {CanEnumerate: true, CanReadBoundCert: true, CanApplyByCertID: true, CanVerifyAfterDeploy: true, Granularity: "domain", OwnCertField: "Certificate.List[].CertId"},
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
func (p *Provider) Preflight(_ context.Context, _ string) ([]core.Check, error) {
	return []core.Check{
		{Key: "doctor.check.credentials", Status: core.CheckOK, Detail: "demo"},
		{Key: "cloud.check.service_role", Status: core.CheckOK, Detail: "demo"},
	}, nil
}

// Scan implements cloud.Provider by delegating to the in-memory lists.
func (p *Provider) Scan(ctx context.Context, account string, scope cloud.ScanScope) (cloud.ScanResult, error) {
	p.emit("progress.stage.certificates")
	certs, err := p.ListCertificates(ctx, account, scope)
	if err != nil {
		return cloud.ScanResult{}, err
	}
	p.emit("progress.stage.certificates_done", "count", len(certs))
	p.emit("progress.stage.cdn")
	resources, err := p.ListResources(ctx, account, scope)
	if err != nil {
		return cloud.ScanResult{}, err
	}
	cdnCount, teoCount := 0, 0
	for _, res := range resources {
		if res.Product == core.ProductCDN {
			cdnCount++
		} else {
			teoCount++
		}
	}
	p.emit("progress.stage.cdn_done", "count", cdnCount)
	p.emit("progress.stage.teo")
	p.emit("progress.teo.zones_done", "count", 1)
	p.emit("progress.teo.zone_domains", "index", 1, "zones", 1, "zone", "zone-1 (demo)")
	p.emit("progress.stage.teo_done", "count", teoCount)
	ids := make([]string, 0, len(certs))
	for _, c := range certs {
		ids = append(ids, c.ID)
	}
	p.emit("progress.stage.bindings", "count", len(ids))
	evidence, err := p.ListBindings(ctx, account, ids)
	if err != nil {
		return cloud.ScanResult{}, err
	}
	p.emit("progress.stage.bindings_done", "count", len(evidence))
	return cloud.ScanResult{Certificates: certs, Resources: resources, Evidence: evidence}, nil
}

// ListCertificates implements cloud.Provider.
func (p *Provider) ListCertificates(_ context.Context, _ string, _ cloud.ScanScope) ([]core.Certificate, error) {
	return append([]core.Certificate(nil), p.certs...), nil
}

// ListResources implements cloud.Provider.
func (p *Provider) ListResources(_ context.Context, _ string, _ cloud.ScanScope) ([]core.Resource, error) {
	return append([]core.Resource(nil), p.resources...), nil
}

// ListBindings implements cloud.Provider.
func (p *Provider) ListBindings(_ context.Context, _ string, certIDs []string) (map[string]core.BindingEvidence, error) {
	out := map[string]core.BindingEvidence{}
	for _, id := range certIDs {
		if ev, ok := p.evidence[id]; ok {
			out[id] = ev
			continue
		}
		out[id] = core.BindingEvidence{CertID: id, Status: 1, Complete: true, QueriedAt: time.Now()}
	}
	return out, nil
}

// Apply implements cloud.Provider. When not a dry run it mutates the in-memory
// state, which lets tests prove idempotency of a second plan.
func (p *Provider) Apply(_ context.Context, _ string, plan core.Plan, opts cloud.ApplyOptions) ([]core.Result, error) {
	results := make([]core.Result, 0, len(plan.Changes))
	for _, ch := range plan.Pending() {
		if ch.Kind != core.KindApply {
			continue
		}
		res := core.Result{Change: ch, OK: true, Verified: !opts.DryRun}
		if !opts.DryRun {
			for i := range p.resources {
				if p.resources[i].Product == ch.Product && p.resources[i].Domain == ch.Domain {
					p.resources[i].BoundCert = ch.To.ID
					p.resources[i].BoundMode = "sslcert"
					p.resources[i].BoundForeign = false
					if ch.To != nil {
						p.resources[i].BoundNotAfter = ch.To.NotAfter
					}
				}
			}
		}
		results = append(results, res)
	}
	return results, nil
}

// DeleteCertificates implements cloud.Provider.
func (p *Provider) DeleteCertificates(_ context.Context, _ string, certIDs []string, opts cloud.DeleteOptions) ([]core.Result, error) {
	wanted := map[string]bool{}
	for _, id := range certIDs {
		wanted[id] = true
	}
	results := make([]core.Result, 0, len(certIDs))
	kept := make([]core.Certificate, 0, len(p.certs))
	for _, c := range p.certs {
		if wanted[c.ID] {
			results = append(results, core.Result{
				Change:   core.Change{Kind: core.KindDelete, To: core.Ref(c)},
				OK:       true,
				Verified: !opts.DryRun,
			})
			if opts.DryRun {
				kept = append(kept, c)
			}
			continue
		}
		kept = append(kept, c)
	}
	p.certs = kept
	return results, nil
}

// UploadCertificate implements cloud.Provider (reserved for decision A7).
func (p *Provider) UploadCertificate(context.Context, string, cloud.LocalCert) (string, error) {
	return "", cloud.ErrNotImplemented
}
