package core

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

// CoverKind describes how well a certificate matches a domain; higher is more
// specific. See plan §2.1.
type CoverKind int

// Coverage kinds, ordered by specificity.
const (
	CoverNone CoverKind = iota
	CoverWildcard
	CoverExactSAN
	CoverExactCN
)

// String renders a coverage kind for logs.
func (k CoverKind) String() string {
	switch k {
	case CoverExactCN:
		return "exact-cn"
	case CoverExactSAN:
		return "exact-san"
	case CoverWildcard:
		return "wildcard"
	default:
		return "none"
	}
}

// NormalizeDomain lowercases, strips the trailing dot and converts IDNs to
// punycode so that comparison is byte-stable.
func NormalizeDomain(domain string) (string, error) {
	d := strings.TrimSpace(domain)
	d = strings.TrimSuffix(d, ".")
	d = strings.ToLower(d)
	if d == "" {
		return "", fmt.Errorf("empty domain")
	}
	ascii, err := idna.Lookup.ToASCII(d)
	if err != nil {
		return "", fmt.Errorf("invalid domain %q: %w", domain, err)
	}
	return ascii, nil
}

// NormalizeName is a best-effort variant used for certificate names: an
// unparsable name is lowercased rather than rejected, so one odd SAN cannot
// invalidate a whole certificate.
func NormalizeName(name string) string {
	if n, err := NormalizeDomain(name); err == nil {
		return n
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

// CertNames returns the primary domain followed by the SAN list, deduplicated.
func CertNames(c Certificate) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(c.SANs)+1)
	add := func(v string) {
		if strings.TrimSpace(v) == "" {
			return
		}
		n := NormalizeName(v)
		if n == "" {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	add(c.Domain)
	for _, s := range c.SANs {
		add(s)
	}
	return out
}

// Cover reports how cert covers domain. Wildcards follow RFC 6125: `*.a.com`
// matches exactly one extra label, so it matches `x.a.com` but neither `a.com`
// nor `x.y.a.com`.
func Cover(c Certificate, domain string) CoverKind {
	d := NormalizeName(domain)
	if d == "" {
		return CoverNone
	}
	if cn := NormalizeName(c.Domain); cn != "" && cn == d {
		return CoverExactCN
	}
	best := CoverNone
	for _, name := range CertNames(c) {
		if name == d {
			if best < CoverExactSAN {
				best = CoverExactSAN
			}
			continue
		}
		if wildcardCovers(name, d) && best < CoverWildcard {
			best = CoverWildcard
		}
	}
	return best
}

func wildcardCovers(pattern, domain string) bool {
	if !strings.HasPrefix(pattern, "*.") {
		return false
	}
	suffix := pattern[2:]
	if suffix == "" {
		return false
	}
	if !strings.HasSuffix(domain, "."+suffix) {
		return false
	}
	// Exactly one extra label.
	rest := strings.TrimSuffix(domain, "."+suffix)
	return rest != "" && !strings.Contains(rest, ".")
}

// MatchOptions tunes candidate selection.
type MatchOptions struct {
	// AllowExpired permits selecting an expired certificate (troubleshooting).
	AllowExpired bool
	// Preference ranks certificate sources, e.g. ["upload", "buy"]. Decision
	// B2a: later NotAfter wins first; this only breaks ties.
	Preference []string
}

type candidate struct {
	cert   Certificate
	cover  CoverKind
	valid  bool
	labels int // wildcard label count: more specific wins
}

// BestFor selects the certificate to use for domain, implementing the ranking
// in plan §2.1 (validity, specificity, expiry, insert time, source, id).
func BestFor(domain string, certs []Certificate, now time.Time, opts MatchOptions) (Certificate, bool) {
	var best *candidate
	for _, c := range certs {
		cover := Cover(c, domain)
		if cover == CoverNone {
			continue
		}
		valid := c.Valid(now) && c.Deployable
		if !valid && !opts.AllowExpired {
			continue
		}
		cur := candidate{cert: c, cover: cover, valid: c.Valid(now), labels: wildcardLabels(c, domain)}
		if best == nil || betterCandidate(cur, *best, opts.Preference) {
			cp := cur
			best = &cp
		}
	}
	if best == nil {
		return Certificate{}, false
	}
	return best.cert, true
}

func wildcardLabels(c Certificate, domain string) int {
	d := NormalizeName(domain)
	max := 0
	for _, name := range CertNames(c) {
		if name == d || !strings.HasPrefix(name, "*.") {
			continue
		}
		if wildcardCovers(name, d) {
			if n := strings.Count(name, "."); n > max {
				max = n
			}
		}
	}
	return max
}

// betterCandidate reports whether a should win over b.
func betterCandidate(a, b candidate, pref []string) bool {
	if a.valid != b.valid {
		return a.valid
	}
	if a.cover != b.cover {
		return a.cover > b.cover
	}
	if a.labels != b.labels {
		return a.labels > b.labels
	}
	if !a.cert.NotAfter.Equal(b.cert.NotAfter) {
		return a.cert.NotAfter.After(b.cert.NotAfter)
	}
	if !a.cert.InsertTime.Equal(b.cert.InsertTime) {
		return a.cert.InsertTime.After(b.cert.InsertTime)
	}
	if pa, pb := prefIndex(a.cert, pref), prefIndex(b.cert, pref); pa != pb {
		return pa < pb
	}
	return a.cert.ID < b.cert.ID
}

func prefIndex(c Certificate, pref []string) int {
	for i, p := range pref {
		if p == c.From {
			return i
		}
	}
	return len(pref) + 1
}

// GroupByDomain maps each certificate name to the certificates covering it,
// which the cleanup gate G3 uses to prove a certificate was superseded.
func GroupByDomain(certs []Certificate) map[string][]Certificate {
	out := map[string][]Certificate{}
	for _, c := range certs {
		for _, n := range CertNames(c) {
			out[n] = append(out[n], c)
		}
	}
	return out
}

// SortedDomains returns the covered domains of a certificate in stable order.
func SortedDomains(c Certificate) []string {
	names := CertNames(c)
	sort.Strings(names)
	return names
}
