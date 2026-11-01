package tencent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	ssl "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"

	"github.com/Nimatattic/Orbit/internal/cloud"
	"github.com/Nimatattic/Orbit/internal/core"
)

// This file pins the provider contracts that are easy to break silently: dry
// runs must never write, read-back budgeting, per-certificate serialisation,
// unfinished-query accounting and retry backoff.

// ------------------------------------------------ apply / delete dry runs

// cdnDomain builds one accelerated domain with its HTTPS state.
func cdnDomain(domain, certID, state string) *cdn.DetailDomain {
	d := &cdn.DetailDomain{Domain: common.StringPtr(domain), Status: common.StringPtr("online")}
	if certID != "" || state != "" {
		d.Https = &cdn.Https{SslStatus: common.StringPtr(state)}
		if certID != "" {
			d.Https.CertInfo = &cdn.ServerCert{CertId: common.StringPtr(certID)}
		}
	}
	return d
}

func cdnVerifyPlan(domain string) core.Plan {
	return core.Plan{Changes: []core.Change{{
		Product: core.ProductCDN, Domain: domain, Kind: core.KindApply,
		Strategy: core.StrategyProduct, To: &core.CertRef{ID: "c-new"},
	}}}
}

func TestApplyDryRunNeverWritesToTheCloud(t *testing.T) {
	cdnFake := &fakeCDN{domains: []*cdn.DetailDomain{cdnDomain("www.example.com", "c-old", "deployed")}}
	p := testProvider(t, &fakeSSL{}, cdnFake, &fakeTEO{})

	plan := cdnVerifyPlan("www.example.com")
	results, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run apply: %v", err)
	}
	if len(cdnFake.modifyReqs) != 0 {
		t.Fatalf("a dry run must not call ModifyDomainConfig, got %d call(s)", len(cdnFake.modifyReqs))
	}
	if got := str(cdnFake.domains[0].Https.CertInfo.CertId); got != "c-old" {
		t.Fatalf("the bound certificate changed on a dry run: %q", got)
	}
	if len(results) != 1 || !results[0].OK || results[0].Verified {
		t.Fatalf("a dry run reports OK but never Verified: %+v", results)
	}
	if results[0].MessageKey != cloud.MsgDryRun {
		t.Fatalf("message key = %q, want %q", results[0].MessageKey, cloud.MsgDryRun)
	}
}

func TestApplyDryRunStillReportsLocalFailures(t *testing.T) {
	// The unified deploy path needs an instance key. That check is local, so a
	// dry run must report exactly what a real run would report: a failure.
	p := testProvider(t, &fakeSSL{}, &fakeCDN{}, &fakeTEO{})
	plan := core.Plan{Changes: []core.Change{{
		Product: core.ProductCDN, Domain: "www.example.com", Kind: core.KindApply,
		Strategy: core.StrategyDeploy, To: &core.CertRef{ID: "c-new"},
	}}}
	results, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run apply: %v", err)
	}
	if len(results) != 1 || results[0].OK {
		t.Fatalf("a locally impossible change must fail even on a dry run: %+v", results)
	}
	if results[0].MessageKey != MsgDeployFailed {
		t.Fatalf("message key = %q, want %q", results[0].MessageKey, MsgDeployFailed)
	}
}

func TestDeleteDryRunNeverCallsTheCloud(t *testing.T) {
	f := &fakeSSL{}
	p := testProvider(t, f, &fakeCDN{}, &fakeTEO{})

	results, err := p.DeleteCertificates(context.Background(), "demo",
		[]string{"c-1", "c-2"}, cloud.DeleteOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run delete: %v", err)
	}
	if len(f.deleteRequests) != 0 {
		t.Fatalf("a dry run must not call ssl:DeleteCertificates, got %d call(s)", len(f.deleteRequests))
	}
	if len(results) != 2 {
		t.Fatalf("every requested certificate must be reported, got %+v", results)
	}
	for _, res := range results {
		if !res.OK || res.Verified {
			t.Fatalf("a dry run reports OK but never Verified: %+v", res)
		}
		if res.MessageKey != cloud.MsgDryRun {
			t.Fatalf("message key = %q, want %q", res.MessageKey, cloud.MsgDryRun)
		}
		if res.Change.Kind != core.KindDelete || res.Change.To == nil || res.Change.To.ID == "" {
			t.Fatalf("the dry-run result must describe a delete: %+v", res)
		}
	}
}

// ----------------------------------------------- read-back budget (CDN)

// laggingCDN reports the pre-write certificate for the first stale reads, to
// simulate the propagation delay of a CDN configuration change.
type laggingCDN struct {
	inner  *fakeCDN
	before []*cdn.DetailDomain
	stale  int
	calls  int
}

func (l *laggingCDN) DescribeDomainsConfig(*cdn.DescribeDomainsConfigRequest) (*cdn.DescribeDomainsConfigResponse, error) {
	l.calls++
	if l.calls <= l.stale {
		return &cdn.DescribeDomainsConfigResponse{Response: &cdn.DescribeDomainsConfigResponseParams{
			TotalNumber: common.Int64Ptr(int64(len(l.before))),
			Domains:     l.before,
		}}, nil
	}
	return l.inner.DescribeDomainsConfig(nil)
}

func (l *laggingCDN) ModifyDomainConfig(req *cdn.ModifyDomainConfigRequest) (*cdn.ModifyDomainConfigResponse, error) {
	return l.inner.ModifyDomainConfig(req)
}

// providerWithClock wires fakes with a virtual clock and explicit timeouts.
func providerWithClock(t *testing.T, opts Options, s sslAPI, c cdnAPI, e teoAPI) *Provider {
	t.Helper()
	opts = opts.withDefaults()
	clk := newClock()
	p := newWithClients("demo", opts, s, c, e)
	p.now, p.sleep = clk.now, clk.sleep
	p.limiters = map[string]*limiter{
		"ssl": newLimiter(1000, clk.now, clk.sleep),
		"cdn": newLimiter(1000, clk.now, clk.sleep),
		"teo": newLimiter(1000, clk.now, clk.sleep),
	}
	return p
}

func TestCDNReadBackUsesVerifyTimeoutNotPollTimeout(t *testing.T) {
	// A 10s propagation delay is normal for CDN. With PollTimeout at 3s the
	// read-back used to give up; VerifyTimeout (60s here) must keep polling.
	inner := &fakeCDN{domains: []*cdn.DetailDomain{cdnDomain("www.example.com", "c-old", "deployed")}}
	lag := &laggingCDN{
		inner:  inner,
		before: []*cdn.DetailDomain{cdnDomain("www.example.com", "c-old", "deployed")},
		stale:  10,
	}
	p := providerWithClock(t, Options{
		Region: "ap-guangzhou", PollInterval: time.Second,
		PollTimeout: 3 * time.Second, VerifyTimeout: 60 * time.Second, MaxAttempts: 3,
	}, &fakeSSL{}, lag, &fakeTEO{})

	results, err := p.Apply(context.Background(), "demo", cdnVerifyPlan("www.example.com"), cloud.ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(results) != 1 || !results[0].Verified {
		t.Fatalf("PollTimeout must not cut the CDN read-back short: %+v (reads=%d)", results, lag.calls)
	}
	if lag.calls <= 10 {
		t.Fatalf("expected the read-back to outlive the propagation delay, got %d read(s)", lag.calls)
	}
}

func TestCDNReadBackStopsAtVerifyTimeout(t *testing.T) {
	// The mirror image: a short VerifyTimeout must end the read-back long before
	// PollTimeout (60s), proving the budget is no longer borrowed.
	inner := &fakeCDN{domains: []*cdn.DetailDomain{cdnDomain("www.example.com", "c-old", "deployed")}}
	lag := &laggingCDN{
		inner:  inner,
		before: []*cdn.DetailDomain{cdnDomain("www.example.com", "c-old", "deployed")},
		stale:  1000,
	}
	p := providerWithClock(t, Options{
		Region: "ap-guangzhou", PollInterval: time.Second,
		PollTimeout: 60 * time.Second, VerifyTimeout: 2 * time.Second, MaxAttempts: 3,
	}, &fakeSSL{}, lag, &fakeTEO{})

	results, err := p.Apply(context.Background(), "demo", cdnVerifyPlan("www.example.com"), cloud.ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(results) != 1 || results[0].Verified || results[0].MessageKey != MsgVerifyFailed {
		t.Fatalf("an unconfirmed write must be reported unverified: %+v", results)
	}
	if lag.calls > 5 {
		t.Fatalf("the read-back should stop at VerifyTimeout, got %d read(s)", lag.calls)
	}
}

// ------------------------------------------------------ per-certificate order

// overlapProbe records the peak number of writes that were in flight at once.
type overlapProbe struct {
	active int32
	peak   int32
	hold   time.Duration
}

func (o *overlapProbe) enter() {
	n := atomic.AddInt32(&o.active, 1)
	for {
		peak := atomic.LoadInt32(&o.peak)
		if n <= peak || atomic.CompareAndSwapInt32(&o.peak, peak, n) {
			break
		}
	}
	time.Sleep(o.hold)
	atomic.AddInt32(&o.active, -1)
}

func TestApplySerialisesEveryStrategyForOneCertificate(t *testing.T) {
	// Decision T7: one deploy task per certificate and resource type at a time.
	// Two strategies writing the same certificate used to be two independent
	// groups and could overlap; they must now run one after another.
	probe := &overlapProbe{hold: 40 * time.Millisecond}
	cdnFake := &fakeCDN{
		domains: []*cdn.DetailDomain{
			cdnDomain("a.example.com", "c-old", "deployed"),
			cdnDomain("b.example.com", "c-old", "deployed"),
		},
		modifyHook: probe.enter,
	}
	f := &fakeSSL{
		deployHook:    probe.enter,
		deployRecords: []*ssl.DeployRecordInfo{{Id: common.Uint64Ptr(42), Status: common.Uint64Ptr(1)}},
	}
	p := testProvider(t, f, cdnFake, &fakeTEO{})

	plan := core.Plan{Changes: []core.Change{
		{Product: core.ProductCDN, Domain: "a.example.com", Kind: core.KindApply, Strategy: core.StrategyProduct, To: &core.CertRef{ID: "c-new"}},
		{Product: core.ProductCDN, Domain: "b.example.com", InstanceKey: "b.example.com", Kind: core.KindApply, Strategy: core.StrategyDeploy, To: &core.CertRef{ID: "c-new"}},
	}}
	if _, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{Concurrency: 4}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if peak := atomic.LoadInt32(&probe.peak); peak != 1 {
		t.Fatalf("two strategies for one certificate overlapped (peak concurrency %d)", peak)
	}
}

func TestApplyKeepsDifferentCertificatesIndependent(t *testing.T) {
	// Serialising per certificate must not serialise the whole run. The two
	// writes live in different fakes so the test stays free of shared-state
	// races while the overlap probe observes them concurrently.
	probe := &overlapProbe{hold: 40 * time.Millisecond}
	cdnFake := &fakeCDN{
		domains:    []*cdn.DetailDomain{cdnDomain("a.example.com", "c-old", "deployed")},
		modifyHook: probe.enter,
	}
	teoFake := &fakeTEO{
		zones: []*teo.Zone{{ZoneId: common.StringPtr("zone-1")}},
		domains: map[string][]*teo.AccelerationDomain{
			"zone-1": {{ZoneId: common.StringPtr("zone-1"), DomainName: common.StringPtr("b.example.com"), DomainStatus: common.StringPtr("online")}},
		},
		modifyHook: probe.enter,
	}
	p := testProvider(t, &fakeSSL{}, cdnFake, teoFake)

	plan := core.Plan{Changes: []core.Change{
		{Product: core.ProductCDN, Domain: "a.example.com", Kind: core.KindApply, Strategy: core.StrategyProduct, To: &core.CertRef{ID: "c-new-a"}},
		{Product: core.ProductTEO, ZoneID: "zone-1", Domain: "b.example.com", Kind: core.KindApply, Strategy: core.StrategyProduct, To: &core.CertRef{ID: "c-new-b"}},
	}}
	if _, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{Concurrency: 2}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if peak := atomic.LoadInt32(&probe.peak); peak < 2 {
		t.Fatalf("different certificates should still run concurrently, peak %d", peak)
	}
}

// --------------------------------------------------- unfinished query status

func TestCountUnfinishedOnlyCountsRunningQueries(t *testing.T) {
	evidence := map[string]core.BindingEvidence{
		"running": {CertID: "running", Status: 0},
		"done":    {CertID: "done", Status: 1},
		"failed":  {CertID: "failed", Status: 2},
	}
	if got := countUnfinished(evidence); got != 1 {
		t.Fatalf("only status 0 is unfinished, got %d", got)
	}
}

func TestScanDoesNotReportFailedBindingQueryAsUnfinished(t *testing.T) {
	f := &fakeSSL{
		certs: []*ssl.Certificates{sslCert("c-1", "a", "example.com", nil, "upload", "", "2027-01-01 00:00:00", ptr(-1))},
		bindCreateResponse: &ssl.CreateCertificateBindResourceSyncTaskResponse{
			Response: &ssl.CreateCertificateBindResourceSyncTaskResponseParams{
				CertTaskIds: []*ssl.CertTaskId{{CertId: common.StringPtr("c-1"), TaskId: common.StringPtr("t-1")}},
			},
		},
		bindPollResponses: []*ssl.DescribeCertificateBindResourceTaskResultResponse{{
			Response: &ssl.DescribeCertificateBindResourceTaskResultResponseParams{
				SyncTaskBindResourceResult: []*ssl.SyncTaskBindResourceResult{{
					TaskId: common.StringPtr("t-1"),
					Status: common.Uint64Ptr(2),
					Error:  &ssl.Error{Message: common.StringPtr("query failed")},
				}},
			},
		}},
	}
	p := testProvider(t, f, &fakeCDN{}, &fakeTEO{})

	res, err := p.Scan(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("a finished-with-error query is not pending work, yet it warned: %+v", res.Warnings)
	}
	if ev := res.Evidence["c-1"]; ev.Status != 2 || ev.Complete {
		t.Fatalf("a failed query must stay incomplete evidence: %+v", ev)
	}
}

// ------------------------------------------------------- progress event args

// progressArg reads one key of the flat key/value Args list.
func progressArg(args []any, name string) string {
	for i := 0; i+1 < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok || key != name {
			continue
		}
		value, _ := args[i+1].(string)
		return value
	}
	return ""
}

func TestApplyItemEventsCarryTheProduct(t *testing.T) {
	// The same hostname can be managed by CDN and EdgeOne at once. Per-item
	// events must name the product so a consumer can tell the two rows apart
	// instead of folding both into one.
	var mu sync.Mutex
	var events []cloud.Progress
	opts := Options{
		Region: "ap-guangzhou", PollInterval: time.Second,
		PollTimeout: 30 * time.Second, MaxAttempts: 3,
	}
	opts.Progress = func(ev cloud.Progress) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	}

	cdnFake := &fakeCDN{domains: []*cdn.DetailDomain{cdnDomain("www.example.com", "c-old", "deployed")}}
	teoFake := &fakeTEO{
		zones: []*teo.Zone{{ZoneId: common.StringPtr("zone-1")}},
		domains: map[string][]*teo.AccelerationDomain{
			"zone-1": {{ZoneId: common.StringPtr("zone-1"), DomainName: common.StringPtr("www.example.com"), DomainStatus: common.StringPtr("online")}},
		},
	}
	p := providerWithClock(t, opts, &fakeSSL{}, cdnFake, teoFake)

	plan := core.Plan{Changes: []core.Change{
		{Product: core.ProductCDN, Domain: "www.example.com", Kind: core.KindApply, Strategy: core.StrategyProduct, To: &core.CertRef{ID: "c-cdn"}},
		{Product: core.ProductTEO, ZoneID: "zone-1", Domain: "www.example.com", Kind: core.KindApply, Strategy: core.StrategyProduct, To: &core.CertRef{ID: "c-teo"}},
	}}
	if _, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{Concurrency: 2}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	keys := []string{"progress.apply.item", "progress.apply.verifying", "progress.apply.verified"}
	seen := map[string]bool{}
	for _, ev := range events {
		isItem := false
		for _, key := range keys {
			if ev.Key == key {
				isItem = true
			}
		}
		if !isItem {
			continue
		}
		product := progressArg(ev.Args, "product")
		domain := progressArg(ev.Args, "domain")
		if product == "" {
			t.Fatalf("%s for %q carries no product: %v", ev.Key, domain, ev.Args)
		}
		seen[ev.Key+"|"+product+"|"+domain] = true
	}
	for _, key := range keys {
		for _, product := range []string{string(core.ProductCDN), string(core.ProductTEO)} {
			want := key + "|" + product + "|www.example.com"
			if !seen[want] {
				t.Fatalf("missing %s; a consumer cannot tell the two products apart", want)
			}
		}
	}
}

// ------------------------------------------------------------ retry backoff

func TestBackoffUsesTheInjectedSleepSeam(t *testing.T) {
	// Backoff used to call the package-level sleepCtx, bypassing the virtual
	// clock: this test would then block for ~600ms of wall time.
	clk := newClock()
	start := clk.now()
	startWall := time.Now()
	l := newLimiter(1000, clk.now, clk.sleep)
	attempts := 0
	resp, err := call(context.Background(), l, 3, func() (*int, error) {
		attempts++
		if attempts < 3 {
			return nil, tcerr.NewTencentCloudSDKError("RequestLimitExceeded", "slow down", "req-1")
		}
		v := 7
		return &v, nil
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp == nil || *resp != 7 || attempts != 3 {
		t.Fatalf("unexpected retry outcome: resp=%v attempts=%d", resp, attempts)
	}
	// 200ms + 400ms of base backoff, plus jitter and pacing.
	if elapsed := clk.now().Sub(start); elapsed < 600*time.Millisecond {
		t.Fatalf("virtual clock advanced only %s: backoff bypassed the injected sleep", elapsed)
	}
	if wall := time.Since(startWall); wall > 200*time.Millisecond {
		t.Fatalf("backoff slept for real (%s): it must use the injected sleep", wall)
	}
}
