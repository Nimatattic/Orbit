package tencent

import (
	"context"
	"strings"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"

	"github.com/Crequency/orbit/internal/core"
)

// teoModeSSLCert is the EdgeOne certificate mode that binds a certificate from
// the SSL service. The other modes are EdgeOne-issued free certificates
// (decision T9 / B2b).
const teoModeSSLCert = "sslcert"

// listZones enumerates every EdgeOne site visible to the account. Sites are
// account level, not regional (decision T6).
func (p *Provider) listZones(ctx context.Context) ([]*teo.Zone, error) {
	var out []*teo.Zone
	var offset int64
	for {
		req := teo.NewDescribeZonesRequest()
		req.Offset = common.Int64Ptr(offset)
		req.Limit = common.Int64Ptr(teoPageSize)

		resp, err := call(ctx, p.limiter("teo"), p.opts.MaxAttempts, func() (*teo.DescribeZonesResponse, error) {
			return p.teo.DescribeZones(req)
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Response == nil || len(resp.Response.Zones) == 0 {
			break
		}
		out = append(out, resp.Response.Zones...)
		offset += int64(len(resp.Response.Zones))
		if resp.Response.TotalCount != nil && offset >= *resp.Response.TotalCount {
			break
		}
	}
	return out, nil
}

// listTEODomains enumerates every accelerated domain of every site.
func (p *Provider) listTEODomains(ctx context.Context) ([]core.Resource, error) {
	zones, err := p.listZones(ctx)
	if err != nil {
		return nil, err
	}
	p.emit("progress.teo.zones_done", "count", len(zones))
	var out []core.Resource
	for i, zone := range zones {
		if zone == nil || str(zone.ZoneId) == "" {
			continue
		}
		name := str(zone.ZoneName)
		if name == "" {
			name = str(zone.ZoneId)
		}
		p.emit("progress.teo.zone_domains", "index", i+1, "zones", len(zones), "zone", name)
		domains, err := p.listAccelerationDomains(ctx, str(zone.ZoneId))
		if err != nil {
			return nil, err
		}
		out = append(out, domains...)
	}
	return out, nil
}

func (p *Provider) listAccelerationDomains(ctx context.Context, zoneID string) ([]core.Resource, error) {
	var out []core.Resource
	var offset int64
	for {
		req := teo.NewDescribeAccelerationDomainsRequest()
		req.ZoneId = common.StringPtr(zoneID)
		req.Offset = common.Int64Ptr(offset)
		req.Limit = common.Int64Ptr(200)

		resp, err := call(ctx, p.limiter("teo"), p.opts.MaxAttempts, func() (*teo.DescribeAccelerationDomainsResponse, error) {
			return p.teo.DescribeAccelerationDomains(req)
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Response == nil || len(resp.Response.AccelerationDomains) == 0 {
			break
		}
		for _, d := range resp.Response.AccelerationDomains {
			out = append(out, toTEOResource(d))
		}
		offset += int64(len(resp.Response.AccelerationDomains))
		if resp.Response.TotalCount != nil && offset >= *resp.Response.TotalCount {
			break
		}
	}
	return out, nil
}

// toTEOResource maps one EdgeOne accelerated domain.
//
// The certificate semantics live in `Certificate.Mode`: only `sslcert` means a
// certificate from the SSL service is bound. EdgeOne free certificates appear as
// `eofreecert*` and are flagged as foreign so the planner never reads them as
// "no certificate".
func toTEOResource(d *teo.AccelerationDomain) core.Resource {
	zoneID := str(d.ZoneId)
	domain := str(d.DomainName)
	res := core.Resource{
		Product: core.ProductTEO,
		ZoneID:  zoneID,
		ID:      zoneID + "/" + domain,
		Name:    domain,
		Domain:  domain,
		Status:  str(d.DomainStatus),
		// The teo InstanceIdList form is the bare domain; domains are globally
		// unique inside EdgeOne, so no ZoneId is needed.
		InstanceKey: domain,
	}
	if d.Certificate != nil {
		res.BoundMode = str(d.Certificate.Mode)
		if len(d.Certificate.List) > 0 && d.Certificate.List[0] != nil {
			res.BoundCert = str(d.Certificate.List[0].CertId)
			res.BoundNotAfter = parseTime(str(d.Certificate.List[0].ExpireTime))
			res.CertState = str(d.Certificate.List[0].Status)
		}
		res.BoundForeign = res.BoundMode != "" && res.BoundMode != teoModeSSLCert && res.BoundMode != "disable"
	}
	return res
}

// getTEODomain re-reads one host, used for post-write verification.
func (p *Provider) getTEODomain(ctx context.Context, zoneID, domain string) (core.Resource, bool, error) {
	req := teo.NewDescribeAccelerationDomainsRequest()
	req.ZoneId = common.StringPtr(zoneID)
	req.Offset = common.Int64Ptr(0)
	req.Limit = common.Int64Ptr(1)
	req.Filters = []*teo.AdvancedFilter{{
		Name:   common.StringPtr("domain-name"),
		Values: common.StringPtrs([]string{domain}),
	}}

	resp, err := call(ctx, p.limiter("teo"), p.opts.MaxAttempts, func() (*teo.DescribeAccelerationDomainsResponse, error) {
		return p.teo.DescribeAccelerationDomains(req)
	})
	if err != nil {
		return core.Resource{}, false, err
	}
	if resp == nil || resp.Response == nil {
		return core.Resource{}, false, nil
	}
	for _, d := range resp.Response.AccelerationDomains {
		res := toTEOResource(d)
		if strings.EqualFold(res.Domain, domain) {
			return res, true, nil
		}
	}
	return core.Resource{}, false, nil
}

// setTEOCertificate binds an SSL-service certificate to a batch of hosts.
//
// Decision B7: only the site certificate is set. `ClientCertInfo` (mTLS) and
// `UpstreamCertInfo` (origin) are deliberately left unset, which the API
// documents as "keep the existing configuration".
func (p *Provider) setTEOCertificate(ctx context.Context, zoneID string, hosts []string, certID string) error {
	if len(hosts) == 0 {
		return nil
	}
	req := teo.NewModifyHostsCertificateRequest()
	req.ZoneId = common.StringPtr(zoneID)
	req.Hosts = common.StringPtrs(hosts)
	req.Mode = common.StringPtr(teoModeSSLCert)
	req.ServerCertInfo = []*teo.ServerCertInfo{{CertId: common.StringPtr(certID)}}

	_, err := call(ctx, p.limiter("teo"), p.opts.MaxAttempts, func() (*teo.ModifyHostsCertificateResponse, error) {
		return p.teo.ModifyHostsCertificate(req)
	})
	return err
}

// verifyTEO polls until the host reports Mode=sslcert with the expected id.
func (p *Provider) verifyTEO(ctx context.Context, zoneID, domain, certID string) (bool, string, error) {
	deadline := p.now().Add(p.opts.PollTimeout)
	state := ""
	for {
		res, found, err := p.getTEODomain(ctx, zoneID, domain)
		if err != nil {
			return false, state, err
		}
		if found {
			state = res.CertState
			if res.BoundMode == teoModeSSLCert && res.BoundCert == certID && res.CertState != "processing" {
				return true, state, nil
			}
		}
		if !p.now().Before(deadline) {
			return false, state, nil
		}
		if err := p.sleep(ctx, p.opts.PollInterval); err != nil {
			return false, state, err
		}
	}
}
