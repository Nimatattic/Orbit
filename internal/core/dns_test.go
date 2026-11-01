package core

import (
	"strings"
	"testing"
)

func validInput(t *testing.T, mutate func(*DNSRecordInput)) DNSRecordInput {
	t.Helper()
	in := DNSRecordInput{
		Zone: "example.com", Name: "www", Type: DNSTypeA, Value: "203.0.113.10",
		TTL: 600, Line: "默认", Enabled: true,
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}

func TestValidateRecordInputAcceptsCommonRecords(t *testing.T) {
	cases := map[string]DNSRecordInput{
		"A at a subdomain": validInput(t, nil),
		"apex A":           validInput(t, func(in *DNSRecordInput) { in.Name = "@"; in.Value = "198.51.100.7" }),
		"wildcard CNAME": validInput(t, func(in *DNSRecordInput) {
			in.Name, in.Type, in.Value = "*", DNSTypeCNAME, "target.example.net"
		}),
		"AAAA": validInput(t, func(in *DNSRecordInput) {
			in.Type, in.Value = DNSTypeAAAA, "2001:db8::1"
		}),
		"MX": validInput(t, func(in *DNSRecordInput) {
			in.Type, in.Value, in.MX = DNSTypeMX, "mail.example.net", 10
		}),
		"TXT (acme challenge)": validInput(t, func(in *DNSRecordInput) {
			in.Name, in.Type, in.Value = "_acme-challenge", DNSTypeTXT, "some-token-value"
		}),
		"SRV": validInput(t, func(in *DNSRecordInput) {
			in.Name, in.Type, in.Value = "_sip._tcp", DNSTypeSRV, "10 60 5060 sip.example.net"
		}),
		"CAA": validInput(t, func(in *DNSRecordInput) {
			in.Name, in.Type, in.Value = "@", DNSTypeCAA, `0 issue "letsencrypt.org"`
		}),
		"NS delegation": validInput(t, func(in *DNSRecordInput) {
			in.Name, in.Type, in.Value = "sub", DNSTypeNS, "ns1.example.net"
		}),
	}
	for name, in := range cases {
		if err := ValidateRecordInput(in); err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
	}
}

func TestValidateRecordInputRejectsBadValues(t *testing.T) {
	cases := map[string]struct {
		in  DNSRecordInput
		sub string
	}{
		"A with v6":    {validInput(t, func(in *DNSRecordInput) { in.Value = "2001:db8::1" }), "IPv4"},
		"AAAA with v4": {validInput(t, func(in *DNSRecordInput) { in.Type, in.Value = DNSTypeAAAA, "203.0.113.10" }), "IPv6"},
		"CNAME at apex": {validInput(t, func(in *DNSRecordInput) {
			in.Name, in.Type, in.Value = "@", DNSTypeCNAME, "target.example.net"
		}), "apex"},
		"MX priority": {validInput(t, func(in *DNSRecordInput) {
			in.Type, in.Value, in.MX = DNSTypeMX, "mail.example.net", 99
		}), "1-20"},
		"TXT too long": {validInput(t, func(in *DNSRecordInput) {
			in.Type, in.Value = DNSTypeTXT, strings.Repeat("x", 256)
		}), "255"},
		"SRV arity": {validInput(t, func(in *DNSRecordInput) {
			in.Name, in.Type, in.Value = "_sip._tcp", DNSTypeSRV, "10 60 sip.example.net"
		}), "priority"},
		"unknown type": {validInput(t, func(in *DNSRecordInput) { in.Type = "NAPTR" }), "unsupported"},
		"empty value":  {validInput(t, func(in *DNSRecordInput) { in.Value = "  " }), "value is required"},
		"ttl too big":  {validInput(t, func(in *DNSRecordInput) { in.TTL = 90000 }), "86400"},
		"name with zone suffix": {validInput(t, func(in *DNSRecordInput) {
			in.Name = "www.example.com"
		}), "already includes the zone"},
		"illegal label": {validInput(t, func(in *DNSRecordInput) { in.Name = "bad name" }), "label"},
		"no zone":       {validInput(t, func(in *DNSRecordInput) { in.Zone = "" }), "zone is required"},
	}
	for name, tc := range cases {
		err := ValidateRecordInput(tc.in)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.sub) {
			t.Errorf("%s: error %q does not mention %q", name, err, tc.sub)
		}
	}
}

func TestPlanUpsert(t *testing.T) {
	existing := []DNSRecord{
		{ID: 1, Zone: "example.com", Name: "www", Type: "A", Value: "203.0.113.10", TTL: 600, Line: "默认", Enabled: true},
		{ID: 2, Zone: "example.com", Name: "@", Type: "MX", Value: "mail.example.net", MX: 10, TTL: 600, Line: "默认", Enabled: true},
	}

	t.Run("create when nothing matches", func(t *testing.T) {
		ch, err := PlanUpsert(existing, validInput(t, func(in *DNSRecordInput) { in.Name = "api" }))
		if err != nil {
			t.Fatal(err)
		}
		if ch.Kind != DNSCreate {
			t.Fatalf("kind = %s, want create", ch.Kind)
		}
	})

	t.Run("keep when identical", func(t *testing.T) {
		ch, err := PlanUpsert(existing, validInput(t, nil))
		if err != nil {
			t.Fatal(err)
		}
		if ch.Kind != DNSKeep || ch.Existing == nil || ch.Existing.ID != 1 {
			t.Fatalf("kind = %s existing = %+v", ch.Kind, ch.Existing)
		}
	})

	t.Run("update when the value differs", func(t *testing.T) {
		ch, err := PlanUpsert(existing, validInput(t, func(in *DNSRecordInput) { in.Value = "203.0.113.11" }))
		if err != nil {
			t.Fatal(err)
		}
		if ch.Kind != DNSUpdate || ch.Existing.ID != 1 {
			t.Fatalf("kind = %s existing = %+v", ch.Kind, ch.Existing)
		}
	})

	t.Run("update when only the ttl differs", func(t *testing.T) {
		ch, err := PlanUpsert(existing, validInput(t, func(in *DNSRecordInput) { in.TTL = 1200 }))
		if err != nil {
			t.Fatal(err)
		}
		if ch.Kind != DNSUpdate {
			t.Fatalf("kind = %s, want update", ch.Kind)
		}
	})

	t.Run("ambiguous round robin is refused", func(t *testing.T) {
		robin := append([]DNSRecord{}, existing...)
		robin = append(robin, DNSRecord{ID: 3, Zone: "example.com", Name: "www", Type: "A", Value: "203.0.113.12", Line: "默认", Enabled: true})
		if _, err := PlanUpsert(robin, validInput(t, nil)); err == nil {
			t.Fatal("expected ambiguity to be refused")
		} else if !strings.Contains(err.Error(), "round-robin") {
			t.Fatalf("unhelpful error: %v", err)
		}
	})
}

func TestRecordFQDN(t *testing.T) {
	cases := []struct{ zone, name, want string }{
		{"example.com", "@", "example.com"},
		{"example.com", "", "example.com"},
		{"example.com", "www", "www.example.com"},
		{"example.com", "WWW", "www.example.com"},
		{"example.com", "*", "*.example.com"},
		{"example.com", "www.example.com.", "www.example.com"}, // already qualified
		{"Example.COM", "_acme-challenge", "_acme-challenge.example.com"},
	}
	for _, c := range cases {
		if got := RecordFQDN(c.zone, c.name); got != c.want {
			t.Errorf("RecordFQDN(%q,%q) = %q, want %q", c.zone, c.name, got, c.want)
		}
	}
}

func TestFilterDNSRecords(t *testing.T) {
	records := []DNSRecord{
		{ID: 1, Name: "www", Type: "A", Value: "203.0.113.10"},
		{ID: 2, Name: "www", Type: "AAAA", Value: "2001:db8::1"},
		{ID: 3, Name: "mail", Type: "MX", Value: "mx.example.net", Remark: "primary"},
	}
	if got := FilterDNSRecords(records, DNSRecordFilter{Type: "a"}); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("type filter: %+v", got)
	}
	if got := FilterDNSRecords(records, DNSRecordFilter{Name: "WWW"}); len(got) != 2 {
		t.Fatalf("name filter: %+v", got)
	}
	if got := FilterDNSRecords(records, DNSRecordFilter{Keyword: "primary"}); len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("keyword filter: %+v", got)
	}
	if got := FilterDNSRecords(records, DNSRecordFilter{}); len(got) != 3 {
		t.Fatalf("empty filter should keep everything: %+v", got)
	}
}
