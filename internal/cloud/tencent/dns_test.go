package tencent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Crequency/orbit/internal/core"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	dnspod "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/dnspod/v20210323"
)

// fakeDNSPod replays the six DNS calls and records what was sent.
type fakeDNSPod struct {
	zones       []*dnspod.DomainListItem
	records     []*dnspod.RecordListItem
	zonePages   [][]*dnspod.DomainListItem
	recordPages [][]*dnspod.RecordListItem

	createCalls []*dnspod.CreateRecordRequest
	updateCalls []*dnspod.ModifyRecordRequest
	deleteCalls []*dnspod.DeleteRecordRequest
	statusCalls []*dnspod.ModifyRecordStatusRequest
	domainCalls int
	recordCalls int

	createID uint64
	err      error
}

func (f *fakeDNSPod) DescribeDomainList(req *dnspod.DescribeDomainListRequest) (*dnspod.DescribeDomainListResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	page := f.zones
	if len(f.zonePages) > 0 {
		idx := int(*req.Offset) / dnsZonePageSize
		if idx >= len(f.zonePages) {
			page = nil
		} else {
			page = f.zonePages[idx]
		}
	}
	f.domainCalls++
	return &dnspod.DescribeDomainListResponse{Response: &dnspod.DescribeDomainListResponseParams{
		DomainList: page, RequestId: common.StringPtr("req"),
	}}, nil
}

func (f *fakeDNSPod) DescribeRecordList(req *dnspod.DescribeRecordListRequest) (*dnspod.DescribeRecordListResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	page := f.records
	if len(f.recordPages) > 0 {
		idx := int(*req.Offset) / dnsRecordPageSize
		if idx >= len(f.recordPages) {
			page = nil
		} else {
			page = f.recordPages[idx]
		}
	}
	f.recordCalls++
	return &dnspod.DescribeRecordListResponse{Response: &dnspod.DescribeRecordListResponseParams{
		RecordList: page, RequestId: common.StringPtr("req"),
	}}, nil
}

func (f *fakeDNSPod) CreateRecord(req *dnspod.CreateRecordRequest) (*dnspod.CreateRecordResponse, error) {
	f.createCalls = append(f.createCalls, req)
	if f.err != nil {
		return nil, f.err
	}
	return &dnspod.CreateRecordResponse{Response: &dnspod.CreateRecordResponseParams{
		RecordId: common.Uint64Ptr(f.createID),
	}}, nil
}

func (f *fakeDNSPod) ModifyRecord(req *dnspod.ModifyRecordRequest) (*dnspod.ModifyRecordResponse, error) {
	f.updateCalls = append(f.updateCalls, req)
	if f.err != nil {
		return nil, f.err
	}
	return &dnspod.ModifyRecordResponse{Response: &dnspod.ModifyRecordResponseParams{}}, nil
}

func (f *fakeDNSPod) DeleteRecord(req *dnspod.DeleteRecordRequest) (*dnspod.DeleteRecordResponse, error) {
	f.deleteCalls = append(f.deleteCalls, req)
	if f.err != nil {
		return nil, f.err
	}
	return &dnspod.DeleteRecordResponse{Response: &dnspod.DeleteRecordResponseParams{}}, nil
}

func (f *fakeDNSPod) ModifyRecordStatus(req *dnspod.ModifyRecordStatusRequest) (*dnspod.ModifyRecordStatusResponse, error) {
	f.statusCalls = append(f.statusCalls, req)
	if f.err != nil {
		return nil, f.err
	}
	return &dnspod.ModifyRecordStatusResponse{Response: &dnspod.ModifyRecordStatusResponseParams{}}, nil
}

func dnsProvider(t *testing.T, fake *fakeDNSPod) *Provider {
	t.Helper()
	p := testProvider(t, &fakeSSL{}, &fakeCDN{}, &fakeTEO{})
	p.dnspod = fake
	return p
}

func item(name string, id uint64) *dnspod.DomainListItem {
	return &dnspod.DomainListItem{
		Name: common.StringPtr(name), DomainId: common.Uint64Ptr(id),
		Status: common.StringPtr("ENABLE"), DNSStatus: common.StringPtr("ENABLE"),
		RecordCount: common.Uint64Ptr(12), GradeTitle: common.StringPtr("免费版"),
		EffectiveDNS: []*string{common.StringPtr("ns1.dnspod.net")},
	}
}

func record(id uint64, name, typ, value, status string) *dnspod.RecordListItem {
	return &dnspod.RecordListItem{
		RecordId: common.Uint64Ptr(id), Name: common.StringPtr(name), Type: common.StringPtr(typ),
		Value: common.StringPtr(value), Status: common.StringPtr(status), TTL: common.Uint64Ptr(600),
		MX: common.Uint64Ptr(10), Line: common.StringPtr("默认"),
	}
}

func TestListZonesPagesAndMaps(t *testing.T) {
	fake := &fakeDNSPod{zonePages: [][]*dnspod.DomainListItem{
		{item("a.example", 1)},
		{item("b.example", 2)},
	}}
	// Two full pages force a second call; a short page ends the loop.
	fake.zonePages[0] = make([]*dnspod.DomainListItem, dnsZonePageSize)
	for i := range fake.zonePages[0] {
		fake.zonePages[0][i] = item("p1.example", uint64(i+1))
	}
	zones, err := dnsProvider(t, fake).ListZones(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != dnsZonePageSize+1 {
		t.Fatalf("got %d zones, want %d", len(zones), dnsZonePageSize+1)
	}
	first := zones[0]
	if first.Name != "p1.example" || first.ID != 1 || first.RecordCount != 12 ||
		first.Grade != "免费版" || len(first.EffectiveNS) != 1 {
		t.Fatalf("zone mapping is wrong: %+v", first)
	}
	if fake.domainCalls != 2 {
		t.Fatalf("expected 2 page calls, got %d", fake.domainCalls)
	}
}

func TestListRecordsPagesAndMaps(t *testing.T) {
	page := make([]*dnspod.RecordListItem, dnsRecordPageSize)
	for i := range page {
		page[i] = record(uint64(i+1), "www", "A", "203.0.113.10", "ENABLE")
	}
	fake := &fakeDNSPod{recordPages: [][]*dnspod.RecordListItem{
		page,
		{record(9999, "@", "MX", "mail.example.net", "DISABLE")},
	}}
	records, err := dnsProvider(t, fake).ListRecords(context.Background(), "main", "Example.COM.")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != dnsRecordPageSize+1 {
		t.Fatalf("got %d records", len(records))
	}
	last := records[len(records)-1]
	if last.ID != 9999 || last.Name != "@" || last.Type != "MX" || last.Enabled {
		t.Fatalf("disabled MX mapping is wrong: %+v", last)
	}
	if last.Zone != "example.com" {
		t.Fatalf("zone should be normalised, got %q", last.Zone)
	}
	if last.FQDN() != "example.com" {
		t.Fatalf("apex FQDN = %q", last.FQDN())
	}
}

func TestCreateRecordSendsTheRightFields(t *testing.T) {
	fake := &fakeDNSPod{createID: 4242}
	p := dnsProvider(t, fake)

	in := core.DNSRecordInput{
		Zone: "Example.com.", Name: "Www", Type: "a", Value: " 203.0.113.10 ",
		TTL: 900, Remark: "web", Enabled: true,
	}
	got, err := p.CreateRecord(context.Background(), "main", in)
	if err != nil {
		t.Fatal(err)
	}
	req := fake.createCalls[0]
	if str(req.Domain) != "example.com" || str(req.SubDomain) != "www" || str(req.RecordType) != "A" {
		t.Fatalf("domain/subdomain/type wrong: %+v", req)
	}
	if str(req.Value) != "203.0.113.10" {
		t.Fatalf("value should be trimmed, got %q", str(req.Value))
	}
	if str(req.RecordLine) != dnsDefaultLine {
		t.Fatalf("line should default to %q, got %q", dnsDefaultLine, str(req.RecordLine))
	}
	if str(req.Status) != "ENABLE" {
		t.Fatalf("status = %q", str(req.Status))
	}
	if req.MX != nil {
		t.Fatal("MX must not be sent for an A record")
	}
	if got.ID != 4242 || got.FQDN() != "www.example.com" {
		t.Fatalf("returned record = %+v", got)
	}
}

func TestCreateRecordApexAndDisabled(t *testing.T) {
	fake := &fakeDNSPod{}
	p := dnsProvider(t, fake)
	if _, err := p.CreateRecord(context.Background(), "main", core.DNSRecordInput{
		Zone: "example.com", Name: "", Type: "AAAA", Value: "2001:db8::1", TTL: 600, Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	req := fake.createCalls[0]
	if str(req.SubDomain) != "@" {
		t.Fatalf("empty name must become @, got %q", str(req.SubDomain))
	}
	if str(req.Status) != "DISABLE" {
		t.Fatalf("status = %q, want DISABLE", str(req.Status))
	}
}

func TestCreateRecordValidatesLocally(t *testing.T) {
	fake := &fakeDNSPod{}
	p := dnsProvider(t, fake)
	_, err := p.CreateRecord(context.Background(), "main", core.DNSRecordInput{
		Zone: "example.com", Name: "www", Type: "A", Value: "not-an-ip", TTL: 600,
	})
	if err == nil || !strings.Contains(err.Error(), "IPv4") {
		t.Fatalf("expected a local validation error, got %v", err)
	}
	if len(fake.createCalls) != 0 {
		t.Fatal("an invalid record must never reach the cloud")
	}
}

func TestUpdateDeleteAndStatus(t *testing.T) {
	fake := &fakeDNSPod{}
	p := dnsProvider(t, fake)

	if _, err := p.UpdateRecord(context.Background(), "main", core.DNSRecord{
		ID: 7, Zone: "example.com", Name: "www", Type: "A", Value: "203.0.113.11", TTL: 600, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	up := fake.updateCalls[0]
	if u64(up.RecordId) != 7 || str(up.Value) != "203.0.113.11" || str(up.Status) != "ENABLE" {
		t.Fatalf("update request is wrong: %+v", up)
	}

	if err := p.DeleteRecord(context.Background(), "main", "example.com", 7); err != nil {
		t.Fatal(err)
	}
	if u64(fake.deleteCalls[0].RecordId) != 7 {
		t.Fatal("delete should target the record id")
	}

	if err := p.SetRecordEnabled(context.Background(), "main", "example.com", 7, false); err != nil {
		t.Fatal(err)
	}
	if str(fake.statusCalls[0].Status) != "DISABLE" {
		t.Fatalf("status = %q", str(fake.statusCalls[0].Status))
	}
	if err := p.SetRecordEnabled(context.Background(), "main", "example.com", 7, true); err != nil {
		t.Fatal(err)
	}
	if str(fake.statusCalls[1].Status) != "ENABLE" {
		t.Fatalf("status = %q", str(fake.statusCalls[1].Status))
	}
}

func TestUpdateRequiresAnID(t *testing.T) {
	fake := &fakeDNSPod{}
	p := dnsProvider(t, fake)
	if _, err := p.UpdateRecord(context.Background(), "main", core.DNSRecord{
		Zone: "example.com", Name: "www", Type: "A", Value: "203.0.113.11", TTL: 600,
	}); err == nil {
		t.Fatal("an update without an id must be refused")
	}
	if len(fake.updateCalls) != 0 {
		t.Fatal("no call should be made")
	}
}

func TestDNSUnavailableWithoutClient(t *testing.T) {
	p := testProvider(t, &fakeSSL{}, &fakeCDN{}, &fakeTEO{}) // dnspod left nil
	if _, err := p.ListZones(context.Background(), "main"); err == nil {
		t.Fatal("a provider without a DNS client must say so")
	} else if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestDNSPropagatesCloudErrors(t *testing.T) {
	fake := &fakeDNSPod{err: errors.New("cloud said no")}
	p := dnsProvider(t, fake)
	if _, err := p.ListZones(context.Background(), "main"); err == nil {
		t.Fatal("expected the cloud error to surface")
	}
}
