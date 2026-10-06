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
	"strings"
	"sync"
	"time"

	cdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	ssl "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205"
	sts "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sts/v20180813"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"

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
	sts      stsAPI
	limiters map[string]*limiter
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

// New builds a provider bound to one account.
func New(account string, opts Options) (*Provider, error) {
	opts = opts.withDefaults()
	sslClient, cdnClient, teoClient, stsClient, err := newSDKClients(opts)
	if err != nil {
		return nil, err
	}
	return newWithClients(account, opts, sslClient, cdnClient, teoClient, stsClient), nil
}

func newWithClients(account string, opts Options, sslClient sslAPI, cdnClient cdnAPI, teoClient teoAPI, stsClients ...stsAPI) *Provider {
	now := time.Now
	var stsClient stsAPI
	if len(stsClients) > 0 {
		stsClient = stsClients[0]
	}
	return &Provider{
		account: account,
		opts:    opts,
		ssl:     sslClient,
		cdn:     cdnClient,
		teo:     teoClient,
		sts:     stsClient,
		now:     now,
		sleep:   sleepCtx,
		limiters: map[string]*limiter{
			"ssl": newLimiter(sslQPS, now, sleepCtx),
			"cdn": newLimiter(cdnQPS, now, sleepCtx),
			"teo": newLimiter(teoQPS, now, sleepCtx),
			"sts": newLimiter(sslQPS, now, sleepCtx),
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

// Preflight validates credentials and permissions per product.
//
// A single failing product is reported on its own line: "UnauthorizedOperation"
// alone does not reveal whether SSL, CDN or EdgeOne refused the call, and a
// sub-account with a broad policy can still hit a product that is not activated
// for the account.
func (p *Provider) Preflight(ctx context.Context, _ string) ([]core.Check, error) {
	checks := make([]core.Check, 0, 6)

	// Identity first: "no permission" is often "granted on another sub-account".
	if p.sts != nil {
		checks = append(checks, p.identityCheck(ctx))
	}

	probe := func(key, action string, call func() error) {
		if err := call(); err != nil {
			code, hint, _ := ErrorInfo(err)
			checks = append(checks, core.Check{
				Key: key, Status: core.CheckFail,
				Err:        withAction(err, p.account, action),
				Detail:     code,
				MessageKey: hint,
			})
			return
		}
		checks = append(checks, core.Check{Key: key, Status: core.CheckOK, Detail: action})
	}

	probe("cloud.check.ssl", "ssl:DescribeCertificates", func() error {
		req := ssl.NewDescribeCertificatesRequest()
		req.Limit = common.Uint64Ptr(1)
		_, err := call(ctx, p.limiter("ssl"), p.opts.MaxAttempts, func() (*ssl.DescribeCertificatesResponse, error) {
			return p.ssl.DescribeCertificates(req)
		})
		return err
	})
	probe("cloud.check.cdn", "cdn:DescribeDomainsConfig", func() error {
		req := cdn.NewDescribeDomainsConfigRequest()
		req.Limit = common.Int64Ptr(1)
		_, err := call(ctx, p.limiter("cdn"), p.opts.MaxAttempts, func() (*cdn.DescribeDomainsConfigResponse, error) {
			return p.cdn.DescribeDomainsConfig(req)
		})
		return err
	})
	probe("cloud.check.teo", "teo:DescribeZones", func() error {
		req := teo.NewDescribeZonesRequest()
		req.Limit = common.Int64Ptr(1)
		_, err := call(ctx, p.limiter("teo"), p.opts.MaxAttempts, func() (*teo.DescribeZonesResponse, error) {
			return p.teo.DescribeZones(req)
		})
		return err
	})

	checks = append(checks, core.Check{
		Key:    "cloud.check.service_role",
		Status: core.CheckWarn,
		Detail: "SSL_QCSLinkedRoleInReplaceLoadCertificate is verified on the first write",
	})
	return checks, nil
}

// identityCheck reports which account the stored credential belongs to.
func (p *Provider) identityCheck(ctx context.Context) core.Check {
	key := "cloud.check.identity"
	req := sts.NewGetCallerIdentityRequest()
	resp, err := call(ctx, p.limiter("sts"), p.opts.MaxAttempts, func() (*sts.GetCallerIdentityResponse, error) {
		return p.sts.GetCallerIdentity(req)
	})
	if err != nil {
		code, hint, _ := ErrorInfo(err)
		return core.Check{
			Key: key, Status: core.CheckFail, Detail: code, MessageKey: hint,
			Err: withAction(err, p.account, "sts:GetCallerIdentity"),
		}
	}
	if resp == nil || resp.Response == nil {
		return core.Check{Key: key, Status: core.CheckWarn, Detail: "empty response"}
	}
	r := resp.Response
	return core.Check{
		Key:    key,
		Status: core.CheckOK,
		Detail: fmt.Sprintf("AccountId=%s Type=%s UserId=%s", str(r.AccountId), str(r.Type), str(r.UserId)),
	}
}

// Scan implements cloud.Provider: one pass over certificates, both products and
// the association evidence. Product-level failures become warnings so that a
// single unavailable product cannot break the whole inventory.
func (p *Provider) Scan(ctx context.Context, _ string, scope cloud.ScanScope) (cloud.ScanResult, error) {
	var res cloud.ScanResult

	p.emit("progress.stage.certificates")
	// The certificate pool is mandatory: without it nothing can be matched.
	certs, err := p.fetchCertificates(ctx)
	if err != nil {
		return res, withAction(err, p.account, "ssl:DescribeCertificates")
	}
	res.Certificates = certs
	p.emit("progress.stage.certificates_done", "count", len(certs))

	want := map[string]bool{}
	for _, name := range scope.Products {
		want[name] = true
	}
	all := len(want) == 0

	if all || want["cdn"] {
		p.emit("progress.stage.cdn")
		items, err := p.listCDNDomains(ctx)
		if err == nil {
			p.emit("progress.stage.cdn_done", "count", len(items))
		}
		if err != nil {
			res.Warnings = append(res.Warnings, p.warning("cdn", "cdn:DescribeDomainsConfig", err))
		} else {
			res.Resources = append(res.Resources, items...)
		}
	}
	if all || want["teo"] {
		p.emit("progress.stage.teo")
		items, warnings, err := p.listTEODomains(ctx)
		if err != nil {
			res.Warnings = append(res.Warnings, p.warning("teo", "teo:DescribeZones", err))
		} else {
			res.Resources = append(res.Resources, items...)
			res.Warnings = append(res.Warnings, warnings...)
			p.emit("progress.stage.teo_done", "count", len(items))
		}
	}

	ids := make([]string, 0, len(certs))
	for _, c := range certs {
		ids = append(ids, c.ID)
	}
	if scope.SkipEvidence {
		// update only needs the pool and the resources; skip the slowest step.
		res.Evidence = map[string]core.BindingEvidence{}
		return res, nil
	}
	if len(ids) > 0 {
		p.emit("progress.stage.bindings", "count", len(ids))
		if !scope.CachedEvidence {
			// Say this up front: the live association query is an async cloud
			// task and routinely takes minutes on the first run.
			p.emit("progress.bindings.first_run", "timeout", p.opts.PollTimeout.String())
		}
	}
	evidence, err := p.fetchBindings(ctx, ids, scope.CachedEvidence)
	if err != nil {
		res.Warnings = append(res.Warnings, p.warning("ssl", "ssl:CreateCertificateBindResourceSyncTask + DescribeCertificateBindResourceTaskResult", err))
	} else {
		res.Evidence = evidence
		p.emit("progress.stage.bindings_done", "count", len(evidence))
		// An unfinished query is not an error, but it is incomplete evidence:
		// surface it instead of silently reporting zero references.
		if pending := countUnfinished(evidence); pending > 0 {
			res.Warnings = append(res.Warnings, cloud.ScanWarning{
				Product:    "ssl",
				Action:     "ssl:DescribeCertificateBindResourceTaskResult",
				Code:       "EvidenceIncomplete",
				MessageKey: MsgEvidenceIncomplete,
				Err:        fmt.Errorf("%d certificate(s) were still being queried after %s", pending, p.opts.PollTimeout),
			})
		}
	}
	return res, nil
}

// countUnfinished reports how many association queries never reached status 1.
func countUnfinished(evidence map[string]core.BindingEvidence) int {
	n := 0
	for _, ev := range evidence {
		if ev.Status != 1 {
			n++
		}
	}
	return n
}

// emit forwards a progress event when reporting is enabled.
func (p *Provider) emit(key string, kv ...any) {
	if p.opts.Progress == nil {
		return
	}
	p.opts.Progress(cloud.Progress{Key: key, Args: kv})
}

func (p *Provider) warning(product, action string, err error) cloud.ScanWarning {
	code, key, _ := ErrorInfo(err)
	return cloud.ScanWarning{
		Product:    product,
		Action:     action,
		Code:       code,
		MessageKey: key,
		Err:        withAction(err, p.account, action),
	}
}

// ListCertificates implements cloud.Provider.
func (p *Provider) ListCertificates(ctx context.Context, _ string, _ cloud.ScanScope) ([]core.Certificate, error) {
	certs, err := p.fetchCertificates(ctx)
	if err != nil {
		return nil, withAction(err, p.account, "ssl:DescribeCertificates")
	}
	return certs, nil
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
		teoResources, _, err := p.listTEODomains(ctx)
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
	ev, err := p.fetchBindings(ctx, certIDs, false)
	if err != nil {
		return nil, withAction(err, p.account, "ssl:CreateCertificateBindResourceSyncTask")
	}
	return ev, nil
}

// Apply implements cloud.Provider.
//
// Changes are grouped by (product, target certificate, strategy) because the API
// allows only one deploy task per certificate and resource type at a time
// (decision T7). Groups are independent, so they run with bounded concurrency;
// results are reassembled in group order so output stays deterministic.
func (p *Provider) Apply(ctx context.Context, _ string, plan core.Plan, opts cloud.ApplyOptions) ([]core.Result, error) {
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
	if len(order) == 0 {
		return nil, nil
	}

	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(order) {
		concurrency = len(order)
	}

	type outcome struct {
		results []core.Result
		err     error
	}
	outcomes := make([]outcome, len(order))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, key := range order {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, key groupKey) {
			defer wg.Done()
			defer func() { <-sem }()
			results, err := p.runGroup(ctx, key, groups[key])
			outcomes[i] = outcome{results: results, err: err}
		}(i, key)
	}
	wg.Wait()

	var (
		results []core.Result
		fatal   error
	)
	for _, out := range outcomes {
		results = append(results, out.results...)
		if out.err != nil {
			if abortOn(out.err) && fatal == nil {
				fatal = out.err
				continue
			}
			// Non-fatal: the affected changes are already reported as failures.
		}
	}
	if fatal != nil {
		return results, wrap(fatal)
	}
	return results, nil
}

type groupKey struct {
	product  core.Product
	certID   string
	strategy core.Strategy
}

// runGroup executes every change of one (product, certificate, strategy) group.
func (p *Provider) runGroup(ctx context.Context, key groupKey, changes []core.Change) ([]core.Result, error) {
	p.emit("progress.apply.group",
		"product", string(key.product), "count", len(changes), "cert", key.certID)

	switch {
	case key.strategy == core.StrategyDeploy:
		results := p.applyUnified(ctx, key.product, key.certID, changes)
		return results, firstFailure(results)
	case key.product == core.ProductCDN:
		return p.applyCDNGroup(ctx, key.certID, changes)
	case key.product == core.ProductTEO:
		return p.applyTEOGroup(ctx, key.certID, changes)
	default:
		return failedResults(changes, MsgDeployFailed,
			fmt.Errorf("product %s has no write path in this batch", key.product)), nil
	}
}

// firstFailure surfaces an abort-worthy error (auth/role) from a result set.
func firstFailure(results []core.Result) error {
	for _, r := range results {
		if r.Err != nil && abortOn(r.Err) {
			return r.Err
		}
	}
	return nil
}

// applyCDNGroup writes each domain through the route-based partial update.
func (p *Provider) applyCDNGroup(ctx context.Context, certID string, changes []core.Change) ([]core.Result, error) {
	results := make([]core.Result, 0, len(changes))
	for i, ch := range changes {
		p.emit("progress.apply.item", "index", i+1, "total", len(changes), "domain", ch.Domain, "cert", certID)
		res := core.Result{Change: ch, OK: true}
		if err := p.setCDNCertificate(ctx, ch.Domain, certID); err != nil {
			p.emit("progress.apply.failed", "domain", ch.Domain, "code", Code(err))
			results = append(results, failure(ch, err))
			continue
		}
		p.emit("progress.apply.verifying", "domain", ch.Domain)
		verified, state, err := p.verifyCDN(ctx, ch.Domain, certID)
		switch {
		case err != nil:
			results = append(results, failure(ch, err))
		case !verified:
			res.Verified = false
			res.MessageKey = MsgVerifyFailed
			res.Err = fmt.Errorf("cdn domain %s did not report certificate %s (state %q)", ch.Domain, certID, state)
			p.emit("progress.apply.unverified", "domain", ch.Domain, "state", state)
			results = append(results, res)
		default:
			res.Verified = true
			p.emit("progress.apply.verified", "domain", ch.Domain)
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
		p.emit("progress.apply.item", "index", len(results)+1, "total", len(changes), "domain", strings.Join(hosts, ", "), "cert", certID)
		if err := p.setTEOCertificate(ctx, zoneID, hosts, certID); err != nil {
			for _, ch := range group {
				p.emit("progress.apply.failed", "domain", ch.Domain, "code", Code(err))
				results = append(results, failure(ch, err))
			}
			continue
		}
		for _, ch := range group {
			p.emit("progress.apply.verifying", "domain", ch.Domain)
		}
		// One read per poll round covers every host of the site.
		verified, states, err := p.verifyTEOBatch(ctx, zoneID, hosts, certID)
		for _, ch := range group {
			key := strings.ToLower(ch.Domain)
			switch {
			case err != nil:
				results = append(results, failure(ch, err))
			case !verified[key]:
				state := states[key]
				p.emit("progress.apply.unverified", "domain", ch.Domain, "state", state)
				results = append(results, core.Result{
					Change:     ch,
					OK:         true,
					Verified:   false,
					MessageKey: MsgVerifyFailed,
					Err:        fmt.Errorf("edgeone host %s did not report mode %s with certificate %s (state %q)", ch.Domain, teoModeSSLCert, certID, state),
				})
			default:
				p.emit("progress.apply.verified", "domain", ch.Domain)
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
	p.emit("progress.apply.deploy", "count", len(instanceIDs))
	if _, err := p.deployInstances(ctx, certID, string(product), "", instanceIDs); err != nil {
		for _, ch := range changes {
			results = append(results, failure(ch, err))
		}
		return results
	}
	for _, ch := range changes {
		p.emit("progress.apply.verifying", "domain", ch.Domain)
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
			p.emit("progress.apply.verified", "domain", ch.Domain)
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
