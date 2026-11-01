package tencent

import (
	"context"
	"strings"

	"github.com/Nimatattic/Orbit/internal/core"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	dnspod "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/dnspod/v20210323"
)

// dnsService reports whether the provider can manage DNS. A provider built without
// a DNSPod client (some tests, or a platform that host no DNS) says no.
func (p *Provider) dnsService() error {
	if p.dnspod == nil {
		return withAction(errDNSUnavailable, p.account, "dns")
	}
	return nil
}

var errDNSUnavailable = &dnsUnavailableError{}

type dnsUnavailableError struct{}

func (*dnsUnavailableError) Error() string { return "DNS management is not available for this account" }

// ListZones implements cloud.DNSProvider.
func (p *Provider) ListZones(ctx context.Context, _ string) ([]core.DNSZone, error) {
	if err := p.dnsService(); err != nil {
		return nil, err
	}
	var zones []core.DNSZone
	for offset := uint64(0); ; {
		req := dnspod.NewDescribeDomainListRequest()
		req.Offset = common.Int64Ptr(int64(offset))
		req.Limit = common.Int64Ptr(dnsZonePageSize)

		resp, err := call(ctx, p.limiter("dns"), p.opts.MaxAttempts, func() (*dnspod.DescribeDomainListResponse, error) {
			return p.dnspod.DescribeDomainList(req)
		})
		if err != nil {
			return nil, withAction(err, p.account, "dnspod:DescribeDomainList")
		}
		if resp == nil || resp.Response == nil {
			return zones, nil
		}
		for _, item := range resp.Response.DomainList {
			if item == nil {
				continue
			}
			zones = append(zones, core.DNSZone{
				ID:          u64(item.DomainId),
				Name:        str(item.Name),
				Status:      str(item.Status),
				DNSStatus:   str(item.DNSStatus),
				Grade:       str(item.GradeTitle),
				RecordCount: u64(item.RecordCount),
				EffectiveNS: strs(item.EffectiveDNS),
				UpdatedOn:   str(item.UpdatedOn),
			})
		}
		if uint64(len(resp.Response.DomainList)) < dnsZonePageSize {
			break
		}
		offset += dnsZonePageSize
	}
	return zones, nil
}

// ListRecords implements cloud.DNSProvider.
func (p *Provider) ListRecords(ctx context.Context, _ string, zone string) ([]core.DNSRecord, error) {
	if err := p.dnsService(); err != nil {
		return nil, err
	}
	zone = core.NormaliseZone(zone)
	var records []core.DNSRecord
	for offset := uint64(0); ; {
		req := dnspod.NewDescribeRecordListRequest()
		req.Domain = common.StringPtr(zone)
		req.Offset = common.Uint64Ptr(offset)
		req.Limit = common.Uint64Ptr(dnsRecordPageSize)

		resp, err := call(ctx, p.limiter("dns"), p.opts.MaxAttempts, func() (*dnspod.DescribeRecordListResponse, error) {
			return p.dnspod.DescribeRecordList(req)
		})
		if err != nil {
			return nil, withAction(err, p.account, "dnspod:DescribeRecordList")
		}
		if resp == nil || resp.Response == nil {
			return records, nil
		}
		for _, item := range resp.Response.RecordList {
			records = append(records, p.toDNSRecord(zone, item))
		}
		if uint64(len(resp.Response.RecordList)) < dnsRecordPageSize {
			break
		}
		offset += dnsRecordPageSize
	}
	return records, nil
}

// toDNSRecord maps one API item, tolerating nil pointers.
func (p *Provider) toDNSRecord(zone string, item *dnspod.RecordListItem) core.DNSRecord {
	if item == nil {
		return core.DNSRecord{Zone: zone}
	}
	status := strings.ToUpper(str(item.Status))
	return core.DNSRecord{
		ID:        u64(item.RecordId),
		Zone:      zone,
		Name:      core.NormaliseDNSName(str(item.Name)),
		Type:      strings.ToUpper(str(item.Type)),
		Value:     str(item.Value),
		TTL:       u64(item.TTL),
		MX:        u64(item.MX),
		Line:      str(item.Line),
		Enabled:   status != "DISABLE",
		Remark:    str(item.Remark),
		UpdatedOn: str(item.UpdatedOn),
	}
}

// CreateRecord implements cloud.DNSProvider.
func (p *Provider) CreateRecord(ctx context.Context, _ string, in core.DNSRecordInput) (core.DNSRecord, error) {
	if err := p.dnsService(); err != nil {
		return core.DNSRecord{}, err
	}
	in = in.WithDefaults()
	if err := core.ValidateRecordInput(in); err != nil {
		return core.DNSRecord{}, err
	}
	zone := core.NormaliseZone(in.Zone)

	req := dnspod.NewCreateRecordRequest()
	req.Domain = common.StringPtr(zone)
	req.SubDomain = common.StringPtr(core.NormaliseDNSName(in.Name))
	req.RecordType = common.StringPtr(strings.ToUpper(in.Type))
	req.RecordLine = common.StringPtr(dnsLine(in.Line))
	req.Value = common.StringPtr(strings.TrimSpace(in.Value))
	req.TTL = common.Uint64Ptr(in.TTL)
	req.Status = common.StringPtr(dnsStatus(in.Enabled))
	if in.Remark != "" {
		req.Remark = common.StringPtr(in.Remark)
	}
	if strings.EqualFold(in.Type, core.DNSTypeMX) {
		req.MX = common.Uint64Ptr(in.MX)
	}

	resp, err := call(ctx, p.limiter("dns"), p.opts.MaxAttempts, func() (*dnspod.CreateRecordResponse, error) {
		return p.dnspod.CreateRecord(req)
	})
	if err != nil {
		return core.DNSRecord{}, withAction(err, p.account, "dnspod:CreateRecord")
	}
	record := core.DNSRecord{
		Zone: zone, Name: core.NormaliseDNSName(in.Name), Type: strings.ToUpper(in.Type),
		Value: strings.TrimSpace(in.Value), TTL: in.TTL, MX: in.MX,
		Line: dnsLine(in.Line), Enabled: in.Enabled, Remark: in.Remark,
	}
	if resp != nil && resp.Response != nil {
		record.ID = u64(resp.Response.RecordId)
	}
	return record, nil
}

// UpdateRecord implements cloud.DNSProvider.
func (p *Provider) UpdateRecord(ctx context.Context, _ string, record core.DNSRecord) (core.DNSRecord, error) {
	if err := p.dnsService(); err != nil {
		return core.DNSRecord{}, err
	}
	if record.ID == 0 {
		return core.DNSRecord{}, withAction(&dnsInputError{"record id is required for an update"}, p.account, "dns")
	}
	in := core.DNSRecordInput{
		Zone: record.Zone, Name: record.Name, Type: record.Type, Value: record.Value,
		TTL: record.TTL, MX: record.MX, Line: record.Line, Remark: record.Remark, Enabled: record.Enabled,
	}
	if err := core.ValidateRecordInput(in); err != nil {
		return core.DNSRecord{}, err
	}
	zone := core.NormaliseZone(record.Zone)

	req := dnspod.NewModifyRecordRequest()
	req.Domain = common.StringPtr(zone)
	req.RecordId = common.Uint64Ptr(record.ID)
	req.SubDomain = common.StringPtr(core.NormaliseDNSName(record.Name))
	req.RecordType = common.StringPtr(strings.ToUpper(record.Type))
	req.RecordLine = common.StringPtr(dnsLine(record.Line))
	req.Value = common.StringPtr(strings.TrimSpace(record.Value))
	req.TTL = common.Uint64Ptr(record.TTL)
	req.Status = common.StringPtr(dnsStatus(record.Enabled))
	if record.Remark != "" {
		req.Remark = common.StringPtr(record.Remark)
	}
	if strings.EqualFold(record.Type, core.DNSTypeMX) {
		req.MX = common.Uint64Ptr(record.MX)
	}

	if _, err := call(ctx, p.limiter("dns"), p.opts.MaxAttempts, func() (*dnspod.ModifyRecordResponse, error) {
		return p.dnspod.ModifyRecord(req)
	}); err != nil {
		return core.DNSRecord{}, withAction(err, p.account, "dnspod:ModifyRecord")
	}
	out := record
	out.Zone = zone
	out.Name = core.NormaliseDNSName(record.Name)
	out.Type = strings.ToUpper(record.Type)
	out.Line = dnsLine(record.Line)
	return out, nil
}

// DeleteRecord implements cloud.DNSProvider.
func (p *Provider) DeleteRecord(ctx context.Context, _ string, zone string, recordID uint64) error {
	if err := p.dnsService(); err != nil {
		return err
	}
	req := dnspod.NewDeleteRecordRequest()
	req.Domain = common.StringPtr(core.NormaliseZone(zone))
	req.RecordId = common.Uint64Ptr(recordID)

	if _, err := call(ctx, p.limiter("dns"), p.opts.MaxAttempts, func() (*dnspod.DeleteRecordResponse, error) {
		return p.dnspod.DeleteRecord(req)
	}); err != nil {
		return withAction(err, p.account, "dnspod:DeleteRecord")
	}
	return nil
}

// SetRecordEnabled implements cloud.DNSProvider.
func (p *Provider) SetRecordEnabled(ctx context.Context, _ string, zone string, recordID uint64, enabled bool) error {
	if err := p.dnsService(); err != nil {
		return err
	}
	req := dnspod.NewModifyRecordStatusRequest()
	req.Domain = common.StringPtr(core.NormaliseZone(zone))
	req.RecordId = common.Uint64Ptr(recordID)
	req.Status = common.StringPtr(dnsStatus(enabled))

	if _, err := call(ctx, p.limiter("dns"), p.opts.MaxAttempts, func() (*dnspod.ModifyRecordStatusResponse, error) {
		return p.dnspod.ModifyRecordStatus(req)
	}); err != nil {
		return withAction(err, p.account, "dnspod:ModifyRecordStatus")
	}
	return nil
}

// dnsStatus maps the boolean to DNSPod's record status vocabulary.
func dnsStatus(enabled bool) string {
	if enabled {
		return "ENABLE"
	}
	return "DISABLE"
}

// dnsLine falls back to the default resolution line when none is given.
func dnsLine(line string) string {
	if strings.TrimSpace(line) == "" {
		return dnsDefaultLine
	}
	return line
}

// dnsInputError is a local validation failure (no cloud call involved).
type dnsInputError struct{ msg string }

func (e *dnsInputError) Error() string { return e.msg }

// ensure the interface is satisfied at compile time.
var _ = func() any {
	var p *Provider
	var _ interface {
		ListZones(context.Context, string) ([]core.DNSZone, error)
	} = p
	return nil
}
