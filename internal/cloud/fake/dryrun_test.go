package fake

import (
	"context"
	"testing"

	"github.com/Nimatattic/Orbit/internal/cloud"
	"github.com/Nimatattic/Orbit/internal/core"
)

// A dry run must be indistinguishable from a write run from the caller's point
// of view (same changes, same order) except that nothing is written: the demo
// provider is used by --demo, so its dry-run contract must match the real one.

func TestDryRunApplyDoesNotMutateDemoState(t *testing.T) {
	p := NewDemo()
	plan := core.Plan{Changes: []core.Change{{
		Product: core.ProductCDN, Domain: "static.example.com", Kind: core.KindApply,
		Strategy: core.StrategyProduct, To: &core.CertRef{ID: "c-1002"},
	}}}

	results, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run apply: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %+v", results)
	}
	res := results[0]
	if !res.OK || res.Verified {
		t.Fatalf("a dry run reports OK but never Verified: %+v", res)
	}
	if res.MessageKey != cloud.MsgDryRun {
		t.Fatalf("message key = %q, want %q", res.MessageKey, cloud.MsgDryRun)
	}

	resources, err := p.ListResources(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	for _, res := range resources {
		if res.Domain == "static.example.com" && res.BoundCert != "" {
			t.Fatalf("a dry run bound a certificate: %+v", res)
		}
	}
}

func TestDryRunDeleteKeepsCertificatesAndSaysSo(t *testing.T) {
	p := NewDemo()
	before, err := p.ListCertificates(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}

	results, err := p.DeleteCertificates(context.Background(), "demo",
		[]string{"c-1004"}, cloud.DeleteOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run delete: %v", err)
	}
	if len(results) != 1 || !results[0].OK || results[0].Verified {
		t.Fatalf("expected one OK, unverified result: %+v", results)
	}
	if results[0].MessageKey != cloud.MsgDryRun {
		t.Fatalf("message key = %q, want %q", results[0].MessageKey, cloud.MsgDryRun)
	}

	after, err := p.ListCertificates(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("a dry run deleted certificates: %d -> %d", len(before), len(after))
	}

	// The real path still deletes, so the dry run is not hiding a broken fake.
	if _, err := p.DeleteCertificates(context.Background(), "demo",
		[]string{"c-1004"}, cloud.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	final, err := p.ListCertificates(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	if len(final) != len(before)-1 {
		t.Fatalf("a real delete should remove the certificate: %d -> %d", len(before), len(final))
	}
}
