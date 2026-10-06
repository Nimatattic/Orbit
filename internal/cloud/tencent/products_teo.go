package tencent

import (
	"context"
	"strings"
	"time"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"

	"github.com/Crequency/orbit/internal/cloud"
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
//
// A site that refuses enumeration must not take the whole product down, so
// failures are collected per site. EdgeOne Pages / AI / no-domain-access sites
// are a different product surface: they are skipped with a progress note rather
// than reported, because CDN and EdgeOne certificate deployment simply do not
// apply to them.
func (p *Provider) listTEODomains(ctx context.Context) ([]core.Resource, []cloud.ScanWarning, error) {
	zones, err := p.listZones(ctx)
	if err != nil {
		return nil, nil, err
	}
	p.emit("progress.teo.zones_done", "count", len(zones))

	var (
		out      []core.Resource
		warnings []cloud.ScanWarning
	)
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
			if zoneTypesWithoutDeployableDomains[str(zone.Type)] {
				p.emit("progress.teo.zone_skipped", "zone", name, "type", str(zone.Type))
				continue
			}
			warnings = append(warnings, p.warning("teo", "teo:DescribeAccelerationDomains", err).WithScope(name))
			continue
		}
		out = append(out, domains...)
	}
	return out, warnings, nil
}

// zoneTypesWithoutDeployableDomains are EdgeOne site types whose domains are not
// managed through the certificate deployment APIs. Skipping them is expected
// behaviour, not a warning: they belong to a different product surface.
var zoneTypesWithoutDeployableDomains = map[string]bool{
	"pages":          true,
	"ai":             true,
	"noDomainAccess": true,
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
		// A host may hold several certificates; report all of them and use the
		// one that expires last as the primary for display purposes.
		for _, info := range d.Certificate.List {
			if info == nil || str(info.CertId) == "" {
				continue
			}
			res.BoundCertIDs = append(res.BoundCertIDs, str(info.CertId))
			expires := parseTime(str(info.ExpireTime))
			if res.BoundCert == "" || expires.After(res.BoundNotAfter) {
				res.BoundCert = str(info.CertId)
				res.BoundNotAfter = expires
				res.CertState = str(info.Status)
			}
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

// verifyTEOBatch polls the site once per round and reports which hosts already
// report Mode=sslcert with the expected certificate.
//
// Verifying host by host would multiply the wait by the number of hosts (6 hosts
// x 2 minutes), while the site listing returns all of them in a single call.
func (p *Provider) verifyTEOBatch(ctx context.Context, zoneID string, domains []string, certID string) (map[string]bool, map[string]string, error) {
	want := make(map[string]bool, len(domains))
	for _, d := range domains {
		want[strings.ToLower(d)] = true
	}
	verified := map[string]bool{}
	states := map[string]string{}

	deadline := p.now().Add(p.opts.VerifyTimeout)
	start := p.now()
	for {
		items, err := p.listAccelerationDomains(ctx, zoneID)
		if err != nil {
			return verified, states, err
		}
		for _, res := range items {
			key := strings.ToLower(res.Domain)
			if !want[key] {
				continue
			}
			states[key] = res.CertState
			if res.BoundMode == teoModeSSLCert && res.HasBoundCert(certID) && res.CertState != "processing" {
				verified[key] = true
			}
		}
		if len(verified) == len(want) {
			return verified, states, nil
		}
		if !p.now().Before(deadline) {
			return verified, states, nil
		}
		pending := make([]string, 0, len(want))
		for _, d := range domains {
			if !verified[strings.ToLower(d)] {
				pending = append(pending, d)
			}
		}
		p.emit("progress.apply.waiting",
			"domain", strings.Join(pending, ", "),
			"state", states[strings.ToLower(pending[0])],
			"waited", p.now().Sub(start).Round(time.Second).String())
		if err := p.sleep(ctx, p.opts.PollInterval); err != nil {
			return verified, states, err
		}
	}
}

// verifyTEO verifies a single host.
func (p *Provider) verifyTEO(ctx context.Context, zoneID, domain, certID string) (bool, string, error) {
	verified, states, err := p.verifyTEOBatch(ctx, zoneID, []string{domain}, certID)
	if err != nil {
		return false, "", err
	}
	return verified[strings.ToLower(domain)], states[strings.ToLower(domain)], nil
}
