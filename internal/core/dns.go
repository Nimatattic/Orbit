package core

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// DNSRecordType is a record type Orbit can validate and manage.
type DNSRecordType = string

// Record types Orbit understands. Anything else is passed through untouched by
// the lister but cannot be produced by `dns set`.
const (
	DNSTypeA     = "A"
	DNSTypeAAAA  = "AAAA"
	DNSTypeCNAME = "CNAME"
	DNSTypeMX    = "MX"
	DNSTypeTXT   = "TXT"
	DNSTypeNS    = "NS"
	DNSTypeSRV   = "SRV"
	DNSTypeCAA   = "CAA"
)

// SupportedDNSTypes lists the types the CLI can create or update.
var SupportedDNSTypes = []string{
	DNSTypeA, DNSTypeAAAA, DNSTypeCNAME, DNSTypeMX,
	DNSTypeTXT, DNSTypeNS, DNSTypeSRV, DNSTypeCAA,
}

// DefaultDNSTTL is used when the operator does not pick one (DNSPod's default).
const DefaultDNSTTL uint64 = 600

// DNSZone is a hosted zone (a domain delegated to the DNS provider).
type DNSZone struct {
	ID          uint64
	Name        string
	Status      string
	DNSStatus   string
	Grade       string
	RecordCount uint64
	EffectiveNS []string
	UpdatedOn   string
}

// DNSRecord is one record inside a zone.
type DNSRecord struct {
	ID        uint64
	Zone      string
	Name      string // subdomain; "@" is the apex, "*" is a wildcard
	Type      string
	Value     string
	TTL       uint64
	MX        uint64
	Line      string
	Enabled   bool
	Remark    string
	UpdatedOn string
}

// FQDN is the fully qualified name of the record.
func (r DNSRecord) FQDN() string {
	return RecordFQDN(r.Zone, r.Name)
}

// RecordFQDN joins a subdomain with its zone, treating "@" as the apex.
func RecordFQDN(zone, name string) string {
	zone = NormaliseZone(zone)
	name = NormaliseDNSName(name)
	switch name {
	case "@", "":
		return zone
	}
	if strings.HasSuffix(name, "."+zone) {
		return name
	}
	return name + "." + zone
}

// IsApex reports whether the record name addresses the zone itself.
func (r DNSRecord) IsApex() bool {
	return NormaliseDNSName(r.Name) == "@"
}

// DNSRecordInput is a desired record, as typed on the command line.
type DNSRecordInput struct {
	Zone    string
	Name    string
	Type    string
	Value   string
	TTL     uint64
	MX      uint64
	Line    string
	Remark  string
	Enabled bool
}

// WithDefaults fills the values an operator may leave empty: the apex name, the
// most common type, and the provider's default TTL.
func (in DNSRecordInput) WithDefaults() DNSRecordInput {
	if strings.TrimSpace(in.Name) == "" {
		in.Name = "@"
	}
	if strings.TrimSpace(in.Type) == "" {
		in.Type = DNSTypeA
	}
	if in.TTL == 0 {
		in.TTL = DefaultDNSTTL
	}
	return in
}

// NormaliseDNSName canonicalises a subdomain:
// empty or "." becomes the apex "@"; case is folded; a trailing dot is dropped.
func NormaliseDNSName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "@" {
		return "@"
	}
	name = strings.TrimSuffix(name, ".")
	return strings.ToLower(name)
}

// NormaliseZone canonicalises a zone name (lower case, no trailing dot).
func NormaliseZone(zone string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))
}

// labelPattern matches a single DNS label that this tool is willing to create.
var labelPattern = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)

// ValidateRecordInput checks a record against per-type rules before it reaches
// the cloud. Validation lives here, in pure code, so both the CLI and the TUI ask
// the same questions.
func ValidateRecordInput(in DNSRecordInput) error {
	zone := NormaliseZone(in.Zone)
	if zone == "" {
		return fmt.Errorf("zone is required")
	}
	if !strings.Contains(zone, ".") {
		return fmt.Errorf("zone %q is not a domain name", in.Zone)
	}
	recType := strings.ToUpper(strings.TrimSpace(in.Type))
	if !isSupportedDNSType(recType) {
		return fmt.Errorf("unsupported record type %q (expected one of %s)",
			in.Type, strings.Join(SupportedDNSTypes, ", "))
	}
	if err := validateDNSName(in.Name, zone); err != nil {
		return err
	}
	if in.TTL > 86400 {
		return fmt.Errorf("ttl %d exceeds 86400", in.TTL)
	}
	value := strings.TrimSpace(in.Value)
	if value == "" {
		return fmt.Errorf("value is required")
	}
	switch recType {
	case DNSTypeA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("%q is not an IPv4 address", value)
		}
	case DNSTypeAAAA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("%q is not an IPv6 address", value)
		}
	case DNSTypeCNAME, DNSTypeNS:
		if err := validateTarget(value); err != nil {
			return err
		}
		if recType == DNSTypeCNAME && NormaliseDNSName(in.Name) == "@" {
			return fmt.Errorf("a CNAME cannot live at the zone apex (@); use an A/AAAA record or a subdomain")
		}
	case DNSTypeMX:
		if in.MX < 1 || in.MX > 20 {
			return fmt.Errorf("mx priority %d is outside 1-20", in.MX)
		}
		if err := validateTarget(value); err != nil {
			return err
		}
	case DNSTypeTXT:
		if len(value) > 255 {
			return fmt.Errorf("a TXT value is limited to 255 bytes (got %d); split it or use a different type", len(value))
		}
	case DNSTypeSRV:
		parts := strings.Fields(value)
		if len(parts) != 4 {
			return fmt.Errorf("SRV value must be %q", "priority weight port target")
		}
		for _, p := range parts[:3] {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n > 65535 {
				return fmt.Errorf("SRV value %q is not a valid number", p)
			}
		}
		if err := validateTarget(parts[3]); err != nil {
			return err
		}
	case DNSTypeCAA:
		parts := strings.Fields(value)
		if len(parts) < 3 {
			return fmt.Errorf("CAA value must be %q", `flags tag "value"`)
		}
		if _, err := strconv.Atoi(parts[0]); err != nil {
			return fmt.Errorf("CAA flags %q is not a number", parts[0])
		}
		switch strings.ToLower(parts[1]) {
		case "issue", "issuewild", "iodef":
		default:
			return fmt.Errorf("CAA tag %q is not issue, issuewild or iodef", parts[1])
		}
	}
	return nil
}

// validateDNSName rejects names the provider would refuse or that would be
// surprising: long labels, illegal characters, or a name that already carries
// the zone suffix.
func validateDNSName(name, zone string) error {
	n := NormaliseDNSName(name)
	if n == "@" {
		return nil
	}
	if n == "*" {
		return nil
	}
	if strings.HasSuffix(n, "."+zone) {
		return fmt.Errorf("name %q already includes the zone; pass just the subdomain", name)
	}
	if len(n) > 253 {
		return fmt.Errorf("name exceeds 253 characters")
	}
	for _, label := range strings.Split(n, ".") {
		if label == "*" {
			continue
		}
		if !labelPattern.MatchString(label) {
			return fmt.Errorf("label %q is not a valid hostname label", label)
		}
	}
	return nil
}

// validateTarget checks a hostname used as a value (CNAME/NS/MX/SRV target).
func validateTarget(value string) error {
	v := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if v == "" {
		return fmt.Errorf("target is required")
	}
	if len(v) > 253 {
		return fmt.Errorf("target exceeds 253 characters")
	}
	for _, label := range strings.Split(v, ".") {
		if !labelPattern.MatchString(label) {
			return fmt.Errorf("target label %q is not a valid hostname label", label)
		}
	}
	return nil
}

func isSupportedDNSType(t string) bool {
	for _, s := range SupportedDNSTypes {
		if s == t {
			return true
		}
	}
	return false
}

// DNSChangeKind says what an upsert would do.
type DNSChangeKind string

// Upsert outcomes.
const (
	DNSCreate DNSChangeKind = "create"
	DNSUpdate DNSChangeKind = "update"
	DNSKeep   DNSChangeKind = "keep"
)

// DNSChange is the plan for one `dns set`.
type DNSChange struct {
	Kind     DNSChangeKind
	Existing *DNSRecord
	Desired  DNSRecordInput
	// Reason explains a "keep" or a refused change in operator terms.
	Reason string
}

// PlanUpsert decides whether a record must be created, updated or left alone.
//
// Matching is by (name, type, line). More than one match is ambiguous: the caller
// must target a record id explicitly rather than guess which one to rewrite.
func PlanUpsert(records []DNSRecord, in DNSRecordInput) (DNSChange, error) {
	name := NormaliseDNSName(in.Name)
	recType := strings.ToUpper(strings.TrimSpace(in.Type))
	line := strings.TrimSpace(in.Line)

	var matches []DNSRecord
	for _, r := range records {
		if NormaliseDNSName(r.Name) != name || strings.ToUpper(r.Type) != recType {
			continue
		}
		if line != "" && r.Line != "" && !strings.EqualFold(r.Line, line) {
			continue
		}
		matches = append(matches, r)
	}

	switch len(matches) {
	case 0:
		return DNSChange{Kind: DNSCreate, Desired: in}, nil
	case 1:
		existing := matches[0]
		if dnsRecordMatches(existing, in) {
			return DNSChange{Kind: DNSKeep, Existing: &existing, Desired: in,
				Reason: "record already has the requested value"}, nil
		}
		return DNSChange{Kind: DNSUpdate, Existing: &existing, Desired: in}, nil
	default:
		return DNSChange{}, fmt.Errorf(
			"%d records named %q with type %s exist (round-robin); target one with --record-id or delete the extras first",
			len(matches), name, recType)
	}
}

// dnsRecordMatches reports whether an existing record already equals the input.
func dnsRecordMatches(r DNSRecord, in DNSRecordInput) bool {
	if strings.TrimSpace(r.Value) != strings.TrimSpace(in.Value) {
		return false
	}
	if in.TTL != 0 && r.TTL != in.TTL {
		return false
	}
	if strings.ToUpper(r.Type) == DNSTypeMX && in.MX != 0 && r.MX != in.MX {
		return false
	}
	if r.Enabled != in.Enabled {
		return false
	}
	return true
}

// DNSRecordFilter narrows a record listing.
type DNSRecordFilter struct {
	Type    string
	Name    string
	Keyword string
}

// FilterDNSRecords applies a filter to a record list.
func FilterDNSRecords(records []DNSRecord, f DNSRecordFilter) []DNSRecord {
	out := make([]DNSRecord, 0, len(records))
	for _, r := range records {
		if f.Type != "" && !strings.EqualFold(r.Type, f.Type) {
			continue
		}
		if f.Name != "" && NormaliseDNSName(r.Name) != NormaliseDNSName(f.Name) {
			continue
		}
		if f.Keyword != "" {
			k := strings.ToLower(f.Keyword)
			hay := strings.ToLower(r.Name + " " + r.Value + " " + r.Remark)
			if !strings.Contains(hay, k) {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// FindDNSRecord locates one record by id.
func FindDNSRecord(records []DNSRecord, id uint64) (DNSRecord, bool) {
	for _, r := range records {
		if r.ID == id {
			return r, true
		}
	}
	return DNSRecord{}, false
}
