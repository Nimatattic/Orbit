package tencent

import (
	"strconv"
	"strings"
	"time"

	"github.com/Crequency/orbit/internal/cloud"
	cdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	ssl "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205"
	sts "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sts/v20180813"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"
)

// Defaults chosen from the published API limits (docs/TENCENT_API_NOTES.md §1).
const (
	defaultRegion       = "ap-guangzhou"
	defaultAPITimeout   = 15 * time.Second
	defaultPollInterval = 5 * time.Second
	defaultPollTimeout  = 5 * time.Minute
	defaultMaxAttempts  = 4

	sslQPS = 10 // per API + endpoint region + sub-account
	cdnQPS = 20
	teoQPS = 20

	sslPageSize = 100 // DescribeCertificates allows up to 1000
	cdnPageSize = 100 // DescribeDomainsConfig hard limit
	teoPageSize = 100 // DescribeZones limit; acceleration domains allow 200
)

// Options configures one account's provider.
type Options struct {
	SecretID  string
	SecretKey string
	// Region is required to construct the SDK clients even though SSL needs no
	// region and neither deployment path uses it (decision T6).
	Region string
	// Endpoint overrides the API endpoint; used by tests and proxies.
	Endpoint     string
	APITimeout   time.Duration
	PollInterval time.Duration
	PollTimeout  time.Duration
	MaxAttempts  int
	// Progress receives scan events; nil disables reporting.
	Progress func(cloud.Progress)
}

func (o Options) withDefaults() Options {
	if o.Region == "" {
		o.Region = defaultRegion
	}
	if o.APITimeout <= 0 {
		o.APITimeout = defaultAPITimeout
	}
	if o.PollInterval <= 0 {
		o.PollInterval = defaultPollInterval
	}
	if o.PollTimeout <= 0 {
		o.PollTimeout = defaultPollTimeout
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = defaultMaxAttempts
	}
	return o
}

// sslAPI is the subset of the SSL client Orbit calls. Keeping it an interface
// lets tests replay recorded responses without network access.
type sslAPI interface {
	DescribeCertificates(*ssl.DescribeCertificatesRequest) (*ssl.DescribeCertificatesResponse, error)
	CreateCertificateBindResourceSyncTask(*ssl.CreateCertificateBindResourceSyncTaskRequest) (*ssl.CreateCertificateBindResourceSyncTaskResponse, error)
	DescribeCertificateBindResourceTaskResult(*ssl.DescribeCertificateBindResourceTaskResultRequest) (*ssl.DescribeCertificateBindResourceTaskResultResponse, error)
	DescribeHostCdnInstanceList(*ssl.DescribeHostCdnInstanceListRequest) (*ssl.DescribeHostCdnInstanceListResponse, error)
	DescribeHostTeoInstanceList(*ssl.DescribeHostTeoInstanceListRequest) (*ssl.DescribeHostTeoInstanceListResponse, error)
	DeployCertificateInstance(*ssl.DeployCertificateInstanceRequest) (*ssl.DeployCertificateInstanceResponse, error)
	DescribeHostDeployRecord(*ssl.DescribeHostDeployRecordRequest) (*ssl.DescribeHostDeployRecordResponse, error)
	DeleteCertificates(*ssl.DeleteCertificatesRequest) (*ssl.DeleteCertificatesResponse, error)
	DescribeDeleteCertificatesTaskResult(*ssl.DescribeDeleteCertificatesTaskResultRequest) (*ssl.DescribeDeleteCertificatesTaskResultResponse, error)
}

// cdnAPI is the subset of the CDN client Orbit calls.
type cdnAPI interface {
	DescribeDomainsConfig(*cdn.DescribeDomainsConfigRequest) (*cdn.DescribeDomainsConfigResponse, error)
	ModifyDomainConfig(*cdn.ModifyDomainConfigRequest) (*cdn.ModifyDomainConfigResponse, error)
}

// teoAPI is the subset of the EdgeOne client Orbit calls.
type teoAPI interface {
	DescribeZones(*teo.DescribeZonesRequest) (*teo.DescribeZonesResponse, error)
	DescribeAccelerationDomains(*teo.DescribeAccelerationDomainsRequest) (*teo.DescribeAccelerationDomainsResponse, error)
	ModifyHostsCertificate(*teo.ModifyHostsCertificateRequest) (*teo.ModifyHostsCertificateResponse, error)
}

// stsAPI is the subset of the STS client Orbit calls, used to report which
// account a credential actually belongs to.
type stsAPI interface {
	GetCallerIdentity(*sts.GetCallerIdentityRequest) (*sts.GetCallerIdentityResponse, error)
}

// newSDKClients builds the product clients sharing one credential.
func newSDKClients(opts Options) (sslAPI, cdnAPI, teoAPI, stsAPI, error) {
	credential := common.NewCredential(opts.SecretID, opts.SecretKey)

	profileFor := func() *profile.ClientProfile {
		prof := profile.NewClientProfile()
		prof.HttpProfile.ReqTimeout = int(opts.APITimeout.Seconds())
		// The SDK has its own retry knobs; Orbit adds pacing on top so that a
		// batch run never exceeds the documented per-API QPS.
		prof.NetworkFailureMaxRetries = 3
		prof.RateLimitExceededMaxRetries = 3
		if opts.Endpoint != "" {
			prof.HttpProfile.Endpoint = opts.Endpoint
		}
		return prof
	}

	sslClient, err := ssl.NewClient(credential, opts.Region, profileFor())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cdnClient, err := cdn.NewClient(credential, opts.Region, profileFor())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	teoClient, err := teo.NewClient(credential, opts.Region, profileFor())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stsClient, err := sts.NewClient(credential, opts.Region, profileFor())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return sslClient, cdnClient, teoClient, stsClient, nil
}

// shanghai is the timezone Tencent Cloud uses for the naive timestamps it
// returns (SSL `CertEndTime`, CDN `ExpireTime`, ...).
var shanghai = time.FixedZone("GMT+8", 8*60*60)

// parseTime tolerates the several timestamp shapes the three APIs return.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, shanghai); err == nil {
			return t
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(n, 0).In(shanghai)
	}
	return time.Time{}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func u64(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

func i64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func strs(list []*string) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		if p != nil && *p != "" {
			out = append(out, *p)
		}
	}
	return out
}

// ptrI64 keeps an optional integer distinguishable from its zero value, which
// matters for HostingStatus where 0 means "hosting" (see core.Certificate).
func ptrI64(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
