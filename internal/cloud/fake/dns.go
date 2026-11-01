package fake

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Nimatattic/Orbit/internal/core"
)

// seedDNS builds the demo zones and records used by `--demo dns`.
func (p *Provider) seedDNS() {
	p.zones = []core.DNSZone{
		{ID: 100, Name: "example.com", Status: "ENABLE", DNSStatus: "ENABLE", Grade: "免费版",
			RecordCount: 7, EffectiveNS: []string{"ns1.example-dns.net", "ns2.example-dns.net"}, UpdatedOn: "2026-10-01 10:00:00"},
		{ID: 200, Name: "example.org", Status: "ENABLE", DNSStatus: "ENABLE", Grade: "专业版",
			RecordCount: 2, EffectiveNS: []string{"ns1.example-dns.net", "ns2.example-dns.net"}, UpdatedOn: "2026-09-20 08:30:00"},
	}
	p.records = map[string][]core.DNSRecord{
		"example.com": {
			{ID: 1, Zone: "example.com", Name: "@", Type: "A", Value: "203.0.113.10", TTL: 600, Line: "默认", Enabled: true, Remark: "1panel 主机"},
			{ID: 2, Zone: "example.com", Name: "www", Type: "CNAME", Value: "static.example.net", TTL: 600, Line: "默认", Enabled: true},
			{ID: 3, Zone: "example.com", Name: "api", Type: "A", Value: "203.0.113.11", TTL: 120, Line: "默认", Enabled: true},
			{ID: 4, Zone: "example.com", Name: "cdn", Type: "CNAME", Value: "cdn.example.net", TTL: 600, Line: "默认", Enabled: true, Remark: "CDN 回源"},
			{ID: 5, Zone: "example.com", Name: "@", Type: "MX", Value: "mail.example.net", TTL: 600, MX: 10, Line: "默认", Enabled: true},
			{ID: 6, Zone: "example.com", Name: "@", Type: "TXT", Value: "v=spf1 include:example.net -all", TTL: 600, Line: "默认", Enabled: true},
			{ID: 7, Zone: "example.com", Name: "old", Type: "A", Value: "203.0.113.99", TTL: 600, Line: "默认", Enabled: false, Remark: "已停用"},
		},
		"example.org": {
			{ID: 21, Zone: "example.org", Name: "@", Type: "A", Value: "198.51.100.7", TTL: 600, Line: "默认", Enabled: true},
			{ID: 22, Zone: "example.org", Name: "_acme-challenge", Type: "TXT", Value: "demo-validation-token", TTL: 60, Line: "默认", Enabled: true, Remark: "证书验证"},
		},
	}
	p.nextRecordID = 500
}

// ListZones implements cloud.DNSProvider.
func (p *Provider) ListZones(_ context.Context, _ string) ([]core.DNSZone, error) {
	if p.zones == nil {
		return nil, fmt.Errorf("DNS is not available for this provider")
	}
	out := make([]core.DNSZone, len(p.zones))
	copy(out, p.zones)
	for i := range out {
		out[i].RecordCount = uint64(len(p.records[out[i].Name]))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

// ListRecords implements cloud.DNSProvider.
func (p *Provider) ListRecords(_ context.Context, _ string, zone string) ([]core.DNSRecord, error) {
	zone = core.NormaliseZone(zone)
	if p.records == nil {
		return nil, fmt.Errorf("DNS is not available for this provider")
	}
	records, ok := p.records[zone]
	if !ok {
		return nil, fmt.Errorf("zone %q is not hosted by this account", zone)
	}
	out := make([]core.DNSRecord, len(records))
	copy(out, records)
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}

// CreateRecord implements cloud.DNSProvider.
func (p *Provider) CreateRecord(_ context.Context, _ string, in core.DNSRecordInput) (core.DNSRecord, error) {
	in = in.WithDefaults()
	if err := core.ValidateRecordInput(in); err != nil {
		return core.DNSRecord{}, err
	}
	zone := core.NormaliseZone(in.Zone)
	if _, ok := p.records[zone]; !ok {
		return core.DNSRecord{}, fmt.Errorf("zone %q is not hosted by this account", zone)
	}
	p.nextRecordID++
	record := core.DNSRecord{
		ID: p.nextRecordID, Zone: zone, Name: core.NormaliseDNSName(in.Name),
		Type: strings.ToUpper(in.Type), Value: strings.TrimSpace(in.Value),
		TTL: in.TTL, MX: in.MX, Line: dnsLine(in.Line), Enabled: in.Enabled, Remark: in.Remark,
	}
	p.records[zone] = append(p.records[zone], record)
	return record, nil
}

// UpdateRecord implements cloud.DNSProvider.
func (p *Provider) UpdateRecord(_ context.Context, _ string, record core.DNSRecord) (core.DNSRecord, error) {
	zone := core.NormaliseZone(record.Zone)
	in := core.DNSRecordInput{
		Zone: zone, Name: record.Name, Type: record.Type, Value: record.Value,
		TTL: record.TTL, MX: record.MX, Line: record.Line, Enabled: record.Enabled,
	}
	if err := core.ValidateRecordInput(in); err != nil {
		return core.DNSRecord{}, err
	}
	for i, existing := range p.records[zone] {
		if existing.ID != record.ID {
			continue
		}
		record.Zone = zone
		record.Name = core.NormaliseDNSName(record.Name)
		record.Type = strings.ToUpper(record.Type)
		record.Line = dnsLine(record.Line)
		p.records[zone][i] = record
		return record, nil
	}
	return core.DNSRecord{}, fmt.Errorf("record %d not found in %s", record.ID, zone)
}

// DeleteRecord implements cloud.DNSProvider.
func (p *Provider) DeleteRecord(_ context.Context, _ string, zone string, recordID uint64) error {
	zone = core.NormaliseZone(zone)
	for i, existing := range p.records[zone] {
		if existing.ID == recordID {
			p.records[zone] = append(p.records[zone][:i], p.records[zone][i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("record %d not found in %s", recordID, zone)
}

// SetRecordEnabled implements cloud.DNSProvider.
func (p *Provider) SetRecordEnabled(_ context.Context, _ string, zone string, recordID uint64, enabled bool) error {
	zone = core.NormaliseZone(zone)
	for i, existing := range p.records[zone] {
		if existing.ID == recordID {
			p.records[zone][i].Enabled = enabled
			return nil
		}
	}
	return fmt.Errorf("record %d not found in %s", recordID, zone)
}

// dnsLine mirrors the provider default.
func dnsLine(line string) string {
	if strings.TrimSpace(line) == "" {
		return "默认"
	}
	return line
}

var _ interface {
	ListZones(ctx context.Context, account string) ([]core.DNSZone, error)
} = (*Provider)(nil)
