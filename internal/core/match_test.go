package core

import (
	"strings"
	"testing"
	"time"
)

func testCert(id, cn string, sans []string, notAfter time.Duration, from string) Certificate {
	now := time.Now()
	return Certificate{
		ID: id, Domain: cn, SANs: sans, From: from,
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(notAfter),
		InsertTime: now.Add(-24 * time.Hour),
		Status:     1, Deployable: true,
	}
}

func TestCover(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		cert   Certificate
		want   CoverKind
	}{
		{"exact CN", "example.com", testCert("c1", "example.com", nil, 24*time.Hour, "upload"), CoverExactCN},
		{"exact SAN", "www.example.com", testCert("c1", "example.com", []string{"www.example.com"}, 24*time.Hour, "upload"), CoverExactSAN},
		{"wildcard one label", "a.example.com", testCert("c1", "example.com", []string{"*.example.com"}, 24*time.Hour, "upload"), CoverWildcard},
		{"wildcard does not match apex", "example.com", testCert("c1", "other.com", []string{"*.example.com"}, 24*time.Hour, "upload"), CoverNone},
		{"wildcard is single label", "a.b.example.com", testCert("c1", "other.com", []string{"*.example.com"}, 24*time.Hour, "upload"), CoverNone},
		{"case and trailing dot", "WWW.Example.COM.", testCert("c1", "example.com", []string{"www.example.com"}, 24*time.Hour, "upload"), CoverExactSAN},
		{"no relation", "other.org", testCert("c1", "example.com", []string{"a.example.com"}, 24*time.Hour, "upload"), CoverNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Cover(tc.cert, tc.domain); got != tc.want {
				t.Fatalf("Cover(%q) = %v, want %v", tc.domain, got, tc.want)
			}
		})
	}
}

func TestNormalizeDomainIDN(t *testing.T) {
	got, err := NormalizeDomain("例子.COM.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "xn--") {
		t.Fatalf("expected punycode, got %q", got)
	}
}

func TestBestForPrefersLatestExpiry(t *testing.T) {
	now := time.Now()
	old := testCert("c-old", "example.com", nil, 30*24*time.Hour, "upload")
	fresh := testCert("c-new", "example.com", nil, 300*24*time.Hour, "upload")

	got, ok := BestFor("example.com", []Certificate{old, fresh}, now, MatchOptions{})
	if !ok {
		t.Fatal("expected a match")
	}
	if got.ID != "c-new" {
		t.Fatalf("got %s, want c-new (later expiry wins)", got.ID)
	}
}

func TestBestForPrefersUploadOnTie(t *testing.T) {
	now := time.Now()
	bought := testCert("c-buy", "example.com", nil, 300*24*time.Hour, "trustasia")
	uploaded := testCert("c-upload", "example.com", nil, 300*24*time.Hour, "upload")

	got, ok := BestFor("example.com", []Certificate{bought, uploaded}, now, MatchOptions{Preference: []string{"upload", "buy"}})
	if !ok {
		t.Fatal("expected a match")
	}
	if got.ID != "c-upload" {
		t.Fatalf("got %s, want c-upload (decision B2a)", got.ID)
	}
}

func TestBestForSpecificityBeatsExpiry(t *testing.T) {
	now := time.Now()
	wildcard := testCert("c-wild", "other.com", []string{"*.example.com"}, 400*24*time.Hour, "upload")
	exact := testCert("c-exact", "other.com", []string{"api.example.com"}, 100*24*time.Hour, "upload")

	got, ok := BestFor("api.example.com", []Certificate{wildcard, exact}, now, MatchOptions{})
	if !ok {
		t.Fatal("expected a match")
	}
	if got.ID != "c-exact" {
		t.Fatalf("got %s, want c-exact (rank 2 beats rank 4)", got.ID)
	}
}

func TestBestForSkipsExpiredUnlessAllowed(t *testing.T) {
	now := time.Now()
	expired := testCert("c-expired", "example.com", nil, -24*time.Hour, "upload")

	if _, ok := BestFor("example.com", []Certificate{expired}, now, MatchOptions{}); ok {
		t.Fatal("expired certificate must not be selected by default")
	}
	got, ok := BestFor("example.com", []Certificate{expired}, now, MatchOptions{AllowExpired: true})
	if !ok || got.ID != "c-expired" {
		t.Fatalf("AllowExpired should select it, got %v ok=%v", got.ID, ok)
	}
}

func TestCertNamesUnionsDomainAndSANs(t *testing.T) {
	c := testCert("c1", "example.com", []string{"example.com", "www.example.com", "*.example.com"}, 24*time.Hour, "upload")
	names := CertNames(c)
	if len(names) != 3 {
		t.Fatalf("expected 3 unique names, got %v", names)
	}
}
