package tencent

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var updatePolicies = flag.Bool("update", false, "rewrite docs/cam/*.json from the permission table")

// TestPermissionTableCoversEverySDKCall is the anti-drift guard: the provider's
// SDK interfaces list every API the code can call, and their method names are the
// CAM action names. Adding a call without documenting its permission fails here.
func TestPermissionTableCoversEverySDKCall(t *testing.T) {
	known := map[string]Permission{}
	for _, p := range Permissions() {
		known[p.Action] = p
	}

	cases := []struct {
		service string
		iface   any
	}{
		{"ssl", (*sslAPI)(nil)},
		{"cdn", (*cdnAPI)(nil)},
		{"teo", (*teoAPI)(nil)},
		{"sts", (*stsAPI)(nil)},
	}
	for _, tc := range cases {
		typ := reflect.TypeOf(tc.iface).Elem()
		for i := 0; i < typ.NumMethod(); i++ {
			action := tc.service + ":" + typ.Method(i).Name
			if _, ok := known[action]; !ok {
				t.Errorf("provider calls %s but the permission table does not document it", action)
			}
		}
	}
}

func TestSelectPermissionsFiltersByTierAndScope(t *testing.T) {
	// Read-only, all products: no write or delete authority.
	readOnly := SelectPermissions(PermissionFilter{Scopes: []string{ScopeSSL, ScopeCDN, ScopeTEO}, MaxTier: TierRead})
	for _, p := range readOnly {
		if p.Tier != TierRead {
			t.Fatalf("read-only selection leaked %s (%s)", p.Action, p.Tier)
		}
		if p.Experimental {
			t.Fatalf("experimental action %s must be opt-in", p.Action)
		}
	}
	contains := func(list []Permission, action string) bool {
		for _, p := range list {
			if p.Action == action {
				return true
			}
		}
		return false
	}
	if !contains(readOnly, "ssl:DescribeCertificates") {
		t.Fatal("the certificate inventory is always needed")
	}
	if contains(readOnly, "cdn:ModifyDomainConfig") {
		t.Fatal("read-only must not include a write action")
	}
	if contains(readOnly, "ssl:DeleteCertificates") {
		t.Fatal("read-only must not include deletion")
	}

	// Dropping EdgeOne removes its actions but keeps SSL and CDN.
	noTEO := SelectPermissions(PermissionFilter{Scopes: []string{ScopeSSL, ScopeCDN}})
	for _, p := range noTEO {
		if len(p.Scopes) == 1 && p.Scopes[0] == ScopeTEO {
			t.Fatalf("EdgeOne action %s must be gone", p.Action)
		}
	}
	if !contains(noTEO, "cdn:ModifyDomainConfig") {
		t.Fatal("the CDN write action should still be there")
	}
	if contains(noTEO, "teo:DescribeZones") {
		t.Fatal("teo:DescribeZones must be gone when EdgeOne is out of scope")
	}

	// Full scope includes deletion and stays free of experimental actions.
	full := SelectPermissions(PermissionFilter{MaxTier: TierDelete})
	if !contains(full, "ssl:DeleteCertificates") {
		t.Fatal("full scope needs the delete action")
	}
	for _, p := range full {
		if p.Experimental {
			t.Fatalf("experimental action %s must not be in the default set", p.Action)
		}
	}
	withExperimental := SelectPermissions(PermissionFilter{MaxTier: TierDelete, IncludeExperimental: true})
	if !contains(withExperimental, "ssl:DeployCertificateInstance") {
		t.Fatal("the deploy strategy actions should appear when opted in")
	}

	// Results are stable and ordered by tier.
	last := 0
	for _, p := range full {
		if lvl := TierLevel(p.Tier); lvl < last {
			t.Fatalf("permissions must be ordered by tier, got %s after level %d", p.Action, last)
		} else {
			last = lvl
		}
	}
}

func TestPolicyDocumentGroupsByTier(t *testing.T) {
	policy := PolicyDocument(PermissionFilter{MaxTier: TierWrite})
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Version   string `json:"version"`
		Statement []struct {
			SID      string   `json:"sid"`
			Effect   string   `json:"effect"`
			Action   []string `json:"action"`
			Resource []string `json:"resource"`
		} `json:"statement"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Version != "2.0" {
		t.Fatalf("version = %q", parsed.Version)
	}
	if len(parsed.Statement) != 2 {
		t.Fatalf("expected a read and a write statement, got %d", len(parsed.Statement))
	}
	for _, s := range parsed.Statement {
		if s.Effect != "allow" || len(s.Action) == 0 || len(s.Resource) != 1 || s.Resource[0] != "*" {
			t.Fatalf("statement is malformed: %+v", s)
		}
	}
	if len(parsed.Statement[0].Action) == 0 {
		t.Fatal("the read statement must carry the inventory actions")
	}
}

// TestPolicyFilesMatchPermissions keeps the paste-ready files in docs/cam in sync
// with the table. Run `go test ./internal/cloud/tencent/ -run TestPolicyFilesMatch -update`
// to regenerate them.
func TestPolicyFilesMatchPermissions(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs", "cam")
	cases := []struct {
		file  string
		tier  PermissionTier
		label string
	}{
		{"readonly.json", TierRead, "inspection only"},
		{"execute.json", TierWrite, "apply certificates"},
		{"delete.json", TierDelete, "apply and clean"},
	}
	for _, tc := range cases {
		want, err := json.MarshalIndent(PolicyDocument(PermissionFilter{MaxTier: tc.tier}), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, '\n')
		path := filepath.Join(dir, tc.file)

		if *updatePolicies {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s (%s)", path, tc.label)
			continue
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s is missing; regenerate with -update: %v", path, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s is out of date (%s); regenerate with:\n  go test ./internal/cloud/tencent/ -run TestPolicyFilesMatch -update", path, tc.label)
		}
	}
}

func TestPermissionTableEntriesAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Permissions() {
		if seen[p.Action] {
			t.Fatalf("duplicate action %s", p.Action)
		}
		seen[p.Action] = true
		if p.PurposeKey == "" {
			t.Fatalf("%s has no purpose key: the guide would show a raw key", p.Action)
		}
		if TierLevel(p.Tier) == 0 {
			t.Fatalf("%s has no tier", p.Action)
		}
	}
	if len(seen) < 10 {
		t.Fatalf("the table looks truncated: %d actions", len(seen))
	}
}
