package core

import (
	"testing"
	"time"
)

func safetyFixture(now time.Time) ([]Certificate, map[string]BindingEvidence, []Resource) {
	i64 := func(v int64) *int64 { return &v }
	upload := func(id string, cn string, age, ttl time.Duration) Certificate {
		return Certificate{
			ID: id, Alias: id, From: "upload", Domain: cn, SANs: []string{cn},
			NotBefore: now.Add(-age), NotAfter: now.Add(ttl), InsertTime: now.Add(-age),
			Status: 1, Deployable: true, HostingStatus: i64(-1),
		}
	}
	certs := []Certificate{
		upload("old-superseded", "a.example.com", 60*24*time.Hour, 10*24*time.Hour),
		upload("new-current", "a.example.com", 5*24*time.Hour, 360*24*time.Hour),
		upload("still-used", "b.example.com", 60*24*time.Hour, 10*24*time.Hour),
		upload("only-cert", "c.example.com", 60*24*time.Hour, 200*24*time.Hour),
		upload("too-young", "d.example.com", 24*time.Hour, 300*24*time.Hour),
		upload("no-evidence", "e.example.com", 60*24*time.Hour, 300*24*time.Hour),
		upload("protected", "prod.example.com", 60*24*time.Hour, 300*24*time.Hour),
		{
			ID: "free-cert", Alias: "free", From: "trustasia", PackageType: PackageFreeType,
			Domain: "f.example.com", SANs: []string{"f.example.com"},
			NotBefore: now.Add(-60 * 24 * time.Hour), NotAfter: now.Add(30 * 24 * time.Hour),
			Status: 1, Deployable: true, HostingStatus: i64(-1),
		},
		{
			ID: "hosted-cert", Alias: "hosted", From: "upload",
			Domain: "g.example.com", SANs: []string{"g.example.com"},
			NotBefore: now.Add(-60 * 24 * time.Hour), NotAfter: now.Add(300 * 24 * time.Hour),
			Status: 1, Deployable: true, HostingStatus: i64(0),
		},
	}
	// Every candidate domain gets a newer certificate so that G3 passes for the
	// certificates we expect to be deletable.
	certs = append(certs,
		upload("new-b", "b.example.com", 5*24*time.Hour, 360*24*time.Hour),
		upload("new-d", "d.example.com", 1*24*time.Hour, 400*24*time.Hour),
		upload("new-e", "e.example.com", 5*24*time.Hour, 400*24*time.Hour),
		upload("new-prod", "prod.example.com", 5*24*time.Hour, 400*24*time.Hour),
		upload("new-f", "f.example.com", 5*24*time.Hour, 400*24*time.Hour),
		upload("new-g", "g.example.com", 5*24*time.Hour, 400*24*time.Hour),
	)

	complete := func(id string, total int) BindingEvidence {
		return BindingEvidence{CertID: id, Status: 1, Complete: true, TotalCount: total, QueriedAt: now}
	}
	evidence := map[string]BindingEvidence{
		"old-superseded": complete("old-superseded", 0),
		"new-current":    complete("new-current", 1),
		"still-used":     complete("still-used", 1),
		"only-cert":      complete("only-cert", 0),
		"too-young":      complete("too-young", 0),
		"protected":      complete("protected", 0),
		"free-cert":      complete("free-cert", 0),
		"hosted-cert":    complete("hosted-cert", 0),
		"new-b":          complete("new-b", 1),
		"new-d":          complete("new-d", 0),
		"new-e":          complete("new-e", 0),
		"new-prod":       complete("new-prod", 0),
		"new-f":          complete("new-f", 0),
		"new-g":          complete("new-g", 0),
	}
	resources := []Resource{
		{Product: ProductCDN, Domain: "b.example.com", BoundCert: "still-used"},
	}
	return certs, evidence, resources
}

func skipReason(t *testing.T, plan CleanPlan, id string) string {
	t.Helper()
	for _, s := range plan.Skipped {
		if s.Cert.ID == id {
			return s.Reason
		}
	}
	for _, c := range plan.Candidates {
		if c.Cert.ID == id {
			return ""
		}
	}
	t.Fatalf("certificate %s is neither a candidate nor a skip", id)
	return "missing"
}

func TestCleanCandidatesGates(t *testing.T) {
	now := time.Now()
	certs, evidence, resources := safetyFixture(now)

	plan := CleanCandidates(certs, evidence, resources, CleanOptions{
		Account:             "demo",
		Now:                 now,
		OlderThan:           7 * 24 * time.Hour,
		RequireFullEvidence: true,
		ProtectPatterns:     []string{"*prod*"},
	})

	candidates := map[string]bool{}
	for _, c := range plan.Candidates {
		candidates[c.Cert.ID] = true
	}
	if !candidates["old-superseded"] {
		t.Fatalf("superseded, unreferenced, old upload certificate must be a candidate; got %+v", plan)
	}
	if candidates["still-used"] {
		t.Fatal("a certificate referenced by a resource must never be a candidate")
	}

	want := map[string]string{
		"still-used":  ReasonInUse,
		"only-cert":   ReasonNotSuperseded,
		"too-young":   ReasonTooYoung,
		"no-evidence": ReasonNoEvidenceData,
		"protected":   ReasonProtected,
		"free-cert":   ReasonNotUploaded,
		"hosted-cert": ReasonHosted,
		"new-current": ReasonInUse,
	}
	for id, reason := range want {
		if got := skipReason(t, plan, id); got != reason {
			t.Errorf("%s: got reason %q, want %q", id, got, reason)
		}
	}
}

func TestCleanCandidatesIncompleteEvidence(t *testing.T) {
	now := time.Now()
	certs, evidence, resources := safetyFixture(now)
	evidence["old-superseded"] = BindingEvidence{
		CertID: "old-superseded", Status: 1, Complete: false, TotalCount: 0, QueriedAt: now,
		Details: []BindingDetail{{Product: "teo", TotalCount: 0, Err: "timeout"}},
	}
	plan := CleanCandidates(certs, evidence, resources, CleanOptions{
		Now: now, OlderThan: 7 * 24 * time.Hour, RequireFullEvidence: true,
	})
	if got := skipReason(t, plan, "old-superseded"); got != ReasonIncomplete {
		t.Fatalf("incomplete evidence must block deletion, got %q", got)
	}
	for _, c := range plan.Candidates {
		if c.Cert.ID == "old-superseded" {
			t.Fatal("a certificate with an incomplete association query must never be a candidate")
		}
	}
}

func TestCleanCandidatesRequiresFullEvidenceFlag(t *testing.T) {
	now := time.Now()
	certs, evidence, resources := safetyFixture(now)
	delete(evidence, "old-superseded")

	plan := CleanCandidates(certs, evidence, resources, CleanOptions{
		Now: now, OlderThan: 7 * 24 * time.Hour, RequireFullEvidence: false,
	})
	if got := skipReason(t, plan, "old-superseded"); got != "" {
		t.Fatalf("with the gate relaxed the certificate may be a candidate, got %q", got)
	}
}

func TestProtectedMatchesAliasAndDomain(t *testing.T) {
	now := time.Now()
	certs, evidence, resources := safetyFixture(now)
	plan := CleanCandidates(certs, evidence, resources, CleanOptions{
		Now: now, OlderThan: 7 * 24 * time.Hour, RequireFullEvidence: true,
		ProtectPatterns: []string{"prod.example.com"},
	})
	if got := skipReason(t, plan, "protected"); got != ReasonProtected {
		t.Fatalf("domain pattern must protect the certificate, got %q", got)
	}
}
