package tencent

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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

// TestPolicyDocumentFollowsTheOfficialGrammar pins the shape documented at
// https://cloud.tencent.com/document/product/598/10604 : a policy is
// {version, statement} and a statement is {effect, action, resource[, condition]}.
// There is no "sid" in Tencent Cloud's grammar, and the console validates the
// JSON, so an extra key is a real risk.
func TestPolicyDocumentFollowsTheOfficialGrammar(t *testing.T) {
	policy := PolicyDocument(PermissionFilter{MaxTier: TierWrite})
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}

	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	if _, ok := generic["sid"]; ok {
		t.Fatal("a policy must not carry sid")
	}
	keys := make([]string, 0, len(generic))
	for k := range generic {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "statement" || keys[1] != "version" {
		t.Fatalf("policy keys = %v, want [statement version]", keys)
	}
	if generic["version"] != "2.0" {
		t.Fatalf("version = %v, want \"2.0\"", generic["version"])
	}

	statements, ok := generic["statement"].([]any)
	if !ok || len(statements) != 2 {
		t.Fatalf("expected a read and a write statement, got %#v", generic["statement"])
	}
	for _, rawStmt := range statements {
		stmt, ok := rawStmt.(map[string]any)
		if !ok {
			t.Fatalf("statement is not an object: %#v", rawStmt)
		}
		for k := range stmt {
			switch k {
			case "effect", "action", "resource", "condition":
			default:
				t.Fatalf("unexpected statement key %q", k)
			}
		}
		if stmt["effect"] != "allow" {
			t.Fatalf("effect = %v, want allow", stmt["effect"])
		}
		res, _ := stmt["resource"].([]any)
		if len(res) != 1 || res[0] != "*" {
			t.Fatalf("resource = %#v, want [\"*\"]", stmt["resource"])
		}
		actions, _ := stmt["action"].([]any)
		if len(actions) == 0 {
			t.Fatal("a statement with no actions is pointless")
		}
		for _, a := range actions {
			str, _ := a.(string)
			if !strings.Contains(str, ":") {
				t.Fatalf("action %q must be in service:Action form", str)
			}
		}
	}

	text, err := PolicyJSON(PermissionFilter{MaxTier: TierRead})
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	if err := json.Unmarshal([]byte(text), &round); err != nil {
		t.Fatalf("the paste-ready text must be valid JSON: %v", err)
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("the text should end with a newline so it pastes cleanly")
	}
}

func TestPolicyNameSuggestion(t *testing.T) {
	cases := map[PermissionTier]string{
		TierRead:   "orbit-readonly",
		TierWrite:  "orbit-apply",
		TierDelete: "orbit-full",
	}
	for tier, want := range cases {
		if got := PolicyNameSuggestion(PermissionFilter{MaxTier: tier}); got != want {
			t.Fatalf("name for %s = %q, want %q", tier, got, want)
		}
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
	grantable := 0
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
		// Provenance: anything that goes into a policy must cite the vendor's
		// own interface list, because the console refuses unknown actions.
		if p.NoGrant {
			if p.Source != "" {
				t.Fatalf("%s needs no grant, so it should not cite a policy source", p.Action)
			}
			continue
		}
		grantable++
		switch p.Source {
		case SourceSSL, SourceCDN, SourceTEO:
		default:
			t.Fatalf("%s has no official source; cite the CAM interface list that documents it", p.Action)
		}
	}
	if len(seen) < 10 || grantable < 8 {
		t.Fatalf("the table looks truncated: %d actions (%d grantable)", len(seen), grantable)
	}
}

// TestNoGrantActionsStayOutOfPolicies guards the exact failure an operator hit:
// the console rejected the whole document because it contained sts:GetCallerIdentity.
func TestNoGrantActionsStayOutOfPolicies(t *testing.T) {
	full := PermissionFilter{MaxTier: TierDelete, IncludeExperimental: true}
	for _, p := range SelectPermissions(full) {
		if p.NoGrant && !strings.HasPrefix(p.Action, "sts:") {
			t.Fatalf("%s is marked no-grant; is that intended?", p.Action)
		}
	}
	for _, p := range PolicyPermissions(full) {
		if p.NoGrant {
			t.Fatalf("no-grant action %s leaked into the policy", p.Action)
		}
	}

	text, err := PolicyJSON(full)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "sts:GetCallerIdentity") {
		t.Fatalf("the policy must not contain the no-grant identity action:\n%s", text)
	}
	// Everything else in the table must still be there.
	for _, p := range PolicyPermissions(full) {
		if !strings.Contains(text, p.Action) {
			t.Fatalf("%s is missing from the policy", p.Action)
		}
	}
}
