package tencent

import (
	"context"
	"encoding/json"
	"strings"

	cdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"

	"github.com/Crequency/orbit/internal/core"
)

// cdnCertRoute is the dotted config path whose leaf is the server certificate
// id. ModifyDomainConfig keeps every non-leaf setting untouched, which is why it
// is the default write path for CDN (it avoids the whole-object overwrite
// semantics of UpdateDomainConfig).
const cdnCertRoute = "Https.CertInfo.CertId"

// listCDNDomains enumerates every accelerated domain with its HTTPS state.
func (p *Provider) listCDNDomains(ctx context.Context) ([]core.Resource, error) {
	var out []core.Resource
	var offset int64
	for {
		req := cdn.NewDescribeDomainsConfigRequest()
		req.Offset = common.Int64Ptr(offset)
		req.Limit = common.Int64Ptr(cdnPageSize)

		resp, err := call(ctx, p.limiter("cdn"), p.opts.MaxAttempts, func() (*cdn.DescribeDomainsConfigResponse, error) {
			return p.cdn.DescribeDomainsConfig(req)
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Response == nil || len(resp.Response.Domains) == 0 {
			break
		}
		for _, d := range resp.Response.Domains {
			out = append(out, toCDNResource(d))
		}
		offset += int64(len(resp.Response.Domains))
		if resp.Response.TotalNumber != nil && offset >= *resp.Response.TotalNumber {
			break
		}
	}
	return out, nil
}

// toCDNResource maps one accelerated domain.
//
// `Https.SslStatus` (closed/deploying/deployed/failed) is the certificate state
// and is kept apart from the domain status: the two mean different things
// (decision T8).
func toCDNResource(d *cdn.DetailDomain) core.Resource {
	res := core.Resource{
		Product: core.ProductCDN,
		Domain:  str(d.Domain),
		ID:      str(d.Domain),
		Name:    str(d.Domain),
		Status:  str(d.Status),
	}
	if d.Https != nil {
		res.CertState = str(d.Https.SslStatus)
		if d.Https.CertInfo != nil {
			res.BoundCert = str(d.Https.CertInfo.CertId)
			res.BoundNotAfter = parseTime(str(d.Https.CertInfo.ExpireTime))
		}
	}
	return res
}

// getCDNDomain re-reads one domain, used for post-write verification.
func (p *Provider) getCDNDomain(ctx context.Context, domain string) (core.Resource, bool, error) {
	req := cdn.NewDescribeDomainsConfigRequest()
	req.Offset = common.Int64Ptr(0)
	req.Limit = common.Int64Ptr(1)
	req.Filters = []*cdn.DomainFilter{{
		Name:  common.StringPtr("domain"),
		Value: common.StringPtrs([]string{domain}),
	}}

	resp, err := call(ctx, p.limiter("cdn"), p.opts.MaxAttempts, func() (*cdn.DescribeDomainsConfigResponse, error) {
		return p.cdn.DescribeDomainsConfig(req)
	})
	if err != nil {
		return core.Resource{}, false, err
	}
	if resp == nil || resp.Response == nil {
		return core.Resource{}, false, nil
	}
	for _, d := range resp.Response.Domains {
		res := toCDNResource(d)
		if strings.EqualFold(res.Domain, domain) {
			return res, true, nil
		}
	}
	return core.Resource{}, false, nil
}

// setCDNCertificate updates only the certificate id through the dotted route.
func (p *Provider) setCDNCertificate(ctx context.Context, domain, certID string) error {
	value, err := json.Marshal(map[string]string{"update": certID})
	if err != nil {
		return err
	}
	req := cdn.NewModifyDomainConfigRequest()
	req.Domain = common.StringPtr(domain)
	req.Route = common.StringPtr(cdnCertRoute)
	req.Value = common.StringPtr(string(value))

	_, err = call(ctx, p.limiter("cdn"), p.opts.MaxAttempts, func() (*cdn.ModifyDomainConfigResponse, error) {
		return p.cdn.ModifyDomainConfig(req)
	})
	return err
}

// verifyCDN polls until the domain reports the expected certificate. A timeout
// is returned as (false, state, nil): the write was accepted but is not
// confirmed yet, which the caller reports without marking it failed.
func (p *Provider) verifyCDN(ctx context.Context, domain, certID string) (bool, string, error) {
	deadline := p.now().Add(p.opts.PollTimeout)
	state := ""
	for {
		res, found, err := p.getCDNDomain(ctx, domain)
		if err != nil {
			return false, state, err
		}
		if found {
			state = res.CertState
			if res.BoundCert == certID && res.CertState != "deploying" {
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
