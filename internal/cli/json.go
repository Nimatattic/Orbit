package cli

import (
	"time"

	"github.com/Nimatattic/Orbit/internal/core"
)

// The JSON documents of update/clean are a public interface (cron jobs pipe them
// into jq), so their field names are snake_case and stable. The core types
// deliberately carry no json tags: their field names are Go API, not a wire
// contract, and every presentation layer owns its own document shape. These
// DTOs are that contract for the CLI, the same way warningDTO covers scan
// warnings.

// updateOutput is the per-account JSON document of `update`. Warnings are
// omitted when the scan was complete, matching status.
type updateOutput struct {
	Account  string       `json:"account"`
	Plan     planDTO      `json:"plan"`
	Warnings []warningDTO `json:"warnings,omitempty"`
}

// cleanOutput is the per-account JSON document of `clean`.
type cleanOutput struct {
	Account  string       `json:"account"`
	Clean    cleanPlanDTO `json:"clean"`
	Warnings []warningDTO `json:"warnings,omitempty"`
}

// planDTO is the JSON shape of one account's update plan.
type planDTO struct {
	Account     string      `json:"account"`
	GeneratedAt string      `json:"generated_at,omitempty"`
	Changes     []changeDTO `json:"changes"`
}

// changeDTO is one plan row.
type changeDTO struct {
	Account      string      `json:"account,omitempty"`
	Product      string      `json:"product"`
	ZoneID       string      `json:"zone_id,omitempty"`
	ResourceID   string      `json:"resource_id,omitempty"`
	ResourceName string      `json:"resource_name,omitempty"`
	Domain       string      `json:"domain"`
	Kind         string      `json:"kind"`
	Strategy     string      `json:"strategy,omitempty"`
	InstanceKey  string      `json:"instance_key,omitempty"`
	From         *certRefDTO `json:"from,omitempty"`
	To           *certRefDTO `json:"to,omitempty"`
	Risk         string      `json:"risk,omitempty"`
	Reasons      []string    `json:"reasons,omitempty"`
}

// certRefDTO is a certificate reference inside a plan row.
type certRefDTO struct {
	CertID   string `json:"cert_id"`
	Alias    string `json:"alias,omitempty"`
	Source   string `json:"source,omitempty"`
	NotAfter string `json:"not_after,omitempty"`
}

// cleanPlanDTO is the JSON shape of one account's cleanup plan.
type cleanPlanDTO struct {
	Account     string              `json:"account"`
	GeneratedAt string              `json:"generated_at,omitempty"`
	Candidates  []cleanCandidateDTO `json:"candidates"`
	Skipped     []cleanSkippedDTO   `json:"skipped,omitempty"`
}

// cleanCandidateDTO is one deletable certificate with the evidence behind it.
type cleanCandidateDTO struct {
	Cert         certificateDTO `json:"cert"`
	SupersededBy string         `json:"superseded_by,omitempty"`
	Evidence     evidenceDTO    `json:"evidence"`
}

// cleanSkippedDTO is one certificate the gates kept, with the reason key.
type cleanSkippedDTO struct {
	Cert   certificateDTO `json:"cert"`
	Reason string         `json:"reason"`
}

// certificateDTO is the certificate view used by cleanup documents.
type certificateDTO struct {
	CertID        string            `json:"cert_id"`
	Alias         string            `json:"alias,omitempty"`
	Source        string            `json:"source,omitempty"`
	Domain        string            `json:"domain,omitempty"`
	SANs          []string          `json:"sans,omitempty"`
	NotBefore     string            `json:"not_before,omitempty"`
	NotAfter      string            `json:"not_after,omitempty"`
	InsertTime    string            `json:"insert_time,omitempty"`
	Status        uint64            `json:"status"`
	Deployable    bool              `json:"deployable"`
	PackageType   string            `json:"package_type,omitempty"`
	HostingStatus *int64            `json:"hosting_status,omitempty"`
	AutoRenew     bool              `json:"auto_renew,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
	Region        string            `json:"region,omitempty"`
}

// evidenceDTO is the association query result behind one cleanup candidate.
type evidenceDTO struct {
	CertID     string             `json:"cert_id,omitempty"`
	Status     uint64             `json:"status"`
	Complete   bool               `json:"complete"`
	TotalCount int                `json:"total_count"`
	Details    []bindingDetailDTO `json:"details,omitempty"`
	QueriedAt  string             `json:"queried_at,omitempty"`
	CacheTime  string             `json:"cache_time,omitempty"`
}

// bindingDetailDTO is one product/region entry of the association query.
type bindingDetailDTO struct {
	Product    string `json:"product"`
	Region     string `json:"region,omitempty"`
	TotalCount int    `json:"total_count"`
	Error      string `json:"error,omitempty"`
}

// resultsDTO is the per-account JSON document of an applied update/clean run.
type resultsDTO struct {
	Account string      `json:"account"`
	Results []resultDTO `json:"results"`
}

// resultDTO is one executed change.
type resultDTO struct {
	Change         changeDTO `json:"change"`
	OK             bool      `json:"ok"`
	Verified       bool      `json:"verified"`
	ErrorCode      string    `json:"error_code,omitempty"`
	Error          string    `json:"error,omitempty"`
	DeployRecordID string    `json:"deploy_record_id,omitempty"`
	MessageKey     string    `json:"message_key,omitempty"`
}

// rfc3339 renders a timestamp the way the rest of the JSON output does. The
// zero value becomes an empty string so omitempty can drop the field.
func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func newPlanDTO(plan core.Plan) planDTO {
	out := planDTO{
		Account:     plan.Account,
		GeneratedAt: rfc3339(plan.GeneratedAt),
		Changes:     make([]changeDTO, 0, len(plan.Changes)),
	}
	for _, ch := range plan.Changes {
		out.Changes = append(out.Changes, newChangeDTO(ch))
	}
	return out
}

func newChangeDTO(ch core.Change) changeDTO {
	dto := changeDTO{
		Account:      ch.Account,
		Product:      string(ch.Product),
		ZoneID:       ch.ZoneID,
		ResourceID:   ch.ResourceID,
		ResourceName: ch.ResourceName,
		Domain:       ch.Domain,
		Kind:         string(ch.Kind),
		Strategy:     string(ch.Strategy),
		InstanceKey:  ch.InstanceKey,
		Risk:         string(ch.Risk),
		Reasons:      ch.Reasons,
	}
	if ch.From != nil {
		ref := newCertRefDTO(ch.From)
		dto.From = &ref
	}
	if ch.To != nil {
		ref := newCertRefDTO(ch.To)
		dto.To = &ref
	}
	return dto
}

func newCertRefDTO(ref *core.CertRef) certRefDTO {
	return certRefDTO{
		CertID:   ref.ID,
		Alias:    ref.Alias,
		Source:   ref.Source,
		NotAfter: rfc3339(ref.NotAfter),
	}
}

func newCleanPlanDTO(plan core.CleanPlan) cleanPlanDTO {
	out := cleanPlanDTO{
		Account:     plan.Account,
		GeneratedAt: rfc3339(plan.GeneratedAt),
		Candidates:  make([]cleanCandidateDTO, 0, len(plan.Candidates)),
	}
	for _, c := range plan.Candidates {
		out.Candidates = append(out.Candidates, cleanCandidateDTO{
			Cert:         newCertificateDTO(c.Cert),
			SupersededBy: c.SupersededBy,
			Evidence:     newEvidenceDTO(c.Evidence),
		})
	}
	for _, s := range plan.Skipped {
		out.Skipped = append(out.Skipped, cleanSkippedDTO{
			Cert:   newCertificateDTO(s.Cert),
			Reason: s.Reason,
		})
	}
	return out
}

func newCertificateDTO(c core.Certificate) certificateDTO {
	return certificateDTO{
		CertID:        c.ID,
		Alias:         c.Alias,
		Source:        c.From,
		Domain:        c.Domain,
		SANs:          c.SANs,
		NotBefore:     rfc3339(c.NotBefore),
		NotAfter:      rfc3339(c.NotAfter),
		InsertTime:    rfc3339(c.InsertTime),
		Status:        c.Status,
		Deployable:    c.Deployable,
		PackageType:   c.PackageType,
		HostingStatus: c.HostingStatus,
		AutoRenew:     c.AutoRenew,
		Tags:          c.Tags,
		Region:        c.Region,
	}
}

func newEvidenceDTO(ev core.BindingEvidence) evidenceDTO {
	out := evidenceDTO{
		CertID:     ev.CertID,
		Status:     ev.Status,
		Complete:   ev.Complete,
		TotalCount: ev.TotalCount,
		QueriedAt:  rfc3339(ev.QueriedAt),
		CacheTime:  ev.CacheTime,
	}
	for _, d := range ev.Details {
		out.Details = append(out.Details, bindingDetailDTO{
			Product: d.Product, Region: d.Region, TotalCount: d.TotalCount, Error: d.Err,
		})
	}
	return out
}

func newResultsDTO(account string, results []core.Result) resultsDTO {
	out := resultsDTO{Account: account, Results: make([]resultDTO, 0, len(results))}
	for _, r := range results {
		dto := resultDTO{
			Change:         newChangeDTO(r.Change),
			OK:             r.OK,
			Verified:       r.Verified,
			ErrorCode:      r.ErrCode,
			DeployRecordID: r.DeployRecordID,
			MessageKey:     r.MessageKey,
		}
		if r.Err != nil {
			dto.Error = r.Err.Error()
		}
		out.Results = append(out.Results, dto)
	}
	return out
}

// --- DNS documents --------------------------------------------------------

// zoneDTO is the JSON shape of one hosted zone.
type zoneDTO struct {
	ID          uint64   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	DNSStatus   string   `json:"dns_status"`
	Grade       string   `json:"grade"`
	RecordCount uint64   `json:"record_count"`
	EffectiveNS []string `json:"effective_ns,omitempty"`
	UpdatedOn   string   `json:"updated_on,omitempty"`
}

// dnsZoneRow is one line of the zone listing.
type dnsZoneRow struct {
	Account string  `json:"account"`
	Zone    zoneDTO `json:"zone"`
}

// recordDTO is the JSON shape of one DNS record.
type recordDTO struct {
	ID        uint64 `json:"id"`
	Zone      string `json:"zone"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	TTL       uint64 `json:"ttl"`
	MX        uint64 `json:"mx"`
	Line      string `json:"line,omitempty"`
	Enabled   bool   `json:"enabled"`
	Remark    string `json:"remark,omitempty"`
	UpdatedOn string `json:"updated_on,omitempty"`
	FQDN      string `json:"fqdn"`
}

// dnsCheckDTO is the JSON document of `dns check`.
type dnsCheckDTO struct {
	Zone       string   `json:"zone"`
	FQDN       string   `json:"fqdn"`
	Type       string   `json:"type"`
	Configured []string `json:"configured"`
	Answers    []string `json:"answers"`
	Missing    []string `json:"missing"`
}

// dnsWriteDTO is the JSON document of a mutating dns command (set / rm /
// enable / disable): what was asked for, whether it ran, and the records
// involved. Verified is only meaningful for a write that reads back, so it is
// omitted elsewhere.
type dnsWriteDTO struct {
	Account  string      `json:"account"`
	Action   string      `json:"action"`
	Applied  bool        `json:"applied"`
	Verified *bool       `json:"verified,omitempty"`
	Records  []recordDTO `json:"records"`
}

func newZoneDTO(z core.DNSZone) zoneDTO {
	return zoneDTO{
		ID:          z.ID,
		Name:        z.Name,
		Status:      z.Status,
		DNSStatus:   z.DNSStatus,
		Grade:       z.Grade,
		RecordCount: z.RecordCount,
		EffectiveNS: z.EffectiveNS,
		UpdatedOn:   z.UpdatedOn,
	}
}

func newRecordDTO(r core.DNSRecord) recordDTO {
	return recordDTO{
		ID:        r.ID,
		Zone:      r.Zone,
		Name:      r.Name,
		Type:      r.Type,
		Value:     r.Value,
		TTL:       r.TTL,
		MX:        r.MX,
		Line:      r.Line,
		Enabled:   r.Enabled,
		Remark:    r.Remark,
		UpdatedOn: r.UpdatedOn,
		FQDN:      r.FQDN(),
	}
}

// newRecordDTOs never returns nil so an empty list marshals as [].
func newRecordDTOs(records []core.DNSRecord) []recordDTO {
	out := make([]recordDTO, 0, len(records))
	for _, r := range records {
		out = append(out, newRecordDTO(r))
	}
	return out
}

// recordFromInput is the intended record of a not-yet-created DNS record: it
// carries the requested values with a zero id, which is what a plan document
// describes.
func recordFromInput(in core.DNSRecordInput) core.DNSRecord {
	return core.DNSRecord{
		Zone: in.Zone, Name: in.Name, Type: in.Type, Value: in.Value,
		TTL: in.TTL, MX: in.MX, Line: in.Line, Remark: in.Remark, Enabled: in.Enabled,
	}
}

// newDNSCheckDTO never returns nil slices so the document stays uniform.
func newDNSCheckDTO(zone, fqdn, typ string, configured, answers, missing []string) dnsCheckDTO {
	return dnsCheckDTO{
		Zone: zone, FQDN: fqdn, Type: typ,
		Configured: stringSlice(configured),
		Answers:    stringSlice(answers),
		Missing:    stringSlice(missing),
	}
}

func stringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
