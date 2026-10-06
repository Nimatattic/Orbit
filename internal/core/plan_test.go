package core

import (
	"testing"
	"time"
)

func planFixture(now time.Time) ([]Certificate, []Resource) {
	certs := []Certificate{
		{
			ID: "c-old", Alias: "old", From: "upload", Domain: "example.com",
			SANs:      []string{"example.com", "www.example.com"},
			NotBefore: now.Add(-400 * 24 * time.Hour), NotAfter: now.Add(-10 * 24 * time.Hour),
			Status: 1, Deployable: true,
		},
		{
			ID: "c-new", Alias: "new", From: "upload", Domain: "example.com",
			SANs:      []string{"example.com", "www.example.com", "*.example.com"},
			NotBefore: now.Add(-10 * 24 * time.Hour), NotAfter: now.Add(300 * 24 * time.Hour),
			Status: 1, Deployable: true,
		},
	}
	resources := []Resource{
		{
			Account: "demo", Product: ProductCDN, ID: "www.example.com", Name: "www.example.com",
			Domain: "www.example.com", BoundCert: "c-old", BoundNotAfter: now.Add(-10 * 24 * time.Hour),
		},
		{
			Account: "demo", Product: ProductTEO, ZoneID: "zone-1", ID: "zone-1/cdn.example.com",
			Name: "cdn.example.com", Domain: "cdn.example.com",
			BoundCert: "teo-free-1", BoundMode: "eofreecert", BoundForeign: true,
			BoundNotAfter: now.Add(60 * 24 * time.Hour),
		},
	}
	return certs, resources
}

func TestBuildUpdatePlanAppliesBetterCert(t *testing.T) {
	now := time.Now()
	certs, resources := planFixture(now)

	plan := BuildUpdatePlan(resources, certs, PlanOptions{Account: "demo", Now: now, EdgeOneReplaceFreeCert: true})
	if plan.ApplyCount() != 2 {
		t.Fatalf("want 2 applies, got %d (%+v)", plan.ApplyCount(), plan.Changes)
	}
	for _, ch := range plan.Changes {
		if ch.To == nil || ch.To.ID != "c-new" {
			t.Fatalf("change for %s should target c-new, got %+v", ch.Domain, ch.To)
		}
	}
	// The expired binding must be flagged as high risk.
	for _, ch := range plan.Changes {
		if ch.Product == ProductCDN && ch.Risk != RiskHigh {
			t.Fatalf("expired current certificate should be high risk, got %s", ch.Risk)
		}
	}
}

func TestBuildUpdatePlanIsIdempotent(t *testing.T) {
	now := time.Now()
	certs, resources := planFixture(now)

	first := BuildUpdatePlan(resources, certs, PlanOptions{Account: "demo", Now: now, EdgeOneReplaceFreeCert: true})
	if first.ApplyCount() == 0 {
		t.Fatal("fixture should need changes")
	}
	// Simulate a successful apply.
	for i := range resources {
		if resources[i].BoundCert != "" || true {
			best, ok := BestFor(resources[i].Domain, certs, now, MatchOptions{})
			if ok {
				resources[i].BoundCert = best.ID
				resources[i].BoundMode = "sslcert"
				resources[i].BoundForeign = false
				resources[i].BoundNotAfter = best.NotAfter
			}
		}
	}
	second := BuildUpdatePlan(resources, certs, PlanOptions{Account: "demo", Now: now, EdgeOneReplaceFreeCert: true})
	if second.ApplyCount() != 0 {
		t.Fatalf("second plan must be a no-op, got %d applies: %+v", second.ApplyCount(), second.Changes)
	}
	if second.Count(KindUpToDate) != len(resources) {
		t.Fatalf("want %d up-to-date rows, got %d", len(resources), second.Count(KindUpToDate))
	}
}

func TestBuildUpdatePlanKeepsEdgeOneFreeCertWhenConfigured(t *testing.T) {
	now := time.Now()
	certs, resources := planFixture(now)

	plan := BuildUpdatePlan(resources, certs, PlanOptions{Account: "demo", Now: now, EdgeOneReplaceFreeCert: false})
	var teo *Change
	for i := range plan.Changes {
		if plan.Changes[i].Product == ProductTEO {
			teo = &plan.Changes[i]
		}
	}
	if teo == nil {
		t.Fatal("expected an EdgeOne row")
	}
	if teo.Kind != KindForeign {
		t.Fatalf("EdgeOne free-certificate domain must not be rewritten, got %s", teo.Kind)
	}
	if plan.Count(KindApply) != 1 {
		t.Fatalf("only the CDN row should be applied, got %d", plan.Count(KindApply))
	}
}

func TestBuildUpdatePlanBlocksUnverifiedStrategy(t *testing.T) {
	now := time.Now()
	certs, resources := planFixture(now)

	plan := BuildUpdatePlan(resources, certs, PlanOptions{
		Account: "demo", Now: now, Strategy: "replace", EdgeOneReplaceFreeCert: true,
	})
	if plan.Count(KindBlocked) == 0 {
		t.Fatal("S-REPLACE must be blocked without --enable-experimental-write")
	}
	if plan.ApplyCount() != 0 {
		t.Fatalf("blocked changes must not be executable, got %d", plan.ApplyCount())
	}

	plan = BuildUpdatePlan(resources, certs, PlanOptions{
		Account: "demo", Now: now, Strategy: "replace", EdgeOneReplaceFreeCert: true, ExperimentalWrite: true,
	})
	if plan.Count(KindBlocked) != 0 || plan.ApplyCount() != 2 {
		t.Fatalf("with the gate open both rows should apply, got blocked=%d apply=%d",
			plan.Count(KindBlocked), plan.ApplyCount())
	}
}

func TestBuildUpdatePlanFiltersProducts(t *testing.T) {
	now := time.Now()
	certs, resources := planFixture(now)

	plan := BuildUpdatePlan(resources, certs, PlanOptions{
		Account: "demo", Now: now, Products: []string{"cdn"}, EdgeOneReplaceFreeCert: true,
	})
	for _, ch := range plan.Changes {
		if ch.Product != ProductCDN {
			t.Fatalf("product filter leaked %s", ch.Product)
		}
	}
}

func TestPlanResolvesTheCurrentCertificateAgainstThePool(t *testing.T) {
	now := time.Now()
	certs, resources := planFixture(now)

	plan := BuildUpdatePlan(resources, certs, PlanOptions{Account: "demo", Now: now, EdgeOneReplaceFreeCert: true})
	var cdn *Change
	for i := range plan.Changes {
		if plan.Changes[i].Product == ProductCDN {
			cdn = &plan.Changes[i]
		}
	}
	if cdn == nil {
		t.Fatal("expected a CDN row")
	}
	if cdn.From == nil {
		t.Fatal("the current certificate must be reported")
	}
	// c-old is part of the pool, so its source and expiry come from the pool.
	if cdn.From.Source != "upload" {
		t.Fatalf("current certificate source = %q, want upload (resolved from the pool)", cdn.From.Source)
	}
	if !cdn.From.NotAfter.Equal(certs[0].NotAfter) {
		t.Fatalf("current certificate expiry = %s, want %s", cdn.From.NotAfter, certs[0].NotAfter)
	}
}

func TestPlanKeepsForeignCurrentCertificateUnresolved(t *testing.T) {
	now := time.Now()
	_, resources := planFixture(now)
	certs := []Certificate{{
		ID: "only", From: "upload", Domain: "other.example.com", SANs: []string{"other.example.com"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(100 * 24 * time.Hour), Status: 1, Deployable: true,
	}}

	plan := BuildUpdatePlan(resources, certs, PlanOptions{Account: "demo", Now: now})
	for _, ch := range plan.Changes {
		if ch.From != nil && ch.From.Source != "" {
			t.Fatalf("an id outside the pool must stay unresolved, got %+v", ch.From)
		}
	}
}

func TestPlanTreatsAnyHeldCertificateAsUpToDate(t *testing.T) {
	// EdgeOne can hold several certificates for one host (for example RSA + SM2),
	// so "already up to date" must match the whole set, not just the primary one.
	now := time.Now()
	certs := []Certificate{{
		ID: "target", From: "upload", Domain: "api.example.com", SANs: []string{"api.example.com"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(200 * 24 * time.Hour), Status: 1, Deployable: true,
	}}
	resources := []Resource{{
		Product: ProductTEO, ZoneID: "zone-1", Domain: "api.example.com",
		BoundMode:    "sslcert",
		BoundCert:    "other", // primary entry expires sooner
		BoundCertIDs: []string{"other", "target"},
	}}

	plan := BuildUpdatePlan(resources, certs, PlanOptions{Account: "demo", Now: now})
	if plan.Count(KindUpToDate) != 1 || plan.ApplyCount() != 0 {
		t.Fatalf("a host already holding the target certificate must be up to date, got %+v", plan.Changes)
	}
}
