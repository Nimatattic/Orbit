package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Nimatattic/Orbit/internal/core"
	"github.com/Nimatattic/Orbit/internal/exitcode"
)

// dnsJSONDocs decodes every document on stdout and fails if anything else (a
// human summary, a dry-run hint, a lookup warning) leaked into the stream.
func dnsJSONDocs(t *testing.T, app *App) []any {
	t.Helper()
	docs := decodeJSONDocs(t, app.testOut.Bytes())
	if len(docs) == 0 {
		t.Fatalf("expected at least one JSON document, stdout was %q", app.testOut.String())
	}
	for _, doc := range docs {
		if bad := uppercaseKeys(doc, "$"); len(bad) > 0 {
			t.Fatalf("JSON keys must be snake_case, Go field names leaked: %v", bad)
		}
	}
	return docs
}

func TestDNSZonesJSONIsSnakeCase(t *testing.T) {
	app, provider := dnsFixture(t)
	provider.dnsZones[0].RecordCount = 2
	provider.dnsZones[0].EffectiveNS = []string{"ns1.example.net", "ns2.example.net"}
	app.JSON = true

	if err := app.runDNSZones(context.Background()); err != nil {
		t.Fatalf("zones --json: %v", err)
	}
	dnsJSONDocs(t, app)

	var rows []dnsZoneRow
	if err := json.Unmarshal(app.testOut.Bytes(), &rows); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if len(rows) != 1 || rows[0].Account != "main" || rows[0].Zone.Name != "example.com" {
		t.Fatalf("unexpected zone document: %+v", rows)
	}
	if rows[0].Zone.RecordCount != 2 || len(rows[0].Zone.EffectiveNS) != 2 {
		t.Fatalf("zone fields must survive the DTO: %+v", rows[0].Zone)
	}
}

func TestDNSListJSONIsSnakeCase(t *testing.T) {
	app, _ := dnsFixture(t)
	app.JSON = true

	if err := app.runDNSList(context.Background(), "example.com"); err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	dnsJSONDocs(t, app)

	var records []recordDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &records); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if len(records) != 2 {
		t.Fatalf("expected two records, got %+v", records)
	}
	for _, r := range records {
		if r.FQDN == "" || r.Zone != "example.com" || r.Type == "" {
			t.Fatalf("record document is incomplete: %+v", r)
		}
	}

	// An empty result set must be [] rather than null.
	app.testOut.Reset()
	app.DNSFilterType = "MX"
	if err := app.runDNSList(context.Background(), "example.com"); err != nil {
		t.Fatalf("ls --json (empty): %v", err)
	}
	if got := strings.TrimSpace(app.testOut.String()); got != "[]" {
		t.Fatalf("an empty listing must marshal as [], got %q", got)
	}
}

func TestDNSSetJSONDocuments(t *testing.T) {
	app, _ := dnsFixture(t)
	app.JSON = true

	// 1. A missing record: plan mode describes the create without writing.
	if err := app.runDNSSet(context.Background(), "example.com", "app", "A", "203.0.113.50"); err != nil {
		t.Fatalf("set --json (plan): %v", err)
	}
	dnsJSONDocs(t, app)
	var planned dnsWriteDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &planned); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if planned.Action != "create" || planned.Applied || planned.Verified != nil {
		t.Fatalf("a plan must be create/not-applied without verification: %+v", planned)
	}
	if len(planned.Records) != 1 || planned.Records[0].FQDN != "app.example.com" || planned.Records[0].Value != "203.0.113.50" {
		t.Fatalf("the plan must describe the intended record: %+v", planned.Records)
	}

	// 2. --yes actually writes and reports the read-back.
	app.testOut.Reset()
	app.Yes = true
	if err := app.runDNSSet(context.Background(), "example.com", "app", "A", "203.0.113.50"); err != nil {
		t.Fatalf("set --json --yes: %v", err)
	}
	dnsJSONDocs(t, app)
	var created dnsWriteDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &created); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if created.Action != "create" || !created.Applied || created.Verified == nil || !*created.Verified {
		t.Fatalf("a create run must be applied+verified: %+v", created)
	}
	if len(created.Records) != 1 || created.Records[0].ID == 0 {
		t.Fatalf("the created record must carry its id: %+v", created.Records)
	}

	// 3. Re-running the same command is a no-op, reported as "keep".
	app.testOut.Reset()
	if err := app.runDNSSet(context.Background(), "example.com", "app", "A", "203.0.113.50"); err != nil {
		t.Fatalf("set --json (keep): %v", err)
	}
	dnsJSONDocs(t, app)
	var kept dnsWriteDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &kept); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if kept.Action != "keep" || kept.Applied || len(kept.Records) != 1 {
		t.Fatalf("an unchanged record must be reported as keep: %+v", kept)
	}

	// 4. An existing record with a different value is an update.
	app.testOut.Reset()
	if err := app.runDNSSet(context.Background(), "example.com", "www", "CNAME", "new.example.net"); err != nil {
		t.Fatalf("set --json (update): %v", err)
	}
	dnsJSONDocs(t, app)
	var updated dnsWriteDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &updated); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if updated.Action != "update" || !updated.Applied || len(updated.Records) != 1 ||
		updated.Records[0].Value != "new.example.net" {
		t.Fatalf("unexpected update document: %+v", updated)
	}
}

func TestDNSRemoveAndToggleJSONDocuments(t *testing.T) {
	app, _ := dnsFixture(t)
	app.JSON = true

	// rm plan mode: nothing is deleted, but the victims are named.
	if err := app.runDNSRemove(context.Background(), "example.com", []string{"2"}); err != nil {
		t.Fatalf("rm --json (plan): %v", err)
	}
	dnsJSONDocs(t, app)
	var plannedDelete dnsWriteDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &plannedDelete); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if plannedDelete.Action != "delete" || plannedDelete.Applied {
		t.Fatalf("a delete plan must not claim it ran: %+v", plannedDelete)
	}
	if len(plannedDelete.Records) != 1 || plannedDelete.Records[0].ID != 2 {
		t.Fatalf("the plan must name the victim record: %+v", plannedDelete.Records)
	}

	// rm --yes: deleted and reported.
	app.testOut.Reset()
	app.Yes = true
	if err := app.runDNSRemove(context.Background(), "example.com", []string{"2"}); err != nil {
		t.Fatalf("rm --json --yes: %v", err)
	}
	dnsJSONDocs(t, app)
	var removed dnsWriteDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &removed); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if removed.Action != "delete" || !removed.Applied || len(removed.Records) != 1 {
		t.Fatalf("unexpected delete document: %+v", removed)
	}

	// disable plan mode, then enable --yes: the document carries the new state.
	app.testOut.Reset()
	app.Yes = false
	if err := app.runDNSToggle(context.Background(), "example.com", []string{"1"}, false); err != nil {
		t.Fatalf("disable --json (plan): %v", err)
	}
	dnsJSONDocs(t, app)
	var plannedToggle dnsWriteDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &plannedToggle); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if plannedToggle.Action != "disable" || plannedToggle.Applied ||
		len(plannedToggle.Records) != 1 || plannedToggle.Records[0].Enabled {
		t.Fatalf("unexpected disable plan: %+v", plannedToggle)
	}

	app.testOut.Reset()
	app.Yes = true
	if err := app.runDNSToggle(context.Background(), "example.com", []string{"1"}, true); err != nil {
		t.Fatalf("enable --json --yes: %v", err)
	}
	dnsJSONDocs(t, app)
	var enabled dnsWriteDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &enabled); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	if enabled.Action != "enable" || !enabled.Applied ||
		len(enabled.Records) != 1 || !enabled.Records[0].Enabled {
		t.Fatalf("unexpected enable document: %+v", enabled)
	}
}

func TestDNSCheckJSONIsSnakeCase(t *testing.T) {
	app, provider := dnsFixture(t)
	// A CAA record keeps the test offline: resolveValues rejects the type before
	// any DNS query is made, and the command still has to emit its document.
	provider.dnsRecords["example.com"] = append(provider.dnsRecords["example.com"], core.DNSRecord{
		ID: 3, Zone: "example.com", Name: "@", Type: "CAA", Value: `0 issue "ca.example"`, TTL: 600, Enabled: true,
	})
	app.JSON = true

	err := app.runDNSCheck(context.Background(), "example.com", "@", "CAA")
	if code := exitcode.CodeOf(err); code != exitcode.PendingChanges {
		t.Fatalf("an unconfirmed record must exit 3, got %d (%v)", code, err)
	}
	var doc dnsCheckDTO
	if err := json.Unmarshal(app.testOut.Bytes(), &doc); err != nil {
		t.Fatalf("stdout must be valid JSON: %v\n%s", err, app.testOut.String())
	}
	dnsJSONDocs(t, app)
	if doc.Zone != "example.com" || doc.FQDN != "example.com" || doc.Type != "CAA" {
		t.Fatalf("unexpected check document: %+v", doc)
	}
	if len(doc.Configured) != 1 || len(doc.Answers) != 0 || len(doc.Missing) != 1 {
		t.Fatalf("check document should carry configured/answers/missing: %+v", doc)
	}
	// The lookup failure is diagnostics: stderr only, never stdout.
	if !strings.Contains(app.testErr.String(), "example.com") {
		t.Fatalf("the lookup failure should still be reported on stderr: %q", app.testErr.String())
	}
}

func TestDNSWriteJSONStdoutIsPure(t *testing.T) {
	// Every mutating dns command must emit exactly one document on stdout, with
	// no human lines (plan text, dry-run hint) mixed in.
	for name, run := range map[string]func(*App) error{
		"set": func(a *App) error {
			return a.runDNSSet(context.Background(), "example.com", "app", "A", "203.0.113.50")
		},
		"rm": func(a *App) error {
			return a.runDNSRemove(context.Background(), "example.com", []string{"1"})
		},
		"enable": func(a *App) error {
			return a.runDNSToggle(context.Background(), "example.com", []string{"1"}, true)
		},
		"disable": func(a *App) error {
			return a.runDNSToggle(context.Background(), "example.com", []string{"1"}, false)
		},
	} {
		app, _ := dnsFixture(t)
		app.JSON = true
		app.Yes = true
		if err := run(app); err != nil {
			t.Fatalf("%s --json --yes: %v", name, err)
		}
		docs := dnsJSONDocs(t, app)
		if len(docs) != 1 {
			t.Fatalf("%s should emit exactly one document, got %d: %s", name, len(docs), app.testOut.String())
		}
	}
}
