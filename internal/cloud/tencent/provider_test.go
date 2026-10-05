package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	cdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	ssl "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205"
	sts "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sts/v20180813"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/core"
)

// ---------------------------------------------------------------- test clock

type clock struct {
	mu   sync.Mutex
	at   time.Time
	step time.Duration
}

func newClock() *clock {
	return &clock{at: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), step: time.Second}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 {
		d = c.step
	}
	c.at = c.at.Add(d)
	return nil
}

// testProvider wires fakes with a virtual clock so polls never really wait.
func testProvider(t *testing.T, s sslAPI, c cdnAPI, e teoAPI) *Provider {
	t.Helper()
	return testProviderWithSTS(t, s, c, e, nil)
}

func testProviderWithSTS(t *testing.T, s sslAPI, c cdnAPI, e teoAPI, stsClient stsAPI) *Provider {
	t.Helper()
	opts := Options{
		Region:       "ap-guangzhou",
		PollInterval: time.Second,
		PollTimeout:  30 * time.Second,
		MaxAttempts:  3,
	}.withDefaults()

	clk := newClock()
	p := newWithClients("demo", opts, s, c, e, stsClient)
	p.now = clk.now
	p.sleep = clk.sleep
	p.limiters = map[string]*limiter{
		"ssl": newLimiter(1000, clk.now, clk.sleep),
		"cdn": newLimiter(1000, clk.now, clk.sleep),
		"teo": newLimiter(1000, clk.now, clk.sleep),
	}
	return p
}

// ---------------------------------------------------------------- fake SSL

type fakeSSL struct {
	certs []*ssl.Certificates

	bindCreateResponse *ssl.CreateCertificateBindResourceSyncTaskResponse
	bindPollResponses  []*ssl.DescribeCertificateBindResourceTaskResultResponse
	bindPollIdx        int

	deployResponse *ssl.DeployCertificateInstanceResponse
	deployRequests []*ssl.DeployCertificateInstanceRequest

	deployRecords []*ssl.DeployRecordInfo

	deleteResponse *ssl.DeleteCertificatesResponse
	deleteRequests []*ssl.DeleteCertificatesRequest

	deletePollResponses []*ssl.DescribeDeleteCertificatesTaskResultResponse
	deletePollIdx       int

	cdnInstances []*ssl.CdnInstanceDetail
	teoInstances []*ssl.TeoInstanceDetail

	failOnce map[string]error
}

func (f *fakeSSL) maybeFail(method string) error {
	if f.failOnce == nil {
		return nil
	}
	if err, ok := f.failOnce[method]; ok {
		delete(f.failOnce, method)
		return err
	}
	return nil
}

func (f *fakeSSL) DescribeCertificates(*ssl.DescribeCertificatesRequest) (*ssl.DescribeCertificatesResponse, error) {
	if err := f.maybeFail("DescribeCertificates"); err != nil {
		return nil, err
	}
	return &ssl.DescribeCertificatesResponse{Response: &ssl.DescribeCertificatesResponseParams{
		TotalCount:   common.Uint64Ptr(uint64(len(f.certs))),
		Certificates: f.certs,
	}}, nil
}

func (f *fakeSSL) CreateCertificateBindResourceSyncTask(*ssl.CreateCertificateBindResourceSyncTaskRequest) (*ssl.CreateCertificateBindResourceSyncTaskResponse, error) {
	if err := f.maybeFail("CreateCertificateBindResourceSyncTask"); err != nil {
		return nil, err
	}
	return f.bindCreateResponse, nil
}

func (f *fakeSSL) DescribeCertificateBindResourceTaskResult(*ssl.DescribeCertificateBindResourceTaskResultRequest) (*ssl.DescribeCertificateBindResourceTaskResultResponse, error) {
	if len(f.bindPollResponses) == 0 {
		return &ssl.DescribeCertificateBindResourceTaskResultResponse{Response: &ssl.DescribeCertificateBindResourceTaskResultResponseParams{}}, nil
	}
	idx := f.bindPollIdx
	if idx >= len(f.bindPollResponses) {
		idx = len(f.bindPollResponses) - 1
	}
	f.bindPollIdx++
	return f.bindPollResponses[idx], nil
}

func (f *fakeSSL) DescribeHostCdnInstanceList(*ssl.DescribeHostCdnInstanceListRequest) (*ssl.DescribeHostCdnInstanceListResponse, error) {
	return &ssl.DescribeHostCdnInstanceListResponse{Response: &ssl.DescribeHostCdnInstanceListResponseParams{
		TotalCount:   common.Uint64Ptr(uint64(len(f.cdnInstances))),
		InstanceList: f.cdnInstances,
	}}, nil
}

func (f *fakeSSL) DescribeHostTeoInstanceList(*ssl.DescribeHostTeoInstanceListRequest) (*ssl.DescribeHostTeoInstanceListResponse, error) {
	return &ssl.DescribeHostTeoInstanceListResponse{Response: &ssl.DescribeHostTeoInstanceListResponseParams{
		TotalCount:   common.Int64Ptr(int64(len(f.teoInstances))),
		InstanceList: f.teoInstances,
	}}, nil
}

func (f *fakeSSL) DeployCertificateInstance(req *ssl.DeployCertificateInstanceRequest) (*ssl.DeployCertificateInstanceResponse, error) {
	if err := f.maybeFail("DeployCertificateInstance"); err != nil {
		return nil, err
	}
	f.deployRequests = append(f.deployRequests, req)
	if f.deployResponse != nil {
		return f.deployResponse, nil
	}
	return &ssl.DeployCertificateInstanceResponse{Response: &ssl.DeployCertificateInstanceResponseParams{
		DeployRecordId: common.Uint64Ptr(42),
		DeployStatus:   common.Int64Ptr(1),
	}}, nil
}

func (f *fakeSSL) DescribeHostDeployRecord(*ssl.DescribeHostDeployRecordRequest) (*ssl.DescribeHostDeployRecordResponse, error) {
	return &ssl.DescribeHostDeployRecordResponse{Response: &ssl.DescribeHostDeployRecordResponseParams{
		DeployRecordList: f.deployRecords,
	}}, nil
}

func (f *fakeSSL) DeleteCertificates(req *ssl.DeleteCertificatesRequest) (*ssl.DeleteCertificatesResponse, error) {
	f.deleteRequests = append(f.deleteRequests, req)
	if f.deleteResponse != nil {
		return f.deleteResponse, nil
	}
	return &ssl.DeleteCertificatesResponse{Response: &ssl.DeleteCertificatesResponseParams{}}, nil
}

func (f *fakeSSL) DescribeDeleteCertificatesTaskResult(*ssl.DescribeDeleteCertificatesTaskResultRequest) (*ssl.DescribeDeleteCertificatesTaskResultResponse, error) {
	if len(f.deletePollResponses) == 0 {
		return &ssl.DescribeDeleteCertificatesTaskResultResponse{Response: &ssl.DescribeDeleteCertificatesTaskResultResponseParams{}}, nil
	}
	idx := f.deletePollIdx
	if idx >= len(f.deletePollResponses) {
		idx = len(f.deletePollResponses) - 1
	}
	f.deletePollIdx++
	return f.deletePollResponses[idx], nil
}

// ---------------------------------------------------------------- fake STS

type fakeSTS struct {
	response *sts.GetCallerIdentityResponse
	err      error
}

func (f *fakeSTS) GetCallerIdentity(*sts.GetCallerIdentityRequest) (*sts.GetCallerIdentityResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

// ---------------------------------------------------------------- fake CDN

type fakeCDN struct {
	domains    []*cdn.DetailDomain
	modifyReqs []*cdn.ModifyDomainConfigRequest
}

func (f *fakeCDN) DescribeDomainsConfig(*cdn.DescribeDomainsConfigRequest) (*cdn.DescribeDomainsConfigResponse, error) {
	return &cdn.DescribeDomainsConfigResponse{Response: &cdn.DescribeDomainsConfigResponseParams{
		TotalNumber: common.Int64Ptr(int64(len(f.domains))),
		Domains:     f.domains,
	}}, nil
}

func (f *fakeCDN) ModifyDomainConfig(req *cdn.ModifyDomainConfigRequest) (*cdn.ModifyDomainConfigResponse, error) {
	f.modifyReqs = append(f.modifyReqs, req)
	var payload struct {
		Update string `json:"update"`
	}
	if err := json.Unmarshal([]byte(str(req.Value)), &payload); err != nil {
		return nil, err
	}
	for _, d := range f.domains {
		if str(d.Domain) != str(req.Domain) {
			continue
		}
		if d.Https == nil {
			d.Https = &cdn.Https{}
		}
		if req.Route != nil && *req.Route == cdnCertRoute {
			d.Https.CertInfo = &cdn.ServerCert{CertId: common.StringPtr(payload.Update)}
			d.Https.SslStatus = common.StringPtr("deployed")
		}
	}
	return &cdn.ModifyDomainConfigResponse{Response: &cdn.ModifyDomainConfigResponseParams{}}, nil
}

// ---------------------------------------------------------------- fake TEO

type fakeTEO struct {
	zones      []*teo.Zone
	domains    map[string][]*teo.AccelerationDomain
	modifyReqs []*teo.ModifyHostsCertificateRequest
	failZones  error
	// failDomains makes DescribeAccelerationDomains fail for specific sites.
	failDomains map[string]error
}

func (f *fakeTEO) DescribeZones(*teo.DescribeZonesRequest) (*teo.DescribeZonesResponse, error) {
	if f.failZones != nil {
		return nil, f.failZones
	}
	return &teo.DescribeZonesResponse{Response: &teo.DescribeZonesResponseParams{
		TotalCount: common.Int64Ptr(int64(len(f.zones))),
		Zones:      f.zones,
	}}, nil
}

func (f *fakeTEO) DescribeAccelerationDomains(req *teo.DescribeAccelerationDomainsRequest) (*teo.DescribeAccelerationDomainsResponse, error) {
	if err, ok := f.failDomains[str(req.ZoneId)]; ok {
		return nil, err
	}
	all := f.domains[str(req.ZoneId)]
	want := ""
	if len(req.Filters) > 0 && req.Filters[0] != nil && len(req.Filters[0].Values) > 0 {
		want = str(req.Filters[0].Values[0])
	}
	var out []*teo.AccelerationDomain
	for _, d := range all {
		if want == "" || strings.EqualFold(str(d.DomainName), want) {
			out = append(out, d)
		}
	}
	return &teo.DescribeAccelerationDomainsResponse{Response: &teo.DescribeAccelerationDomainsResponseParams{
		TotalCount:          common.Int64Ptr(int64(len(out))),
		AccelerationDomains: out,
	}}, nil
}

func (f *fakeTEO) ModifyHostsCertificate(req *teo.ModifyHostsCertificateRequest) (*teo.ModifyHostsCertificateResponse, error) {
	f.modifyReqs = append(f.modifyReqs, req)
	certID := ""
	if len(req.ServerCertInfo) > 0 && req.ServerCertInfo[0] != nil {
		certID = str(req.ServerCertInfo[0].CertId)
	}
	for _, host := range req.Hosts {
		for _, d := range f.domains[str(req.ZoneId)] {
			if !strings.EqualFold(str(d.DomainName), str(host)) {
				continue
			}
			d.Certificate = &teo.AccelerationDomainCertificate{
				Mode: req.Mode,
				List: []*teo.CertificateInfo{{
					CertId: common.StringPtr(certID),
					Status: common.StringPtr("deployed"),
				}},
			}
		}
	}
	return &teo.ModifyHostsCertificateResponse{Response: &teo.ModifyHostsCertificateResponseParams{}}, nil
}

// ------------------------------------------------------------------- helpers

func sslCert(id, alias, domain string, sans []string, from, pkg, end string, hosting *int64) *ssl.Certificates {
	c := &ssl.Certificates{
		CertificateId: common.StringPtr(id),
		Alias:         common.StringPtr(alias),
		Domain:        common.StringPtr(domain),
		From:          common.StringPtr(from),
		Status:        common.Uint64Ptr(1),
		Deployable:    common.BoolPtr(true),
		CertBeginTime: common.StringPtr("2026-01-01 00:00:00"),
		CertEndTime:   common.StringPtr(end),
		InsertTime:    common.StringPtr("2026-01-01 00:00:00"),
		HostingStatus: hosting,
	}
	if pkg != "" {
		c.PackageType = common.StringPtr(pkg)
	}
	c.SubjectAltName = common.StringPtrs(sans)
	return c
}

func ptr(v int64) *int64 { return &v }

// ------------------------------------------------------------------- mapping

func TestToCertificateUnionsSANsAndParsesShanghaiTime(t *testing.T) {
	got := toCertificate(sslCert("c-1", "alias", "example.com",
		[]string{"example.com", "www.example.com"}, "upload", "", "2027-09-10 07:59:59", ptr(-1)))

	if got.ID != "c-1" || got.From != "upload" {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if len(got.SANs) != 2 {
		t.Fatalf("expected the SAN union, got %v", got.SANs)
	}
	// 07:59:59 GMT+8 == 23:59:59 UTC on the previous day.
	want := time.Date(2027, 9, 9, 23, 59, 59, 0, time.UTC)
	if !got.NotAfter.Equal(want) {
		t.Fatalf("NotAfter = %s, want %s", got.NotAfter, want)
	}
	if got.HostingStatus == nil || *got.HostingStatus != -1 {
		t.Fatalf("HostingStatus pointer must be preserved, got %v", got.HostingStatus)
	}
	if got.IsHosted() {
		t.Fatal("-1 means not hosted")
	}
}

func TestToCertificateKeepsMissingHostingStatusUnknown(t *testing.T) {
	got := toCertificate(sslCert("c-1", "a", "example.com", nil, "upload", "", "2027-01-01 00:00:00", nil))
	if got.HostingStatus != nil {
		t.Fatalf("absent field must stay nil, got %v", *got.HostingStatus)
	}
	if got.IsHosted() {
		t.Fatal("an absent HostingStatus must not read as hosted")
	}
}

func TestToTEOResourceTreatsFreeCertificateAsForeign(t *testing.T) {
	free := &teo.AccelerationDomain{
		ZoneId:     common.StringPtr("zone-1"),
		DomainName: common.StringPtr("cdn.example.com"),
		Certificate: &teo.AccelerationDomainCertificate{
			Mode: common.StringPtr("eofreecert"),
			List: []*teo.CertificateInfo{{CertId: common.StringPtr("teo-285inl1otbtd"), Status: common.StringPtr("deployed")}},
		},
	}
	res := toTEOResource(free)
	if !res.BoundForeign {
		t.Fatal("mode eofreecert must be flagged as a foreign certificate")
	}
	if res.BoundCert != "teo-285inl1otbtd" || res.BoundMode != "eofreecert" {
		t.Fatalf("unexpected mapping: %+v", res)
	}

	managed := &teo.AccelerationDomain{
		ZoneId:     common.StringPtr("zone-1"),
		DomainName: common.StringPtr("api.example.com"),
		Certificate: &teo.AccelerationDomainCertificate{
			Mode: common.StringPtr("sslcert"),
			List: []*teo.CertificateInfo{{CertId: common.StringPtr("c-1"), Status: common.StringPtr("deployed")}},
		},
	}
	res = toTEOResource(managed)
	if res.BoundForeign {
		t.Fatal("mode sslcert is a pool certificate, not foreign")
	}
	if res.InstanceKey != "api.example.com" {
		t.Fatalf("teo instance key must be the bare domain, got %q", res.InstanceKey)
	}
}

func TestToCDNResourceKeepsCertificateStateApartFromDomainStatus(t *testing.T) {
	d := &cdn.DetailDomain{
		Domain: common.StringPtr("www.example.com"),
		Status: common.StringPtr("online"),
		Https: &cdn.Https{
			SslStatus: common.StringPtr("deploying"),
			CertInfo:  &cdn.ServerCert{CertId: common.StringPtr("c-9"), ExpireTime: common.StringPtr("2027-01-01 00:00:00")},
		},
	}
	res := toCDNResource(d)
	if res.Status != "online" || res.CertState != "deploying" {
		t.Fatalf("domain status and certificate state must not be mixed: %+v", res)
	}
	if res.BoundCert != "c-9" || res.BoundNotAfter.IsZero() {
		t.Fatalf("certificate and expiry should be captured: %+v", res)
	}
}

// ----------------------------------------------------------------- bindings

func TestFetchBindingsAggregatesAndFlagsIncomplete(t *testing.T) {
	f := &fakeSSL{
		bindCreateResponse: &ssl.CreateCertificateBindResourceSyncTaskResponse{
			Response: &ssl.CreateCertificateBindResourceSyncTaskResponseParams{
				CertTaskIds: []*ssl.CertTaskId{
					{CertId: common.StringPtr("c-1"), TaskId: common.StringPtr("t-1")},
					{CertId: common.StringPtr("c-2"), TaskId: common.StringPtr("t-2")},
				},
			},
		},
		bindPollResponses: []*ssl.DescribeCertificateBindResourceTaskResultResponse{{
			Response: &ssl.DescribeCertificateBindResourceTaskResultResponseParams{
				SyncTaskBindResourceResult: []*ssl.SyncTaskBindResourceResult{
					{
						TaskId: common.StringPtr("t-1"),
						Status: common.Uint64Ptr(1),
						BindResourceResult: []*ssl.BindResourceResult{{
							ResourceType: common.StringPtr("cdn"),
							BindResourceRegionResult: []*ssl.BindResourceRegionResult{
								{Region: common.StringPtr(""), TotalCount: common.Uint64Ptr(0)},
							},
						}},
					},
					{
						TaskId: common.StringPtr("t-2"),
						Status: common.Uint64Ptr(1),
						BindResourceResult: []*ssl.BindResourceResult{{
							ResourceType: common.StringPtr("teo"),
							BindResourceRegionResult: []*ssl.BindResourceRegionResult{
								{Region: common.StringPtr(""), TotalCount: common.Uint64Ptr(2)},
								{Region: common.StringPtr(""), TotalCount: common.Uint64Ptr(0), Error: common.StringPtr("timeout")},
							},
						}},
					},
				},
			},
		}},
	}
	p := testProvider(t, f, &fakeCDN{}, &fakeTEO{})

	got, err := p.ListBindings(context.Background(), "demo", []string{"c-1", "c-2"})
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if got["c-1"].TotalCount != 0 || !got["c-1"].Complete {
		t.Fatalf("c-1 should be a complete zero-binding result: %+v", got["c-1"])
	}
	if got["c-2"].TotalCount != 2 {
		t.Fatalf("c-2 totals must be summed: %+v", got["c-2"])
	}
	if got["c-2"].Complete {
		t.Fatal("a per-region error must make the evidence incomplete")
	}
}

func TestFetchBindingsPollsUntilQueryCompletes(t *testing.T) {
	f := &fakeSSL{
		bindCreateResponse: &ssl.CreateCertificateBindResourceSyncTaskResponse{
			Response: &ssl.CreateCertificateBindResourceSyncTaskResponseParams{
				CertTaskIds: []*ssl.CertTaskId{{CertId: common.StringPtr("c-1"), TaskId: common.StringPtr("t-1")}},
			},
		},
		bindPollResponses: []*ssl.DescribeCertificateBindResourceTaskResultResponse{
			{Response: &ssl.DescribeCertificateBindResourceTaskResultResponseParams{
				SyncTaskBindResourceResult: []*ssl.SyncTaskBindResourceResult{
					{TaskId: common.StringPtr("t-1"), Status: common.Uint64Ptr(0)},
				},
			}},
			{Response: &ssl.DescribeCertificateBindResourceTaskResultResponseParams{
				SyncTaskBindResourceResult: []*ssl.SyncTaskBindResourceResult{
					{TaskId: common.StringPtr("t-1"), Status: common.Uint64Ptr(1)},
				},
			}},
		},
	}
	p := testProvider(t, f, &fakeCDN{}, &fakeTEO{})

	got, err := p.ListBindings(context.Background(), "demo", []string{"c-1"})
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if got["c-1"].Status != 1 || !got["c-1"].Complete {
		t.Fatalf("polling must continue until the query reports success: %+v", got["c-1"])
	}
	if f.bindPollIdx < 2 {
		t.Fatalf("expected two polls, got %d", f.bindPollIdx)
	}
}

// -------------------------------------------------------------------- apply

func TestApplyCDNUsesRouteUpdateAndVerifies(t *testing.T) {
	cdnFake := &fakeCDN{domains: []*cdn.DetailDomain{{
		Domain: common.StringPtr("www.example.com"),
		Status: common.StringPtr("online"),
		Https: &cdn.Https{
			CertInfo:  &cdn.ServerCert{CertId: common.StringPtr("c-old")},
			SslStatus: common.StringPtr("deployed"),
		},
	}}}
	p := testProvider(t, &fakeSSL{}, cdnFake, &fakeTEO{})

	plan := core.Plan{Changes: []core.Change{{
		Product: core.ProductCDN, Domain: "www.example.com", ResourceID: "www.example.com",
		Kind: core.KindApply, Strategy: core.StrategyProduct,
		To: &core.CertRef{ID: "c-new"},
	}}}
	results, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(results) != 1 || !results[0].OK || !results[0].Verified {
		t.Fatalf("expected one verified success, got %+v", results)
	}
	if len(cdnFake.modifyReqs) != 1 {
		t.Fatalf("expected exactly one ModifyDomainConfig call, got %d", len(cdnFake.modifyReqs))
	}
	req := cdnFake.modifyReqs[0]
	if str(req.Route) != cdnCertRoute {
		t.Fatalf("route = %q, want %q (dotted leaf update)", str(req.Route), cdnCertRoute)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(str(req.Value)), &payload); err != nil {
		t.Fatalf("value must be a JSON object: %v", err)
	}
	if payload["update"] != "c-new" {
		t.Fatalf("value = %v, want update=c-new", payload)
	}
}

func TestApplyTEOBatchesHostsAndLeavesOriginCertificatesUntouched(t *testing.T) {
	teoFake := &fakeTEO{
		zones: []*teo.Zone{{ZoneId: common.StringPtr("zone-1")}},
		domains: map[string][]*teo.AccelerationDomain{
			"zone-1": {
				{ZoneId: common.StringPtr("zone-1"), DomainName: common.StringPtr("a.example.com"), DomainStatus: common.StringPtr("online")},
				{ZoneId: common.StringPtr("zone-1"), DomainName: common.StringPtr("b.example.com"), DomainStatus: common.StringPtr("online")},
			},
		},
	}
	p := testProvider(t, &fakeSSL{}, &fakeCDN{}, teoFake)

	plan := core.Plan{Changes: []core.Change{
		{Product: core.ProductTEO, ZoneID: "zone-1", Domain: "a.example.com", Kind: core.KindApply, Strategy: core.StrategyProduct, To: &core.CertRef{ID: "c-new"}},
		{Product: core.ProductTEO, ZoneID: "zone-1", Domain: "b.example.com", Kind: core.KindApply, Strategy: core.StrategyProduct, To: &core.CertRef{ID: "c-new"}},
	}}
	results, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two results, got %d", len(results))
	}
	for _, r := range results {
		if !r.OK || !r.Verified {
			t.Fatalf("expected verified success, got %+v", r)
		}
	}
	if len(teoFake.modifyReqs) != 1 {
		t.Fatalf("hosts sharing one certificate must be batched, got %d calls", len(teoFake.modifyReqs))
	}
	req := teoFake.modifyReqs[0]
	if str(req.Mode) != teoModeSSLCert {
		t.Fatalf("mode = %q, want %q", str(req.Mode), teoModeSSLCert)
	}
	if len(req.Hosts) != 2 {
		t.Fatalf("expected both hosts in one call, got %v", strs(req.Hosts))
	}
	// Decision B7: origin (upstream) and mTLS client certificates are untouched.
	if req.UpstreamCertInfo != nil {
		t.Fatal("UpstreamCertInfo must not be set: origin certificates are out of scope")
	}
	if req.ClientCertInfo != nil {
		t.Fatal("ClientCertInfo must not be set: mTLS is out of scope")
	}
	if len(req.ServerCertInfo) != 1 || str(req.ServerCertInfo[0].CertId) != "c-new" {
		t.Fatalf("ServerCertInfo = %+v", req.ServerCertInfo)
	}
}

func TestApplyUnifiedDeploySkipsResourcesWithoutInstanceKey(t *testing.T) {
	f := &fakeSSL{deployRecords: []*ssl.DeployRecordInfo{
		{Id: common.Uint64Ptr(42), Status: common.Uint64Ptr(1)},
	}}
	cdnFake := &fakeCDN{domains: []*cdn.DetailDomain{{
		Domain: common.StringPtr("www.example.com"),
		Https:  &cdn.Https{CertInfo: &cdn.ServerCert{CertId: common.StringPtr("c-new")}, SslStatus: common.StringPtr("deployed")},
	}}}
	p := testProvider(t, f, cdnFake, &fakeTEO{})

	plan := core.Plan{Changes: []core.Change{
		{Product: core.ProductTEO, ZoneID: "zone-1", Domain: "api.example.com", Kind: core.KindApply, Strategy: core.StrategyDeploy, InstanceKey: "api.example.com", To: &core.CertRef{ID: "c-new"}},
	}}
	// teo verification fake has no domains, so the deploy is verified false but
	// the request itself must still have been issued with the instance key.
	results, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(f.deployRequests) != 1 {
		t.Fatalf("expected one DeployCertificateInstance call, got %d", len(f.deployRequests))
	}
	req := f.deployRequests[0]
	if str(req.ResourceType) != "teo" {
		t.Fatalf("resource type = %q, want teo", str(req.ResourceType))
	}
	if len(req.InstanceIdList) != 1 || str(req.InstanceIdList[0]) != "api.example.com" {
		t.Fatalf("instance list = %v", strs(req.InstanceIdList))
	}
	if len(results) == 0 || results[0].MessageKey != MsgVerifyFailed {
		t.Fatalf("an unconfirmed write must be reported as unverified: %+v", results)
	}
}

func TestApplyReportsResourcesWithoutDeployableInstanceID(t *testing.T) {
	p := testProvider(t, &fakeSSL{}, &fakeCDN{}, &fakeTEO{})
	plan := core.Plan{Changes: []core.Change{{
		Product: core.ProductCDN, Domain: "www.example.com", Kind: core.KindApply,
		Strategy: core.StrategyDeploy, To: &core.CertRef{ID: "c-new"},
	}}}
	results, err := p.Apply(context.Background(), "demo", plan, cloud.ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(results) != 1 || results[0].OK {
		t.Fatalf("a resource without an instance id must fail loudly: %+v", results)
	}
	if results[0].MessageKey != MsgDeployFailed {
		t.Fatalf("message key = %q, want %q", results[0].MessageKey, MsgDeployFailed)
	}
}

// ------------------------------------------------------------------- delete

func TestDeleteAlwaysChecksBindingsAndClassifiesStatus(t *testing.T) {
	f := &fakeSSL{
		deleteResponse: &ssl.DeleteCertificatesResponse{Response: &ssl.DeleteCertificatesResponseParams{
			CertTaskIds: []*ssl.CertTaskId{
				{CertId: common.StringPtr("c-ok"), TaskId: common.StringPtr("t-1")},
				{CertId: common.StringPtr("c-bound"), TaskId: common.StringPtr("t-2")},
				{CertId: common.StringPtr("c-timeout"), TaskId: common.StringPtr("t-3")},
			},
		}},
		deletePollResponses: []*ssl.DescribeDeleteCertificatesTaskResultResponse{{
			Response: &ssl.DescribeDeleteCertificatesTaskResultResponseParams{
				DeleteTaskResult: []*ssl.DeleteTaskResult{
					{TaskId: common.StringPtr("t-1"), CertId: common.StringPtr("c-ok"), Status: common.Uint64Ptr(1)},
					{TaskId: common.StringPtr("t-2"), CertId: common.StringPtr("c-bound"), Status: common.Uint64Ptr(4), Error: common.StringPtr("still bound")},
					{TaskId: common.StringPtr("t-3"), CertId: common.StringPtr("c-timeout"), Status: common.Uint64Ptr(5), Error: common.StringPtr("query timeout")},
				},
			},
		}},
	}
	p := testProvider(t, f, &fakeCDN{}, &fakeTEO{})

	// The caller asks for the unsafe setting on purpose: the provider must
	// override it (decision T5).
	results, err := p.DeleteCertificates(context.Background(), "demo", []string{"c-ok", "c-bound", "c-timeout"}, cloud.DeleteOptions{CheckBindings: false})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.deleteRequests) != 1 {
		t.Fatalf("expected one batch call, got %d", len(f.deleteRequests))
	}
	if req := f.deleteRequests[0]; req.IsSync == nil || !*req.IsSync {
		t.Fatal("IsSync must always be true: the API default does not check associations")
	}
	byID := map[string]core.Result{}
	for _, r := range results {
		byID[r.Change.To.ID] = r
	}
	if !byID["c-ok"].OK {
		t.Fatalf("status 1 must succeed: %+v", byID["c-ok"])
	}
	if byID["c-bound"].OK || byID["c-bound"].MessageKey != MsgDeleteKeptBound {
		t.Fatalf("status 4 must keep the certificate: %+v", byID["c-bound"])
	}
	if byID["c-timeout"].OK || byID["c-timeout"].MessageKey != MsgDeleteKeptQuery {
		t.Fatalf("status 5 must keep the certificate: %+v", byID["c-timeout"])
	}
}

// -------------------------------------------------------------------- retry

func TestRetriesOnRateLimitThenSucceeds(t *testing.T) {
	f := &fakeSSL{
		certs:    []*ssl.Certificates{sslCert("c-1", "a", "example.com", nil, "upload", "", "2027-01-01 00:00:00", ptr(-1))},
		failOnce: map[string]error{"DescribeCertificates": tcerr.NewTencentCloudSDKError("LimitExceeded.RateLimitExceeded", "too fast", "req-1")},
	}
	p := testProvider(t, f, &fakeCDN{}, &fakeTEO{})

	certs, err := p.ListCertificates(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("a rate limit must be retried, got %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("expected one certificate after the retry, got %d", len(certs))
	}
}

func TestDoesNotRetryPermanentErrors(t *testing.T) {
	perm := tcerr.NewTencentCloudSDKError("FailedOperation.SomethingWeird", "not transient", "req-2")
	f := &fakeSSL{failOnce: map[string]error{"DescribeCertificates": perm}}
	p := testProvider(t, f, &fakeCDN{}, &fakeTEO{})

	_, err := p.ListCertificates(context.Background(), "demo", cloud.ScanScope{})
	if err == nil {
		t.Fatal("an auth failure must surface immediately")
	}
	if !errors.Is(err, perm) {
		t.Fatalf("the original SDK error must be preserved for errors.As, got %v", err)
	}
	code, key, requestID := ErrorInfo(err)
	if code != "FailedOperation.SomethingWeird" {
		t.Fatalf("code = %q", code)
	}
	if key != MsgUnknown {
		t.Fatalf("unmapped codes fall back to the generic hint, got %q", key)
	}
	if requestID != "req-2" {
		t.Fatalf("request id = %q", requestID)
	}
}

func TestAuthFailuresAreNotRetried(t *testing.T) {
	auth := tcerr.NewTencentCloudSDKError("AuthFailure.SecretIdNotFound", "no such secret id", "req-3")
	f := &fakeSSL{failOnce: map[string]error{"DescribeCertificates": auth}}
	p := testProvider(t, f, &fakeCDN{}, &fakeTEO{})

	_, err := p.ListCertificates(context.Background(), "demo", cloud.ScanScope{})
	if err == nil {
		t.Fatal("an auth failure must surface immediately")
	}
	if key := messageKeyOf(err); key != MsgAuthFailed {
		t.Fatalf("message key = %q, want %q", key, MsgAuthFailed)
	}
}

func TestErrorMessagesAreActionable(t *testing.T) {
	cases := map[string]string{
		"FailedOperation.RoleNotFoundAuthorization": MsgRoleMissing,
		"FailedOperation.CertificateMismatch":       MsgCertMismatch,
		"ResourceUnavailable.CdnHostNoIcp":          MsgNoICP,
		"InvalidParameter.RegionNotEmpty":           MsgRegionRequired,
		"LimitExceeded.RateLimitExceeded":           MsgRateLimited,
		"FailedOperation.CertificateNotFound":       MsgNotFound,
		"AuthFailure.SecretIdNotFound":              MsgAuthFailed,
		"UnauthorizedOperation.CamUnauthorized":     MsgNoPermission,
	}
	for code, want := range cases {
		err := tcerr.NewTencentCloudSDKError(code, "boom", "req")
		if got := MessageKey(err); got != want {
			t.Errorf("%s: got %q, want %q", code, got, want)
		}
	}
}

func TestAbortOnAuthorisationFailures(t *testing.T) {
	if !abortOn(tcerr.NewTencentCloudSDKError("FailedOperation.RoleNotFoundAuthorization", "x", "r")) {
		t.Fatal("a missing service role must abort the run")
	}
	if abortOn(tcerr.NewTencentCloudSDKError("ResourceUnavailable.CdnHostNoIcp", "x", "r")) {
		t.Fatal("a per-domain failure must not abort the whole run")
	}
}

// ---------------------------------------------------------------------- scan

func TestScanDegradesWhenOneProductIsUnavailable(t *testing.T) {
	sslFake := &fakeSSL{
		certs: []*ssl.Certificates{sslCert("c-1", "a", "www.example.com", nil, "upload", "", "2027-01-01 00:00:00", ptr(-1))},
	}
	cdnFake := &fakeCDN{domains: []*cdn.DetailDomain{{
		Domain: common.StringPtr("www.example.com"),
		Status: common.StringPtr("online"),
		Https:  &cdn.Https{CertInfo: &cdn.ServerCert{CertId: common.StringPtr("c-1")}, SslStatus: common.StringPtr("deployed")},
	}}}
	teoFake := &fakeTEO{failZones: tcerr.NewTencentCloudSDKError("UnauthorizedOperation", "操作无权限。", "req-9")}
	p := testProvider(t, sslFake, cdnFake, teoFake)

	res, err := p.Scan(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("a single unavailable product must not fail the scan: %v", err)
	}
	if len(res.Certificates) != 1 {
		t.Fatalf("certificates should still be collected, got %d", len(res.Certificates))
	}
	if len(res.Resources) != 1 {
		t.Fatalf("the healthy product should still be collected, got %d", len(res.Resources))
	}
	if res.Complete() {
		t.Fatal("warnings must make the scan incomplete")
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected one warning, got %+v", res.Warnings)
	}
	w := res.Warnings[0]
	if w.Product != "teo" || w.Code != "UnauthorizedOperation" {
		t.Fatalf("the warning must name the product and code: %+v", w)
	}
	if !strings.Contains(w.Action, "teo:DescribeZones") {
		t.Fatalf("the warning must name the failing action, got %q", w.Action)
	}
	if !strings.Contains(w.Err.Error(), "account demo") {
		t.Fatalf("the warning must name the account, got %q", w.Err.Error())
	}
}

func TestScanFailsWhenCertificatePoolIsUnavailable(t *testing.T) {
	auth := tcerr.NewTencentCloudSDKError("UnauthorizedOperation", "操作无权限。", "req-10")
	sslFake := &fakeSSL{failOnce: map[string]error{"DescribeCertificates": auth}}
	p := testProvider(t, sslFake, &fakeCDN{}, &fakeTEO{})

	if _, err := p.Scan(context.Background(), "demo", cloud.ScanScope{}); err == nil {
		t.Fatal("without the certificate pool nothing can be matched, so this must fail")
	} else if got := ActionOf(err); got != "ssl:DescribeCertificates" {
		t.Fatalf("action = %q, want ssl:DescribeCertificates", got)
	}
}

func TestPreflightReportsEachProductSeparately(t *testing.T) {
	teoFake := &fakeTEO{failZones: tcerr.NewTencentCloudSDKError("UnauthorizedOperation", "操作无权限。", "req-11")}
	stsFake := &fakeSTS{response: &sts.GetCallerIdentityResponse{Response: &sts.GetCallerIdentityResponseParams{
		AccountId: common.StringPtr("100012345678"),
		Type:      common.StringPtr("CAMUser"),
		UserId:    common.StringPtr("1234567"),
	}}}
	p := testProviderWithSTS(t, &fakeSSL{}, &fakeCDN{}, teoFake, stsFake)

	checks, err := p.Preflight(context.Background(), "main")
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	status := map[string]core.CheckStatus{}
	for _, c := range checks {
		status[c.Key] = c.Status
	}
	if status["cloud.check.ssl"] != core.CheckOK || status["cloud.check.cdn"] != core.CheckOK {
		t.Fatalf("healthy products must pass: %+v", status)
	}
	if status["cloud.check.teo"] != core.CheckFail {
		t.Fatalf("the unavailable product must fail on its own line: %+v", status)
	}
	if status["cloud.check.service_role"] != core.CheckWarn {
		t.Fatalf("the service role cannot be probed read-only: %+v", status)
	}
	if status["cloud.check.identity"] != core.CheckOK {
		t.Fatalf("the credential identity should be reported: %+v", status)
	}
	for _, c := range checks {
		if c.Key == "cloud.check.identity" {
			if !strings.Contains(c.Detail, "100012345678") || !strings.Contains(c.Detail, "CAMUser") {
				t.Fatalf("identity detail must name the account and type, got %q", c.Detail)
			}
		}
	}
}

func TestPreflightReportsIdentityFailure(t *testing.T) {
	stsFake := &fakeSTS{err: tcerr.NewTencentCloudSDKError("AuthFailure.SecretIdNotFound", "no such id", "req-12")}
	p := testProviderWithSTS(t, &fakeSSL{}, &fakeCDN{}, &fakeTEO{}, stsFake)

	checks, err := p.Preflight(context.Background(), "main")
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	for _, c := range checks {
		if c.Key != "cloud.check.identity" {
			continue
		}
		if c.Status != core.CheckFail || c.MessageKey != MsgAuthFailed {
			t.Fatalf("identity failure must be reported with its hint: %+v", c)
		}
		return
	}
	t.Fatal("expected an identity check")
}

// ------------------------------------------------------------------ progress

func TestScanEmitsProgressEvents(t *testing.T) {
	sslFake := &fakeSSL{
		certs: []*ssl.Certificates{sslCert("c-1", "a", "www.example.com", nil, "upload", "", "2027-01-01 00:00:00", ptr(-1))},
		bindCreateResponse: &ssl.CreateCertificateBindResourceSyncTaskResponse{
			Response: &ssl.CreateCertificateBindResourceSyncTaskResponseParams{
				CertTaskIds: []*ssl.CertTaskId{{CertId: common.StringPtr("c-1"), TaskId: common.StringPtr("t-1")}},
			},
		},
		bindPollResponses: []*ssl.DescribeCertificateBindResourceTaskResultResponse{{
			Response: &ssl.DescribeCertificateBindResourceTaskResultResponseParams{
				SyncTaskBindResourceResult: []*ssl.SyncTaskBindResourceResult{
					{TaskId: common.StringPtr("t-1"), Status: common.Uint64Ptr(1)},
				},
			},
		}},
	}
	cdnFake := &fakeCDN{domains: []*cdn.DetailDomain{{Domain: common.StringPtr("www.example.com"), Status: common.StringPtr("online")}}}
	teoFake := &fakeTEO{zones: []*teo.Zone{{ZoneId: common.StringPtr("zone-1"), ZoneName: common.StringPtr("example.com")}}}

	opts := Options{Region: "ap-guangzhou", PollInterval: time.Second, PollTimeout: 30 * time.Second, MaxAttempts: 3}.withDefaults()
	clk := newClock()
	var keys []string
	opts.Progress = func(ev cloud.Progress) { keys = append(keys, ev.Key) }
	p := newWithClients("demo", opts, sslFake, cdnFake, teoFake)
	p.now, p.sleep = clk.now, clk.sleep
	p.limiters = map[string]*limiter{
		"ssl": newLimiter(1000, clk.now, clk.sleep),
		"cdn": newLimiter(1000, clk.now, clk.sleep),
		"teo": newLimiter(1000, clk.now, clk.sleep),
	}

	if _, err := p.Scan(context.Background(), "demo", cloud.ScanScope{}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	joined := strings.Join(keys, ",")
	for _, want := range []string{
		"progress.stage.certificates",
		"progress.stage.certificates_done",
		"progress.stage.cdn",
		"progress.stage.teo",
		"progress.teo.zone_domains",
		"progress.stage.bindings",
		"progress.bindings.first_run",
		"progress.stage.bindings_done",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing progress event %q in %v", want, keys)
		}
	}
}

func TestUnfinishedAssociationQueryBecomesAWarning(t *testing.T) {
	sslFake := &fakeSSL{
		certs: []*ssl.Certificates{sslCert("c-1", "a", "www.example.com", nil, "upload", "", "2027-01-01 00:00:00", ptr(-1))},
		bindCreateResponse: &ssl.CreateCertificateBindResourceSyncTaskResponse{
			Response: &ssl.CreateCertificateBindResourceSyncTaskResponseParams{
				CertTaskIds: []*ssl.CertTaskId{{CertId: common.StringPtr("c-1"), TaskId: common.StringPtr("t-1")}},
			},
		},
		// The task never leaves status 0 (querying); the poll deadline expires.
		bindPollResponses: []*ssl.DescribeCertificateBindResourceTaskResultResponse{{
			Response: &ssl.DescribeCertificateBindResourceTaskResultResponseParams{
				SyncTaskBindResourceResult: []*ssl.SyncTaskBindResourceResult{
					{TaskId: common.StringPtr("t-1"), Status: common.Uint64Ptr(0)},
				},
			},
		}},
	}
	p := testProvider(t, sslFake, &fakeCDN{}, &fakeTEO{})

	res, err := p.Scan(context.Background(), "demo", cloud.ScanScope{CachedEvidence: true})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Complete() {
		t.Fatal("an unfinished association query must make the scan incomplete")
	}
	found := false
	for _, w := range res.Warnings {
		if w.MessageKey == MsgEvidenceIncomplete {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an incomplete-evidence warning, got %+v", res.Warnings)
	}
}

func TestOneBadSiteDoesNotKillTheEdgeOneScan(t *testing.T) {
	denied := tcerr.NewTencentCloudSDKError("UnauthorizedOperation", "操作无权限。", "req-20")
	teoFake := &fakeTEO{
		zones: []*teo.Zone{
			{ZoneId: common.StringPtr("zone-pages"), ZoneName: common.StringPtr("default-pages-zone"), Type: common.StringPtr("pages")},
			{ZoneId: common.StringPtr("zone-main"), ZoneName: common.StringPtr("example.com"), Type: common.StringPtr("full")},
		},
		domains: map[string][]*teo.AccelerationDomain{
			"zone-main": {{ZoneId: common.StringPtr("zone-main"), DomainName: common.StringPtr("cdn.example.com"), DomainStatus: common.StringPtr("online")}},
		},
		failDomains: map[string]error{"zone-pages": denied},
	}
	sslFake := &fakeSSL{certs: []*ssl.Certificates{sslCert("c-1", "a", "cdn.example.com", nil, "upload", "", "2027-01-01 00:00:00", ptr(-1))}}
	p := testProvider(t, sslFake, &fakeCDN{}, teoFake)

	res, err := p.Scan(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Resources) != 1 || res.Resources[0].Domain != "cdn.example.com" {
		t.Fatalf("the healthy site must still be enumerated, got %+v", res.Resources)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected one warning for the bad site, got %+v", res.Warnings)
	}
	w := res.Warnings[0]
	if w.Scope != "default-pages-zone" {
		t.Fatalf("the warning must name the site, got %+v", w)
	}
	// A Pages site is not certificate-deployable; say that instead of blaming
	// permissions.
	if w.Code != "ZoneNotApplicable" || w.MessageKey != MsgZoneNotApplicable {
		t.Fatalf("pages sites must be reported as not applicable, got %+v", w)
	}
	if !w.Informational {
		t.Fatal("a Pages site is structurally out of scope and must not block cleanup")
	}
	if res.Complete() == false {
		t.Fatal("an out-of-scope site must not make the scan incomplete")
	}
}

func TestRealProductFailureStaysBlocking(t *testing.T) {
	denied := tcerr.NewTencentCloudSDKError("UnauthorizedOperation", "操作无权限。", "req-22")
	teoFake := &fakeTEO{
		zones:       []*teo.Zone{{ZoneId: common.StringPtr("zone-1"), ZoneName: common.StringPtr("main"), Type: common.StringPtr("full")}},
		failDomains: map[string]error{"zone-1": denied},
	}
	p := testProvider(t, &fakeSSL{}, &fakeCDN{}, teoFake)

	res, err := p.Scan(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Complete() {
		t.Fatal("a denied full-type site must keep the evidence incomplete")
	}
	if len(res.BlockingWarnings()) != 1 {
		t.Fatalf("expected one blocking warning, got %+v", res.Warnings)
	}
}

func TestUnknownSiteTypeKeepsThePermissionCode(t *testing.T) {
	denied := tcerr.NewTencentCloudSDKError("UnauthorizedOperation", "操作无权限。", "req-21")
	teoFake := &fakeTEO{
		zones:       []*teo.Zone{{ZoneId: common.StringPtr("zone-1"), ZoneName: common.StringPtr("main"), Type: common.StringPtr("full")}},
		failDomains: map[string]error{"zone-1": denied},
	}
	p := testProvider(t, &fakeSSL{}, &fakeCDN{}, teoFake)

	res, err := p.Scan(context.Background(), "demo", cloud.ScanScope{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected one warning, got %+v", res.Warnings)
	}
	if got := res.Warnings[0].Code; got != "UnauthorizedOperation" {
		t.Fatalf("a full-type site failure keeps its real code, got %q", got)
	}
}
