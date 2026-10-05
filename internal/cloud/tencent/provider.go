// Package tencent implements cloud.Provider on top of the official Tencent
// Cloud Go SDK.
//
// Wiring follows the plan:
//
//	read   SSL DescribeCertificates / CreateCertificateBindResourceSyncTask
//	       SSL DescribeHostCdnInstanceList / DescribeHostTeoInstanceList
//	       CDN DescribeDomainsConfig, EdgeOne DescribeZones + DescribeAccelerationDomains
//	write  CDN ModifyDomainConfig(Route=Https.CertInfo.CertId)
//	       EdgeOne ModifyHostsCertificate(Mode=sslcert)
//	       SSL DeployCertificateInstance (strategy "deploy")
//	erase  SSL DeleteCertificates(IsSync=true) + DescribeDeleteCertificatesTaskResult
//
// Every action is followed by a read-back verification; a 200 is never treated
// as proof of success (plan §2.2).
package tencent

import (
	"context"
	"fmt"
	"time"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	ssl "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/core"
)

// Provider is the Tencent Cloud implementation of cloud.Provider.
type Provider struct {
	account  string
	opts     Options
	ssl      sslAPI
	cdn      cdnAPI
	teo      teoAPI
	limiters map[string]*limiter
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

// New builds a provider bound to one account.
func New(account string, opts Options) (*Provider, error) {
	opts = opts.withDefaults()
	sslClient, cdnClient, teoClient, err := newSDKClients(opts)
	if err != nil {
		return nil, err
	}
	return newWithClients(account, opts, sslClient, cdnClient, teoClient), nil
}

func newWithClients(account string, opts Options, sslClient sslAPI, cdnClient cdnAPI, teoClient teoAPI) *Provider {
	now := time.Now
	return &Provider{
		account: account,
		opts:    opts,
		ssl:     sslClient,
		cdn:     cdnClient,
		teo:     teoClient,
		now:     now,
		sleep:   sleepCtx,
		limiters: map[string]*limiter{
			"ssl": newLimiter(sslQPS, now, sleepCtx),
			"cdn": newLimiter(cdnQPS, now, sleepCtx),
			"teo": newLimiter(teoQPS, now, sleepCtx),
		},
	}
}

func (p *Provider) limiter(name string) *limiter { return p.limiters[name] }

// Name implements cloud.Provider.
func (p *Provider) Name() string { return "tencent" }

// Capabilities implements cloud.Provider.
func (p *Provider) Capabilities() cloud.Capabilities {
	return cloud.Capabilities{
		CertGlobal: true,
		Products: map[core.Product]cloud.ProductCapability{
			core.ProductCDN: {
				CanEnumerate: true, CanReadBoundCert: true, CanApplyByCertID: true,
				CanVerifyAfterDeploy: true, Granularity: "domain",
				OwnCertField: "Https.CertInfo.CertId",
			},
			core.ProductTEO: {
				CanEnumerate: true, CanReadBoundCert: true, CanApplyByCertID: true,
				CanVerifyAfterDeploy: true, Granularity: "domain",
				OwnCertField: "Certificate.List[].CertId",
			},
		},
		// Neither cdn nor teo requires a Region on the deploy call (decision T6).
		DeployNeedsRegion:   map[core.Product]bool{core.ProductCDN: false, core.ProductTEO: false},
		AsyncDeploy:         true,
		DeploySerialPerPair: true,
		SupportsUnbind:      false,
		VerifyAfterDeploy:   true,
		ExtraScopes:         []string{"zone"},
	}
}

// Preflight validates credentials and permissions for the account.
//
// The service role needed by deployment and association-checked deletion cannot
// be probed without writing, so it is reported as a warning rather than a pass.
func (p *Provider) Preflight(ctx context.Context, _ string) ([]core.Check, error) {
	checks := []core.Check{
		{Key: "cloud.check.credentials", Status: core.CheckOK, Detail: "DescribeCertificates"},
	}
	req := ssl.NewDescribeCertificatesRequest()
	req.Limit = common.Uint64Ptr(1)

	if _, err := call(ctx, p.limiter("ssl"), p.opts.MaxAttempts, func() (*ssl.DescribeCertificatesResponse, error) {
		return p.ssl.DescribeCertificates(req)
	}); err != nil {
		_, key, _ := ErrorInfo(err)
		checks[0] = core.Check{Key: "cloud.check.credentials", Status: core.CheckFail, Err: err, Detail: key}
		return checks, nil
	}
	checks = append(checks, core.Check{
		Key:    "cloud.check.service_role",
		Status: core.CheckWarn,
		Detail: "SSL_QCSLinkedRoleInReplaceLoadCertificate is verified on the first write",
	})
	return checks, nil
}

// ListCertificates implements cloud.Provider.
func (p *Provider) ListCertificates(ctx context.Context, _ string, _ cloud.ScanScope) ([]core.Certificate, error) {
	return p.fetchCertificates(ctx)
}

// ListResources implements cloud.Provider. Both products are enumerated unless
// the scope narrows them.
func (p *Provider) ListResources(ctx context.Context, _ string, scope cloud.ScanScope) ([]core.Resource, error) {
	want := map[string]bool{}
	for _, name := range scope.Products {
		want[name] = true
	}
	all := len(want) == 0

	var out []core.Resource
	if all || want["cdn"] {
		cdnResources, err := p.listCDNDomains(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, cdnResources...)
	}
	if all || want["teo"] {
		teoResources, err := p.listTEODomains(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, teoResources...)
	}
	return out, nil
}

// ListBindings implements cloud.Provider using the authoritative association
// query. `IsCache` is left at 0 so cleanup never acts on stale evidence.
func (p *Provider) ListBindings(ctx context.Context, _ string, certIDs []string) (map[string]core.BindingEvidence, error) {
	if len(certIDs) == 0 {
		return map[string]core.BindingEvidence{}, nil
	}
	return p.fetchBindings(ctx, certIDs, false)
}

// Apply implements cloud.Provider.
//
// Changes are grouped by (product, target certificate, strategy) and executed
// group by group: the API allows only one deploy task per certificate and
// resource type at a time (decision T7).
func (p *Provider) Apply(ctx context.Context, _ string, plan core.Plan, opts cloud.ApplyOptions) ([]core.Result, error) {
	type groupKey struct {
		product  core.Product
		certID   string
		strategy core.Strategy
	}
	order := make([]groupKey, 0, len(plan.Changes))
	groups := map[groupKey][]core.Change{}
	for _, ch := range plan.Pending() {
		if ch.Kind != core.KindApply || ch.To == nil {
			continue
		}
		key := groupKey{product: ch.Product, certID: ch.To.ID, strategy: ch.Strategy}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], ch)
	}

	var results []core.Result
	for _, key := range order {
		changes := groups[key]
		var (
			groupResults []core.Result
			err          error
		)
		switch {
		case key.strategy == core.StrategyDeploy:
			groupResults = p.applyUnified(ctx, key.product, key.certID, changes)
		case key.product == core.ProductCDN:
			groupResults, err = p.applyCDNGroup(ctx, key.certID, changes)
		case key.product == core.ProductTEO:
			groupResults, err = p.applyTEOGroup(ctx, key.certID, changes)
		default:
			groupResults = failedResults(changes, MsgDeployFailed,
				fmt.Errorf("product %s has no write path in this batch", key.product))
		}
		results = append(results, groupResults...)
		if err != nil {
			if abortOn(err) {
				return results, wrap(err)
			}
			// Non-fatal: keep going so one bad domain does not block the rest.
			results = append(results, failedResults(changes, MessageKey(err), err)...)
		}
	}
	return results, nil
}

// applyCDNGroup writes each domain through the route-based partial update.
func (p *Provider) applyCDNGroup(ctx context.Context, certID string, changes []core.Change) ([]core.Result, error) {
	results := make([]core.Result, 0, len(changes))
	for _, ch := range changes {
		res := core.Result{Change: ch, OK: true}
		if err := p.setCDNCertificate(ctx, ch.Domain, certID); err != nil {
			results = append(results, failure(ch, err))
			continue
		}
		verified, state, err := p.verifyCDN(ctx, ch.Domain, certID)
		switch {
		case err != nil:
			results = append(results, failure(ch, err))
		case !verified:
			res.Verified = false
			res.MessageKey = MsgVerifyFailed
			res.Err = fmt.Errorf("cdn domain %s did not report certificate %s (state %q)", ch.Domain, certID, state)
			results = append(results, res)
		default:
			res.Verified = true
			results = append(results, res)
		}
	}
	return results, nil
}

// applyTEOGroup batches all hosts of one site into a single call. The API
// requires every host in one call to end up with the same configuration, and
// grouping by target certificate guarantees that.
func (p *Provider) applyTEOGroup(ctx context.Context, certID string, changes []core.Change) ([]core.Result, error) {
	byZone := map[string][]core.Change{}
	order := []string{}
	for _, ch := range changes {
		if _, seen := byZone[ch.ZoneID]; !seen {
			order = append(order, ch.ZoneID)
		}
		byZone[ch.ZoneID] = append(byZone[ch.ZoneID], ch)
	}

	results := make([]core.Result, 0, len(changes))
	for _, zoneID := range order {
		group := byZone[zoneID]
		hosts := make([]string, 0, len(group))
		for _, ch := range group {
			hosts = append(hosts, ch.Domain)
		}
		if err := p.setTEOCertificate(ctx, zoneID, hosts, certID); err != nil {
			for _, ch := range group {
				results = append(results, failure(ch, err))
			}
			continue
		}
		for _, ch := range group {
			verified, state, err := p.verifyTEO(ctx, zoneID, ch.Domain, certID)
			switch {
			case err != nil:
				results = append(results, failure(ch, err))
			case !verified:
				results = append(results, core.Result{
					Change:     ch,
					OK:         true,
					Verified:   false,
					MessageKey: MsgVerifyFailed,
					Err:        fmt.Errorf("edgeone host %s did not report mode %s with certificate %s (state %q)", ch.Domain, teoModeSSLCert, certID, state),
				})
			default:
				results = append(results, core.Result{Change: ch, OK: true, Verified: true})
			}
		}
	}
	return results, nil
}

// applyUnified runs the SSL DeployCertificateInstance path (strategy "deploy").
func (p *Provider) applyUnified(ctx context.Context, product core.Product, certID string, changes []core.Change) []core.Result {
	results := make([]core.Result, 0, len(changes))
	instanceIDs := make([]string, 0, len(changes))
	for _, ch := range changes {
		if ch.InstanceKey == "" {
			results = append(results, failure(ch, errorWithKey(MsgDeployFailed,
				fmt.Errorf("%s resource %s has no deployable instance id; use the product strategy", product, ch.Domain))))
			continue
		}
		instanceIDs = append(instanceIDs, ch.InstanceKey)
	}
	if len(instanceIDs) == 0 {
		return results
	}
	if _, err := p.deployInstances(ctx, certID, string(product), "", instanceIDs); err != nil {
		for _, ch := range changes {
			results = append(results, failure(ch, err))
		}
		return results
	}
	for _, ch := range changes {
		verified, state, err := p.verifyProduct(ctx, ch)
		switch {
		case err != nil:
			results = append(results, failure(ch, err))
		case !verified:
			results = append(results, core.Result{
				Change: ch, OK: true, Verified: false, MessageKey: MsgVerifyFailed,
				Err: fmt.Errorf("resource %s did not confirm certificate %s (state %q)", ch.Domain, certID, state),
			})
		default:
			results = append(results, core.Result{Change: ch, OK: true, Verified: true})
		}
	}
	return results
}

// verifyProduct re-reads one resource through its product API.
func (p *Provider) verifyProduct(ctx context.Context, ch core.Change) (bool, string, error) {
	if ch.To == nil {
		return false, "", fmt.Errorf("change for %s has no target certificate", ch.Domain)
	}
	switch ch.Product {
	case core.ProductCDN:
		return p.verifyCDN(ctx, ch.Domain, ch.To.ID)
	case core.ProductTEO:
		return p.verifyTEO(ctx, ch.ZoneID, ch.Domain, ch.To.ID)
	default:
		return false, "", fmt.Errorf("no verification path for product %s", ch.Product)
	}
}

// DeleteCertificates implements cloud.Provider.
//
// CheckBindings is forced on regardless of the caller: the API default is the
// unsafe one, so the safe value is applied at this boundary (decision T5).
func (p *Provider) DeleteCertificates(ctx context.Context, _ string, certIDs []string, _ cloud.DeleteOptions) ([]core.Result, error) {
	if len(certIDs) == 0 {
		return nil, nil
	}
	return p.deleteCertificates(ctx, certIDs, deleteOptions{CheckBindings: true})
}

// UploadCertificate is reserved for `orbit push` (decision A7).
func (p *Provider) UploadCertificate(context.Context, string, cloud.LocalCert) (string, error) {
	return "", cloud.ErrNotImplemented
}

func failure(ch core.Change, err error) core.Result {
	code, _, _ := ErrorInfo(err)
	key := messageKeyOf(err)
	if key == "" {
		key = MsgUnknown
	}
	return core.Result{Change: ch, OK: false, Err: err, ErrCode: code, MessageKey: key}
}

func failedResults(changes []core.Change, key string, err error) []core.Result {
	out := make([]core.Result, 0, len(changes))
	code, _, _ := ErrorInfo(err)
	if mapped := messageKeyOf(err); mapped != "" && mapped != MsgUnknown {
		key = mapped
	}
	for _, ch := range changes {
		out = append(out, core.Result{Change: ch, OK: false, Err: err, ErrCode: code, MessageKey: key})
	}
	return out
}

// abortOn reports whether an error should stop the whole run: authentication and
// authorisation failures affect every following call.
func abortOn(err error) bool {
	switch Code(err) {
	case "FailedOperation.RoleNotFoundAuthorization",
		"FailedOperation.NoRealNameAuth",
		"UnauthorizedOperation.CamUnauthorized",
		"AuthFailure",
		"AuthFailure.SignatureFailure",
		"AuthFailure.SecretIdNotFound",
		"AuthFailure.TokenFailure":
		return true
	}
	return false
}
