package tencent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	ssl "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205"

	"github.com/Crequency/orbit/internal/core"
)

// sslStates that allow a certificate to be deployed. Everything else (review,
// revocation, refund, migration) is filtered out before planning.
var deployableStates = map[uint64]bool{0: true, 1: true, 4: true, 5: true, 8: true, 13: true}

// fetchCertificates pages through the whole certificate pool of the account.
//
// SSL's certificate APIs are global: Region is neither required nor honoured,
// so a single pass sees every certificate (decision T6).
func (p *Provider) fetchCertificates(ctx context.Context) ([]core.Certificate, error) {
	var out []core.Certificate
	var offset uint64
	for {
		req := ssl.NewDescribeCertificatesRequest()
		req.Offset = common.Uint64Ptr(offset)
		req.Limit = common.Uint64Ptr(sslPageSize)

		resp, err := call(ctx, p.limiters["ssl"], p.opts.MaxAttempts, func() (*ssl.DescribeCertificatesResponse, error) {
			return p.ssl.DescribeCertificates(req)
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Response == nil {
			break
		}
		for _, item := range resp.Response.Certificates {
			out = append(out, toCertificate(item))
		}
		offset += uint64(len(resp.Response.Certificates))
		p.emit("progress.page.certificates", "count", len(out), "total", int(u64(resp.Response.TotalCount)))
		if len(resp.Response.Certificates) == 0 || offset >= u64(resp.Response.TotalCount) {
			break
		}
	}
	return out, nil
}

// toCertificate normalises the SSL list item. `SubjectAltName` on the list
// endpoint already includes the primary domain, and `CertSANs` is unioned in as
// well because the two fields do not always agree (plan §2.1).
func toCertificate(item *ssl.Certificates) core.Certificate {
	if item == nil {
		return core.Certificate{}
	}
	sans := append(strs(item.SubjectAltName), strs(item.CertSANs)...)
	tags := map[string]string{}
	for _, t := range item.Tags {
		if t == nil {
			continue
		}
		tags[str(t.TagKey)] = str(t.TagValue)
	}
	cert := core.Certificate{
		ID:            str(item.CertificateId),
		Alias:         str(item.Alias),
		From:          str(item.From),
		Domain:        str(item.Domain),
		SANs:          sans,
		NotBefore:     parseTime(str(item.CertBeginTime)),
		NotAfter:      parseTime(str(item.CertEndTime)),
		InsertTime:    parseTime(str(item.InsertTime)),
		Status:        u64(item.Status),
		Deployable:    item.Deployable == nil || *item.Deployable,
		PackageType:   str(item.PackageType),
		HostingStatus: ptrI64(item.HostingStatus),
		AutoRenew:     item.AutoRenewFlag != nil && *item.AutoRenewFlag != 0,
		Tags:          tags,
	}
	if item.Deployable == nil {
		// Older responses omit the field; fall back to the status whitelist.
		cert.Deployable = deployableStates[cert.Status]
	}
	return cert
}

// fetchBindings answers "which cloud resources use these certificates" using the
// authoritative association task (decision T4).
//
// All batches are created first and then polled together: polling batches
// serially would multiply the wait by the batch count (N x poll timeout), while
// the cloud runs those tasks concurrently.
func (p *Provider) fetchBindings(ctx context.Context, certIDs []string, useCache bool) (map[string]core.BindingEvidence, error) {
	out := map[string]core.BindingEvidence{}
	chunks := chunkStrings(certIDs, 100)
	taskToCert := map[string]string{}
	taskIDs := make([]string, 0, len(certIDs))

	for i, chunk := range chunks {
		create := ssl.NewCreateCertificateBindResourceSyncTaskRequest()
		create.CertificateIds = common.StringPtrs(chunk)
		create.IsCache = common.Uint64Ptr(boolToUint64(useCache))

		created, err := call(ctx, p.limiters["ssl"], p.opts.MaxAttempts, func() (*ssl.CreateCertificateBindResourceSyncTaskResponse, error) {
			return p.ssl.CreateCertificateBindResourceSyncTask(create)
		})
		if err != nil {
			return nil, err
		}
		p.emit("progress.batch.bindings", "batch", i+1, "batches", len(chunks), "count", len(chunk))
		if created == nil || created.Response == nil {
			continue
		}
		for _, t := range created.Response.CertTaskIds {
			if t == nil {
				continue
			}
			certID, taskID := str(t.CertId), str(t.TaskId)
			if certID == "" || taskID == "" {
				continue
			}
			taskToCert[taskID] = certID
			taskIDs = append(taskIDs, taskID)
			out[certID] = core.BindingEvidence{CertID: certID, QueriedAt: p.now()}
		}
	}
	if len(taskIDs) == 0 {
		return out, nil
	}
	if err := p.pollBindingTasks(ctx, taskIDs, taskToCert, out); err != nil {
		return nil, err
	}
	return out, nil
}

// pollBindingTasks polls every task (100 ids per request, as the API allows) in
// one loop, so the total wait tracks the slowest task instead of the sum.
func (p *Provider) pollBindingTasks(ctx context.Context, taskIDs []string, taskToCert map[string]string, out map[string]core.BindingEvidence) error {
	batches := chunkStrings(taskIDs, 100)
	start := p.now()
	deadline := start.Add(p.opts.PollTimeout)

	for {
		pending := 0
		for _, batch := range batches {
			req := ssl.NewDescribeCertificateBindResourceTaskResultRequest()
			req.TaskIds = common.StringPtrs(batch)

			resp, err := call(ctx, p.limiters["ssl"], p.opts.MaxAttempts, func() (*ssl.DescribeCertificateBindResourceTaskResultResponse, error) {
				return p.ssl.DescribeCertificateBindResourceTaskResult(req)
			})
			if err != nil {
				return err
			}
			if resp == nil || resp.Response == nil {
				continue
			}
			for _, r := range resp.Response.SyncTaskBindResourceResult {
				if r == nil {
					continue
				}
				certID := taskToCert[str(r.TaskId)]
				if certID == "" {
					continue
				}
				ev := core.BindingEvidence{
					CertID:    certID,
					Status:    u64(r.Status),
					CacheTime: str(r.CacheTime),
					QueriedAt: p.now(),
				}
				if r.Error != nil {
					ev.Details = append(ev.Details, core.BindingDetail{Product: "meta", Err: str(r.Error.Message)})
				}
				for _, br := range r.BindResourceResult {
					if br == nil {
						continue
					}
					for _, rr := range br.BindResourceRegionResult {
						if rr == nil {
							continue
						}
						ev.Details = append(ev.Details, core.BindingDetail{
							Product:    str(br.ResourceType),
							Region:     str(rr.Region),
							TotalCount: int(u64(rr.TotalCount)),
							Err:        str(rr.Error),
						})
						ev.TotalCount += int(u64(rr.TotalCount))
					}
				}
				ev.Complete = ev.Status == 1 && allDetailsOK(ev.Details)
				out[certID] = ev
				if ev.Status == 0 {
					pending++
				}
			}
		}
		if pending == 0 || !p.now().Before(deadline) {
			return nil
		}
		p.emit("progress.wait.bindings",
			"pending", pending,
			"waited", p.now().Sub(start).Round(time.Second).String())
		if err := p.sleep(ctx, p.opts.PollInterval); err != nil {
			return err
		}
	}
}

func allDetailsOK(details []core.BindingDetail) bool {
	for _, d := range details {
		if strings.TrimSpace(d.Err) != "" {
			return false
		}
	}
	return true
}

// fetchHostCDNInstances lists the CDN domains currently holding a certificate.
func (p *Provider) fetchHostCDNInstances(ctx context.Context, certID string) ([]*ssl.CdnInstanceDetail, error) {
	var out []*ssl.CdnInstanceDetail
	var offset uint64
	for {
		req := ssl.NewDescribeHostCdnInstanceListRequest()
		req.CertificateId = common.StringPtr(certID)
		req.IsCache = common.Uint64Ptr(1)
		req.Offset = common.Uint64Ptr(offset)
		req.Limit = common.Uint64Ptr(200)

		resp, err := call(ctx, p.limiters["ssl"], p.opts.MaxAttempts, func() (*ssl.DescribeHostCdnInstanceListResponse, error) {
			return p.ssl.DescribeHostCdnInstanceList(req)
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Response == nil || len(resp.Response.InstanceList) == 0 {
			break
		}
		out = append(out, resp.Response.InstanceList...)
		offset += uint64(len(resp.Response.InstanceList))
		if offset >= u64(resp.Response.TotalCount) {
			break
		}
	}
	return out, nil
}

// fetchHostTEOInstances lists the EdgeOne hosts currently holding a certificate.
func (p *Provider) fetchHostTEOInstances(ctx context.Context, certID string) ([]*ssl.TeoInstanceDetail, error) {
	var out []*ssl.TeoInstanceDetail
	var offset uint64
	for {
		req := ssl.NewDescribeHostTeoInstanceListRequest()
		req.CertificateId = common.StringPtr(certID)
		req.IsCache = common.Uint64Ptr(1)
		req.Offset = common.Uint64Ptr(offset)
		req.Limit = common.Uint64Ptr(200)

		resp, err := call(ctx, p.limiters["ssl"], p.opts.MaxAttempts, func() (*ssl.DescribeHostTeoInstanceListResponse, error) {
			return p.ssl.DescribeHostTeoInstanceList(req)
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Response == nil || len(resp.Response.InstanceList) == 0 {
			break
		}
		out = append(out, resp.Response.InstanceList...)
		offset += uint64(len(resp.Response.InstanceList))
		if offset >= uint64(i64(resp.Response.TotalCount)) {
			break
		}
	}
	return out, nil
}

// deployInstances runs the unified SSL deployment path (strategy S-UNI) and
// waits for its deploy record to reach a terminal state.
//
// Official constraint: one task per (certificate, resource type) at a time, so
// the caller groups instances accordingly.
func (p *Provider) deployInstances(ctx context.Context, certID, resourceType, region string, instanceIDs []string) (uint64, error) {
	if len(instanceIDs) == 0 {
		return 0, nil
	}
	req := ssl.NewDeployCertificateInstanceRequest()
	req.CertificateId = common.StringPtr(certID)
	req.ResourceType = common.StringPtr(resourceType)
	req.InstanceIdList = common.StringPtrs(instanceIDs)
	req.IsCache = common.Uint64Ptr(0)
	// cdn and teo take no Region (the client's region is irrelevant for them and
	// the API rejects an unexpected Region for the regional resource types).

	resp, err := call(ctx, p.limiters["ssl"], p.opts.MaxAttempts, func() (*ssl.DeployCertificateInstanceResponse, error) {
		return p.ssl.DeployCertificateInstance(req)
	})
	if err != nil {
		return 0, err
	}
	if resp == nil || resp.Response == nil {
		return 0, fmt.Errorf("empty deploy response for %s", certID)
	}
	recordID := u64(resp.Response.DeployRecordId)
	// DeployStatus 0 means "an in-flight task exists, this call created
	// nothing"; the returned record id is that task's.
	if err := p.waitDeployRecord(ctx, certID, resourceType, recordID); err != nil {
		return recordID, err
	}
	return recordID, nil
}

func (p *Provider) waitDeployRecord(ctx context.Context, certID, resourceType string, recordID uint64) error {
	deadline := p.now().Add(p.opts.PollTimeout)
	for {
		req := ssl.NewDescribeHostDeployRecordRequest()
		req.CertificateId = common.StringPtr(certID)
		if resourceType != "" {
			req.ResourceType = common.StringPtr(resourceType)
		}
		req.Limit = common.Uint64Ptr(100)

		resp, err := call(ctx, p.limiters["ssl"], p.opts.MaxAttempts, func() (*ssl.DescribeHostDeployRecordResponse, error) {
			return p.ssl.DescribeHostDeployRecord(req)
		})
		if err != nil {
			return err
		}
		status, found := uint64(0), false
		if resp != nil && resp.Response != nil {
			for _, rec := range resp.Response.DeployRecordList {
				if rec == nil {
					continue
				}
				if recordID != 0 && u64(rec.Id) != recordID {
					continue
				}
				status, found = u64(rec.Status), true
				break
			}
		}
		if found && status == 1 {
			return nil
		}
		if found && status == 2 {
			return errorWithKey(MsgDeployFailed, fmt.Errorf("deploy record %d for certificate %s failed", recordID, certID))
		}
		if !p.now().Before(deadline) {
			return errorWithKey(MsgDeployFailed, fmt.Errorf("deploy record %d for certificate %s did not finish in %s", recordID, certID, p.opts.PollTimeout))
		}
		if err := p.sleep(ctx, p.opts.PollInterval); err != nil {
			return err
		}
	}
}

// deleteCertificates removes certificates, always asking the cloud to check
// resource associations first (decision T5) and classifying the async result.
func (p *Provider) deleteCertificates(ctx context.Context, certIDs []string, opts deleteOptions) ([]core.Result, error) {
	var results []core.Result
	for _, params := range chunkStrings(certIDs, 100) {
		req := ssl.NewDeleteCertificatesRequest()
		req.CertificateIds = common.StringPtrs(params)
		// Must always be true: the API default is NOT to check associations,
		// which is the opposite of a safe default.
		req.IsSync = common.BoolPtr(opts.CheckBindings)

		resp, err := call(ctx, p.limiters["ssl"], p.opts.MaxAttempts, func() (*ssl.DeleteCertificatesResponse, error) {
			return p.ssl.DeleteCertificates(req)
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Response == nil {
			continue
		}
		for _, id := range resp.Response.Success {
			results = append(results, deleteResult(str(id), true, "", nil))
		}
		for _, fail := range resp.Response.Fail {
			if fail == nil {
				continue
			}
			results = append(results, deleteResult(str(fail.CertId), false, MsgUnknown, fmt.Errorf("%s", str(fail.Msg))))
		}
		if len(resp.Response.CertTaskIds) == 0 {
			continue
		}
		taskIDs := make([]string, 0, len(resp.Response.CertTaskIds))
		taskToCert := map[string]string{}
		for _, t := range resp.Response.CertTaskIds {
			if t == nil {
				continue
			}
			taskIDs = append(taskIDs, str(t.TaskId))
			taskToCert[str(t.TaskId)] = str(t.CertId)
		}
		taskResults, err := p.waitDeleteTasks(ctx, taskIDs, taskToCert)
		if err != nil {
			return nil, err
		}
		results = append(results, taskResults...)
	}
	return results, nil
}

type deleteOptions struct {
	CheckBindings bool
}

func (p *Provider) waitDeleteTasks(ctx context.Context, taskIDs []string, taskToCert map[string]string) ([]core.Result, error) {
	deadline := p.now().Add(p.opts.PollTimeout)
	for {
		req := ssl.NewDescribeDeleteCertificatesTaskResultRequest()
		req.TaskIds = common.StringPtrs(taskIDs)

		resp, err := call(ctx, p.limiter("ssl"), p.opts.MaxAttempts, func() (*ssl.DescribeDeleteCertificatesTaskResultResponse, error) {
			return p.ssl.DescribeDeleteCertificatesTaskResult(req)
		})
		if err != nil {
			return nil, err
		}
		var done []core.Result
		pending := 0
		if resp != nil && resp.Response != nil {
			for _, r := range resp.Response.DeleteTaskResult {
				if r == nil {
					continue
				}
				certID := str(r.CertId)
				if certID == "" {
					certID = taskToCert[str(r.TaskId)]
				}
				switch u64(r.Status) {
				case 0:
					pending++
				case 1:
					done = append(done, deleteResult(certID, true, "", nil))
				case 2:
					done = append(done, deleteResult(certID, false, MsgUnknown, fmt.Errorf("%s", str(r.Error))))
				case 3:
					// Service role not authorised for association checks.
					done = append(done, deleteResult(certID, false, MsgDeleteRoleMiss, fmt.Errorf("%s", str(r.Error))))
				case 4:
					// Still bound to a cloud resource: keep it.
					done = append(done, deleteResult(certID, false, MsgDeleteKeptBound, fmt.Errorf("%s", str(r.Error))))
				case 5:
					// Association query timed out: never delete on weak evidence.
					done = append(done, deleteResult(certID, false, MsgDeleteKeptQuery, fmt.Errorf("%s", str(r.Error))))
				default:
					done = append(done, deleteResult(certID, false, MsgUnknown, fmt.Errorf("unexpected delete status %d", u64(r.Status))))
				}
			}
		}
		if pending == 0 {
			return done, nil
		}
		if !p.now().Before(deadline) {
			return done, nil
		}
		if err := p.sleep(ctx, p.opts.PollInterval); err != nil {
			return nil, err
		}
	}
}

func deleteResult(certID string, ok bool, key string, err error) core.Result {
	return core.Result{
		Change:     core.Change{Kind: core.KindDelete, To: &core.CertRef{ID: certID}},
		OK:         ok,
		Verified:   ok,
		MessageKey: key,
		Err:        err,
	}
}

func chunkStrings(in []string, size int) [][]string {
	if size <= 0 {
		size = 100
	}
	var out [][]string
	for start := 0; start < len(in); start += size {
		end := start + size
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[start:end])
	}
	return out
}

func boolToUint64(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// errorWithKey attaches a message key to a plain error.
func errorWithKey(key string, err error) error {
	return &keyedError{key: key, err: err}
}

type keyedError struct {
	key string
	err error
}

func (e *keyedError) Error() string      { return e.err.Error() }
func (e *keyedError) Unwrap() error      { return e.err }
func (e *keyedError) MessageKey() string { return e.key }
