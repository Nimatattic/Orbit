package core

import (
	"path"
	"strings"
	"time"
)

// Cleanup gate reason keys (see plan §2.3).
const (
	ReasonNotUploaded    = "clean.reason.not_uploaded"
	ReasonInUse          = "clean.reason.in_use"
	ReasonIncomplete     = "clean.reason.incomplete_evidence"
	ReasonNotSuperseded  = "clean.reason.not_superseded"
	ReasonTooYoung       = "clean.reason.too_young"
	ReasonProtected      = "clean.reason.protected"
	ReasonFreeCert       = "clean.reason.free"
	ReasonHosted         = "clean.reason.hosted"
	ReasonNoEvidenceData = "clean.reason.no_evidence"
)

// CleanOptions configures CleanCandidates.
type CleanOptions struct {
	Account string
	Now     time.Time
	// OlderThan is gate G4's grace period; zero disables the age check.
	OlderThan time.Duration
	// RequireFullEvidence enables gate G2: a candidate must have a complete
	// association query with zero bindings.
	RequireFullEvidence bool
	// IncludeFree allows deleting free certificates (off by default, A4).
	IncludeFree bool
	// ProtectPatterns holds glob patterns matched against the alias and domains.
	ProtectPatterns []string
}

// CleanCandidates applies gates G1–G4 and returns what may be deleted, plus
// everything that was kept back together with the reason.
//
// It deliberately errs towards keeping a certificate: any missing or ambiguous
// evidence results in a skip (plan §2.3).
func CleanCandidates(certs []Certificate, evidence map[string]BindingEvidence, resources []Resource, opts CleanOptions) CleanPlan {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	plan := CleanPlan{Account: opts.Account, GeneratedAt: now}

	inUse := map[string]bool{}
	for _, res := range resources {
		if res.BoundCert != "" {
			inUse[res.BoundCert] = true
		}
	}

	for _, cert := range certs {
		if reason := cleanSkipReason(cert, evidence, inUse, certs, now, opts); reason != "" {
			plan.Skipped = append(plan.Skipped, CleanSkipped{Cert: cert, Reason: reason})
			continue
		}
		supersededBy := ""
		for _, domain := range SortedDomains(cert) {
			if best, ok := BestFor(domain, certs, now, MatchOptions{}); ok && best.ID != cert.ID {
				supersededBy = best.ID
				break
			}
		}
		ev := evidence[cert.ID]
		plan.Candidates = append(plan.Candidates, CleanCandidate{Cert: cert, SupersededBy: supersededBy, Evidence: ev})
	}
	return plan
}

// cleanSkipReason returns the first failing gate's reason key, or "" when the
// certificate passes every gate.
//
// Gate order matters for reporting: "still in use" is checked before the grace
// period so that the most actionable reason wins.
func cleanSkipReason(cert Certificate, evidence map[string]BindingEvidence, inUse map[string]bool, all []Certificate, now time.Time, opts CleanOptions) string {
	// G1: only user-uploaded certificates may ever be deleted (decision A2/B3).
	if !cert.IsUploaded() {
		return ReasonNotUploaded
	}
	if cert.IsFree() && !opts.IncludeFree {
		return ReasonFreeCert
	}
	if cert.IsHosted() {
		return ReasonHosted
	}
	// G2a: local cross-check — anything a resource currently points at is in use.
	if inUse[cert.ID] {
		return ReasonInUse
	}
	// G2b: the authoritative association evidence must be complete and zero.
	ev, ok := evidence[cert.ID]
	switch {
	case !ok:
		if opts.RequireFullEvidence {
			return ReasonNoEvidenceData
		}
	case ev.Status == 2:
		return ReasonIncomplete
	case ev.TotalCount > 0:
		return ReasonInUse
	case opts.RequireFullEvidence && !ev.Complete:
		return ReasonIncomplete
	}
	// G4: age and protection.
	ref := cert.NotBefore
	if ref.IsZero() {
		ref = cert.InsertTime
	}
	if ref.IsZero() {
		return ReasonTooYoung
	}
	if opts.OlderThan > 0 && now.Sub(ref) < opts.OlderThan {
		return ReasonTooYoung
	}
	if protected(cert, opts.ProtectPatterns) {
		return ReasonProtected
	}
	// G3: every covered domain must already have a better certificate.
	for _, domain := range SortedDomains(cert) {
		best, found := BestFor(domain, all, now, MatchOptions{})
		if !found || best.ID == cert.ID {
			return ReasonNotSuperseded
		}
	}
	return ""
}

func protected(cert Certificate, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	candidates := append([]string{cert.Alias, cert.ID}, SortedDomains(cert)...)
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		for _, name := range candidates {
			if name == "" {
				continue
			}
			if ok, err := path.Match(pat, name); err == nil && ok {
				return true
			}
			if strings.Contains(name, pat) {
				return true
			}
		}
	}
	return false
}
