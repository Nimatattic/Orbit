package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Nimatattic/Orbit/internal/core"
	"github.com/Nimatattic/Orbit/internal/exitcode"
)

// dnsFixture builds a stub provider with one zone and two records.
func dnsFixture(t *testing.T) (*App, *stubProvider) {
	t.Helper()
	provider := &stubProvider{
		dnsZones: []core.DNSZone{{ID: 1, Name: "example.com", Status: "ENABLE", DNSStatus: "ENABLE", Grade: "免费版"}},
		dnsRecords: map[string][]core.DNSRecord{
			"example.com": {
				{ID: 1, Zone: "example.com", Name: "@", Type: "A", Value: "203.0.113.10", TTL: 600, Enabled: true},
				{ID: 2, Zone: "example.com", Name: "www", Type: "CNAME", Value: "static.example.net", TTL: 600, Enabled: true},
			},
		},
		dnsNextID: 100,
	}
	app := testApp(t, provider)
	app.DNSFlags.ttl = 600
	app.DNSFlags.mx = 10
	return app, provider
}

func TestDNSZonesTextAndJSON(t *testing.T) {
	app, _ := dnsFixture(t)
	if err := app.runDNSZones(context.Background()); err != nil {
		t.Fatalf("zones: %v", err)
	}
	out := app.testOut.String()
	if !strings.Contains(out, "example.com") || !strings.Contains(out, "免费版") {
		t.Fatalf("zone listing looks wrong:\n%s", out)
	}

	app.testOut.Reset()
	app.JSON = true
	if err := app.runDNSZones(context.Background()); err != nil {
		t.Fatal(err)
	}
	var rows []dnsZoneRow
	if err := json.Unmarshal(app.testOut.Bytes(), &rows); err != nil {
		t.Fatalf("json output: %v (%s)", err, app.testOut.String())
	}
	if len(rows) != 1 || rows[0].Zone.Name != "example.com" || rows[0].Account != "main" {
		t.Fatalf("unexpected json rows: %+v", rows)
	}
}

func TestDNSListFilters(t *testing.T) {
	app, _ := dnsFixture(t)
	if err := app.runDNSList(context.Background(), "Example.COM."); err != nil {
		t.Fatalf("ls: %v", err)
	}
	out := app.testOut.String()
	if !strings.Contains(out, "www") || !strings.Contains(out, "1") {
		t.Fatalf("listing is missing records:\n%s", out)
	}

	app.testOut.Reset()
	app.DNSFilterType = "cname"
	if err := app.runDNSList(context.Background(), "example.com"); err != nil {
		t.Fatal(err)
	}
	filtered := app.testOut.String()
	if strings.Contains(filtered, "CNAME  ") && strings.Count(filtered, "\n") != 2 {
		t.Fatalf("type filter let too many rows through:\n%s", filtered)
	}
	if !strings.Contains(filtered, "static.example.net") {
		t.Fatalf("type filter dropped the CNAME:\n%s", filtered)
	}
}

func TestDNSSetIsDryRunByDefault(t *testing.T) {
	app, provider := dnsFixture(t)
	if err := app.runDNSSet(context.Background(), "example.com", "app", "A", "203.0.113.50"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if provider.dnsCreates != 0 {
		t.Fatal("without --yes nothing may be written")
	}
	out := app.testOut.String()
	if !strings.Contains(out, "app.example.com") || !strings.Contains(out, "203.0.113.50") {
		t.Fatalf("dry run should describe the planned record:\n%s", out)
	}
	if !strings.Contains(out, "--yes") {
		t.Fatalf("dry run should explain how to apply:\n%s", out)
	}
}

func TestDNSSetCreatesThenKeeps(t *testing.T) {
	app, provider := dnsFixture(t)
	app.Yes = true

	if err := app.runDNSSet(context.Background(), "example.com", "app", "A", "203.0.113.50"); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if provider.dnsCreates != 1 {
		t.Fatalf("expected one create, got %d", provider.dnsCreates)
	}
	app.testOut.Reset()

	// The same command again must be a no-op: that is what makes it safe in cron.
	if err := app.runDNSSet(context.Background(), "example.com", "app", "A", "203.0.113.50"); err != nil {
		t.Fatalf("second set: %v", err)
	}
	if provider.dnsCreates != 1 || provider.dnsUpdates != 0 {
		t.Fatalf("idempotent set should not write again (creates=%d updates=%d)", provider.dnsCreates, provider.dnsUpdates)
	}
	if out := app.testOut.String(); !strings.Contains(out, "app.example.com") {
		t.Fatalf("the no-op run should still name the record:\n%s", out)
	}
}

func TestDNSSetUpdatesExistingValue(t *testing.T) {
	app, provider := dnsFixture(t)
	app.Yes = true
	if err := app.runDNSSet(context.Background(), "example.com", "www", "CNAME", "new.example.net"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if provider.dnsUpdates != 1 || provider.dnsCreates != 0 {
		t.Fatalf("expected one update, got creates=%d updates=%d", provider.dnsCreates, provider.dnsUpdates)
	}
	records := provider.dnsRecords["example.com"]
	for _, r := range records {
		if r.ID == 2 && r.Value != "new.example.net" {
			t.Fatalf("record was not updated: %+v", r)
		}
	}
}

func TestDNSSetHonoursRecordID(t *testing.T) {
	app, provider := dnsFixture(t)
	app.Yes = true
	app.DNSFlags.recordID = 2
	if err := app.runDNSSet(context.Background(), "example.com", "www", "CNAME", "pinned.example.net"); err != nil {
		t.Fatalf("set by id: %v", err)
	}
	if provider.dnsUpdates != 1 {
		t.Fatalf("expected an update by id, got %d", provider.dnsUpdates)
	}
}

func TestDNSSetRejectsInvalidRecord(t *testing.T) {
	app, provider := dnsFixture(t)
	app.Yes = true
	err := app.runDNSSet(context.Background(), "example.com", "bad", "A", "not-an-ip")
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.Fatal {
		t.Fatalf("expected a fatal validation error, got %v", err)
	}
	if provider.dnsCreates != 0 {
		t.Fatal("an invalid record must not reach the provider")
	}
}

func TestDNSRemoveRequiresYesAndReportsMissing(t *testing.T) {
	app, provider := dnsFixture(t)
	if err := app.runDNSRemove(context.Background(), "example.com", []string{"2"}); err != nil {
		t.Fatalf("dry run rm: %v", err)
	}
	if provider.dnsDeletes != 0 {
		t.Fatal("rm without --yes must not delete")
	}

	app.testOut.Reset()
	app.Yes = true
	if err := app.runDNSRemove(context.Background(), "example.com", []string{"2"}); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if provider.dnsDeletes != 1 {
		t.Fatalf("expected one delete, got %d", provider.dnsDeletes)
	}
	if got := len(provider.dnsRecords["example.com"]); got != 1 {
		t.Fatalf("one record should remain, got %d", got)
	}

	if err := app.runDNSRemove(context.Background(), "example.com", []string{"9999"}); err == nil {
		t.Fatal("removing an unknown id must fail")
	}
	if err := app.runDNSRemove(context.Background(), "example.com", []string{"abc"}); err == nil {
		t.Fatal("a non-numeric id must fail")
	}
}

func TestDNSToggle(t *testing.T) {
	app, provider := dnsFixture(t)
	app.Yes = true
	if err := app.runDNSToggle(context.Background(), "example.com", []string{"1"}, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if provider.dnsToggles != 1 || provider.dnsRecords["example.com"][0].Enabled {
		t.Fatalf("disable did not take effect: %+v", provider.dnsRecords["example.com"][0])
	}
	if err := app.runDNSToggle(context.Background(), "example.com", []string{"1"}, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !provider.dnsRecords["example.com"][0].Enabled {
		t.Fatal("enable did not take effect")
	}
}

func TestDNSWriteNeedsOneAccount(t *testing.T) {
	app, _ := dnsFixture(t)
	app.Accounts = "main,hk"
	app.Yes = true
	err := app.runDNSSet(context.Background(), "example.com", "app", "A", "203.0.113.50")
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.GateRefused {
		t.Fatalf("multi-account write must be refused, got %v", err)
	}
}
