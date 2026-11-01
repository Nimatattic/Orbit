package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Nimatattic/Orbit/internal/core"
	"github.com/Nimatattic/Orbit/internal/i18n"
)

// dnsJob builds a job backed by in-memory state, mirroring the real provider.
func dnsJob(zones []core.DNSZone, records map[string][]core.DNSRecord) (DNSJob, *dnsRecorder) {
	rec := &dnsRecorder{records: records, nextID: 900}
	return DNSJob{
		Account: "main",
		Zones:   func(context.Context) ([]core.DNSZone, error) { return zones, nil },
		Records: func(_ context.Context, zone string) ([]core.DNSRecord, error) {
			return rec.records[zone], nil
		},
		Create: func(_ context.Context, in core.DNSRecordInput) (core.DNSRecord, error) {
			if err := core.ValidateRecordInput(in); err != nil {
				return core.DNSRecord{}, err
			}
			rec.creates++
			rec.nextID++
			record := core.DNSRecord{ID: rec.nextID, Zone: in.Zone, Name: in.Name, Type: in.Type, Value: in.Value,
				TTL: in.TTL, MX: in.MX, Enabled: in.Enabled, Remark: in.Remark}
			rec.records[in.Zone] = append(rec.records[in.Zone], record)
			return record, nil
		},
		Update: func(_ context.Context, record core.DNSRecord) (core.DNSRecord, error) {
			rec.updates++
			for i, existing := range rec.records[record.Zone] {
				if existing.ID == record.ID {
					rec.records[record.Zone][i] = record
					return record, nil
				}
			}
			return core.DNSRecord{}, errors.New("not found")
		},
		Delete: func(_ context.Context, zone string, id uint64) error {
			rec.deletes++
			for i, existing := range rec.records[zone] {
				if existing.ID == id {
					rec.records[zone] = append(rec.records[zone][:i], rec.records[zone][i+1:]...)
					return nil
				}
			}
			return errors.New("not found")
		},
		Toggle: func(_ context.Context, zone string, id uint64, enabled bool) error {
			rec.toggles++
			for i, existing := range rec.records[zone] {
				if existing.ID == id {
					rec.records[zone][i].Enabled = enabled
					return nil
				}
			}
			return errors.New("not found")
		},
	}, rec
}

type dnsRecorder struct {
	records map[string][]core.DNSRecord
	nextID  uint64
	creates int
	updates int
	deletes int
	toggles int
}

func testDNSModel(t *testing.T, job DNSJob) *dnsModel {
	t.Helper()
	bundle, err := i18n.New("en-US")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := newDNSModel(ctx, cancel, bundle, job, true)
	m.width, m.height = 110, 34
	return m
}

func demoDNS() ([]core.DNSZone, map[string][]core.DNSRecord) {
	return []core.DNSZone{
		{ID: 1, Name: "example.com", RecordCount: 2, Grade: "free"},
		{ID: 2, Name: "example.org", RecordCount: 1, Grade: "pro"},
	}, map[string][]core.DNSRecord{
		"example.com": {
			{ID: 1, Zone: "example.com", Name: "@", Type: "A", Value: "203.0.113.10", TTL: 600, Enabled: true},
			{ID: 2, Zone: "example.com", Name: "www", Type: "CNAME", Value: "static.example.net", TTL: 600, Enabled: true},
		},
		"example.org": {
			{ID: 3, Zone: "example.org", Name: "@", Type: "A", Value: "198.51.100.7", TTL: 600, Enabled: true},
		},
	}
}

// loadedDNS drives the model through the initial zone and record load.
func loadedDNS(t *testing.T) (*dnsModel, *dnsRecorder) {
	t.Helper()
	zones, records := demoDNS()
	job, rec := dnsJob(zones, records)
	m := testDNSModel(t, job)
	next, _ := m.Update(dnsZonesMsg{zones: zones})
	m = next.(*dnsModel)
	next, _ = m.Update(dnsRecordsMsg{zone: "example.com", records: records["example.com"]})
	return next.(*dnsModel), rec
}

func TestDNSModelLoadsZonesThenRecords(t *testing.T) {
	m, _ := loadedDNS(t)
	if len(m.zones) != 2 || m.currentZone() != "example.com" {
		t.Fatalf("zones = %+v, current = %q", m.zones, m.currentZone())
	}
	if got := len(m.visibleRecords()); got != 2 {
		t.Fatalf("records = %d, want 2", got)
	}
	if out := m.View(); !strings.Contains(out, "example.com") || !strings.Contains(out, "static.example.net") {
		t.Fatalf("view is missing content:\n%s", out)
	}
}

func TestDNSModelCursorAndZoneSwitch(t *testing.T) {
	m, _ := loadedDNS(t)

	next, _ := m.Update(key("down"))
	m = next.(*dnsModel)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	// Past the end the cursor stays put rather than disappearing.
	next, _ = m.Update(key("down"))
	m = next.(*dnsModel)
	if m.cursor != 1 {
		t.Fatalf("cursor should clamp, got %d", m.cursor)
	}

	next, cmd := m.Update(key("right"))
	m = next.(*dnsModel)
	if m.currentZone() != "example.org" || cmd == nil {
		t.Fatalf("zone switch: zone=%q cmd=%v", m.currentZone(), cmd != nil)
	}
	// Switching zones resets the cursor and clears the filter.
	next, _ = m.Update(dnsRecordsMsg{zone: "example.org", records: []core.DNSRecord{
		{ID: 3, Zone: "example.org", Name: "@", Type: "A", Value: "198.51.100.7", TTL: 600, Enabled: true},
	}})
	m = next.(*dnsModel)
	if m.cursor != 0 || len(m.visibleRecords()) != 1 {
		t.Fatalf("cursor/records after switch: %d/%d", m.cursor, len(m.visibleRecords()))
	}
}

func TestDNSModelFilter(t *testing.T) {
	m, _ := loadedDNS(t)
	next, _ := m.Update(key("/"))
	m = next.(*dnsModel)
	if m.mode != dnsFilterMode {
		t.Fatal("filter mode should open")
	}
	m.input.SetValue("static")
	next, _ = m.Update(key("enter"))
	m = next.(*dnsModel)
	if m.mode != dnsBrowse || len(m.visibleRecords()) != 1 {
		t.Fatalf("filter did not apply: mode=%d records=%d", m.mode, len(m.visibleRecords()))
	}
	if !strings.Contains(m.recordPanel(110), "static.example.net") {
		t.Fatal("filtered panel should show the match")
	}
}

func TestDNSModelCreateThroughForm(t *testing.T) {
	m, rec := loadedDNS(t)

	next, _ := m.Update(key("n"))
	m = next.(*dnsModel)
	if m.mode != dnsFormMode {
		t.Fatal("n should open the form")
	}
	// Fields: name, type, value, ttl, mx, remark.
	m.form.inputs[0].SetValue("app")
	m.form.inputs[1].SetValue("A")
	m.form.inputs[2].SetValue("203.0.113.50")

	next, cmd := m.Update(key("enter"))
	m = next.(*dnsModel)
	if m.mode != dnsBrowse || cmd == nil {
		t.Fatalf("submit should close the form and issue a write: mode=%d cmd=%v", m.mode, cmd != nil)
	}
	msg, ok := cmd().(dnsWriteMsg)
	if !ok || msg.err != nil {
		t.Fatalf("write message = %+v", msg)
	}
	if rec.creates != 1 {
		t.Fatalf("expected one create, got %d", rec.creates)
	}

	next, _ = m.Update(msg)
	m = next.(*dnsModel)
	if m.status == "" || !strings.Contains(m.status, "app.example.com") {
		t.Fatalf("status should confirm the write, got %q", m.status)
	}
}

func TestDNSModelFormValidationStaysOpen(t *testing.T) {
	m, rec := loadedDNS(t)
	next, _ := m.Update(key("n"))
	m = next.(*dnsModel)
	m.form.inputs[1].SetValue("A")
	m.form.inputs[2].SetValue("not-an-ip")

	next, cmd := m.Update(key("enter"))
	m = next.(*dnsModel)
	if m.mode != dnsFormMode || cmd != nil {
		t.Fatal("an invalid value must keep the form open")
	}
	if m.form.err == "" {
		t.Fatal("the form should explain the validation error")
	}
	if rec.creates != 0 {
		t.Fatal("nothing may be written")
	}
}

func TestDNSModelDeleteNeedsConfirmation(t *testing.T) {
	m, rec := loadedDNS(t)
	next, _ := m.Update(key("d"))
	m = next.(*dnsModel)
	if m.mode != dnsConfirmDelete || m.pending == nil {
		t.Fatal("d should ask for confirmation")
	}
	// Cancelling leaves everything intact.
	next, _ = m.Update(key("n"))
	m = next.(*dnsModel)
	if m.mode != dnsBrowse || rec.deletes != 0 {
		t.Fatalf("cancel: mode=%d deletes=%d", m.mode, rec.deletes)
	}

	next, _ = m.Update(key("d"))
	m = next.(*dnsModel)
	next, cmd := m.Update(key("y"))
	m = next.(*dnsModel)
	if cmd == nil {
		t.Fatal("confirming should issue the delete")
	}
	if msg, ok := cmd().(dnsWriteMsg); !ok || msg.err != nil {
		t.Fatalf("delete message = %+v", msg)
	}
	if rec.deletes != 1 {
		t.Fatalf("expected one delete, got %d", rec.deletes)
	}
	next, reload := m.Update(dnsWriteMsg{op: "deleted", fqdn: "example.com"})
	m = next.(*dnsModel)
	if reload == nil {
		t.Fatal("a successful write must reload the records")
	}
	// The reload is asynchronous: feed the answer back in.
	next, _ = m.Update(reload())
	m = next.(*dnsModel)
	if len(m.records) != 1 {
		t.Fatalf("records should be reloaded after the delete, got %d", len(m.records))
	}
}

func TestDNSModelToggle(t *testing.T) {
	m, rec := loadedDNS(t)
	next, cmd := m.Update(key(" "))
	m = next.(*dnsModel)
	if cmd == nil {
		t.Fatal("space should issue a toggle")
	}
	if msg, ok := cmd().(dnsWriteMsg); !ok || msg.err != nil {
		t.Fatalf("toggle message = %+v", msg)
	}
	if rec.toggles != 1 || rec.records["example.com"][0].Enabled {
		t.Fatalf("toggle did not disable: %+v", rec.records["example.com"][0])
	}
}

func TestDNSModelShowsZoneErrors(t *testing.T) {
	job := DNSJob{
		Account: "main",
		Zones:   func(context.Context) ([]core.DNSZone, error) { return nil, errors.New("no permission") },
		Records: func(context.Context, string) ([]core.DNSRecord, error) { return nil, nil },
	}
	m := testDNSModel(t, job)
	next, _ := m.Update(dnsZonesMsg{err: errors.New("no permission")})
	m = next.(*dnsModel)
	if m.err == nil {
		t.Fatal("the error should be kept")
	}
	if out := m.View(); !strings.Contains(out, "no permission") {
		t.Fatalf("the view should show the failure:\n%s", out)
	}
}
