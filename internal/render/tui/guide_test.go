package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/Crequency/orbit/internal/i18n"
)

func testGuideJob() GuideJob {
	return GuideJob{
		Platforms: []GuidePlatform{
			{ID: "tencent", Label: "Tencent Cloud", Available: true},
			{ID: "aliyun", Label: "Alibaba Cloud", Unavailable: "(planned)"},
		},
		Products: []GuideProduct{
			{ID: "ssl", Label: "SSL", Required: true},
			{ID: "cdn", Label: "CDN"},
			{ID: "teo", Label: "EdgeOne"},
		},
		Scopes: []GuideScope{
			{ID: "read", Label: "read only"},
			{ID: "apply", Label: "apply"},
			{ID: "full", Label: "full"},
		},
		Permissions: func(products []string, scope string, experimental bool) []GuidePermission {
			// A miniature stand-in for the real table: the certificate inventory
			// is always there, the products add their own actions, and the scope
			// controls write/delete.
			out := []GuidePermission{{Action: "ssl:DescribeCertificates", Tier: "read", Purpose: "inventory"}}
			for _, p := range products {
				if p == "cdn" {
					out = append(out, GuidePermission{Action: "cdn:DescribeDomainsConfig", Tier: "read", Purpose: "cdn read"})
					if scope != "read" {
						out = append(out, GuidePermission{Action: "cdn:ModifyDomainConfig", Tier: "write", Purpose: "cdn write"})
					}
				}
				if p == "teo" {
					out = append(out, GuidePermission{Action: "teo:DescribeZones", Tier: "read", Purpose: "teo read"})
				}
			}
			if scope == "full" {
				out = append(out, GuidePermission{Action: "ssl:DeleteCertificates", Tier: "delete", Purpose: "delete"})
			}
			return out
		},
		ServiceRole: "SSL_QCSLinkedRoleInReplaceLoadCertificate",
		PolicyHint:  "docs/cam/delete.json",
	}
}

func testGuideModel(t *testing.T) *guideModel {
	t.Helper()
	bundle, err := i18n.New("en-US")
	if err != nil {
		t.Fatal(err)
	}
	m := newGuideModel(bundle, testGuideJob(), true)
	m.width, m.height = 100, 34
	return m
}

func actionsOf(m *guideModel) []string {
	out := []string{}
	for _, p := range m.permissions() {
		out = append(out, p.Action)
	}
	return out
}

func hasAction(m *guideModel, action string) bool {
	for _, a := range actionsOf(m) {
		if a == action {
			return true
		}
	}
	return false
}

func TestGuideFocusCyclesThroughSections(t *testing.T) {
	m := testGuideModel(t)
	order := []int{guideSectionPlatform, guideSectionProducts, guideSectionScope, guideSectionList}
	for _, want := range append(order[1:], guideSectionPlatform) {
		next, _ := m.Update(key("tab"))
		m = next.(*guideModel)
		if m.focus != want {
			t.Fatalf("focus = %d, want %d", m.focus, want)
		}
	}
	next, _ := m.Update(key("shift+tab"))
	m = next.(*guideModel)
	if m.focus != guideSectionList {
		t.Fatalf("shift+tab must go back, got %d", m.focus)
	}
}

func TestGuideProductToggleAndRequiredProduct(t *testing.T) {
	m := testGuideModel(t)

	// Walk to the product section and try to disable the required one.
	next, _ := m.Update(key("tab"))
	m = next.(*guideModel)
	next, _ = m.Update(key(" ")) // ssl is required: stays on
	m = next.(*guideModel)
	if !m.products["ssl"] {
		t.Fatal("a required product must not be switchable off")
	}

	next, _ = m.Update(key("down"))
	m = next.(*guideModel)
	next, _ = m.Update(key(" "))
	m = next.(*guideModel)
	if m.products["cdn"] {
		t.Fatal("space must disable the optional product under the cursor")
	}
	if hasAction(m, "cdn:DescribeDomainsConfig") {
		t.Fatal("the CDN permission must disappear once CDN is out of scope")
	}

	// The cursor stops at the last product.
	next, _ = m.Update(key("down"))
	m = next.(*guideModel)
	next, _ = m.Update(key("down"))
	m = next.(*guideModel)
	if m.productCursor != len(m.job.Products)-1 {
		t.Fatalf("cursor = %d, want the last product", m.productCursor)
	}
}

func TestGuideScopeControlsAuthority(t *testing.T) {
	m := testGuideModel(t)

	// Default scope is the first entry: read only.
	if hasAction(m, "cdn:ModifyDomainConfig") || hasAction(m, "ssl:DeleteCertificates") {
		t.Fatalf("read scope must not include write or delete: %v", actionsOf(m))
	}

	next, _ := m.Update(key("tab"))
	m = next.(*guideModel)
	next, _ = m.Update(key("tab")) // products -> scope
	m = next.(*guideModel)
	if m.focus != guideSectionScope {
		t.Fatalf("focus = %d, want scope", m.focus)
	}

	next, _ = m.Update(key("down"))
	m = next.(*guideModel)
	if m.scope != 1 {
		t.Fatalf("scope = %d, want 1", m.scope)
	}
	if !hasAction(m, "cdn:ModifyDomainConfig") {
		t.Fatalf("apply scope needs the write action: %v", actionsOf(m))
	}
	if hasAction(m, "ssl:DeleteCertificates") {
		t.Fatal("apply scope must not include deletion")
	}

	next, _ = m.Update(key("down"))
	m = next.(*guideModel)
	if !hasAction(m, "ssl:DeleteCertificates") {
		t.Fatalf("full scope needs deletion: %v", actionsOf(m))
	}
	// The linked role only becomes relevant once write authority is granted.
	if !m.requiresRole() {
		t.Fatal("write/delete scope requires the service role note")
	}
	if out := m.View(); !strings.Contains(out, "SSL_QCSLinkedRoleInReplaceLoadCertificate") {
		t.Fatalf("the role must be shown:\n%s", out)
	}
}

func TestGuideReadScopeHidesRoleNote(t *testing.T) {
	m := testGuideModel(t)
	if m.requiresRole() {
		t.Fatal("read-only scope needs no service role")
	}
	if out := m.View(); strings.Contains(out, "SSL_QCSLinkedRoleInReplaceLoadCertificate") {
		t.Fatal("read-only must not advertise the role requirement")
	}
}

func TestGuideListViewScrolls(t *testing.T) {
	bundle, _ := i18n.New("en-US")
	job := testGuideJob()
	job.Permissions = func([]string, string, bool) []GuidePermission {
		out := make([]GuidePermission, 40)
		for i := range out {
			out[i] = GuidePermission{Action: "x:Action", Tier: "read", Purpose: "p"}
		}
		return out
	}
	m := newGuideModel(bundle, job, true)
	m.width, m.height = 100, 20
	m.focus = guideSectionList

	next, _ := m.Update(key("down"))
	m = next.(*guideModel)
	if m.scroll != 1 {
		t.Fatalf("scroll = %d, want 1", m.scroll)
	}
	// Scrolling stops at the last page.
	for i := 0; i < 100; i++ {
		next, _ = m.Update(key("down"))
		m = next.(*guideModel)
	}
	if want := len(m.permissions()) - m.listRows(); m.scroll != want {
		t.Fatalf("scroll = %d, want the last page %d", m.scroll, want)
	}
}

func TestGuideViewRendersAllSections(t *testing.T) {
	m := testGuideModel(t)
	out := m.View()
	for _, want := range []string{
		"Account permission guide", "1. Cloud vendor", "2. Product scope", "3. Usage level",
		"Tencent Cloud", "SSL", "read only", "permissions needed", "ssl:DescribeCertificates",
		"tab",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("view is missing %q:\n%s", want, out)
		}
	}
	// The unavailable platform is shown but marked.
	if !strings.Contains(out, "Alibaba Cloud") || !strings.Contains(out, "(planned)") {
		t.Fatalf("the planned platform should be listed with a note:\n%s", out)
	}
}

func TestGuidePolicyPanelTogglesAndShowsJSON(t *testing.T) {
	job := testGuideJob()
	job.PolicyJSON = func([]string, string, bool) string {
		return "{\n  \"version\": \"2.0\",\n  \"statement\": []\n}\n"
	}
	job.PolicyName = func([]string, string) string { return "orbit-readonly" }
	job.PolicySteps = []string{"1. open the console", "2. paste the JSON"}

	bundle, _ := i18n.New("en-US")
	m := newGuideModel(bundle, job, true)
	m.width, m.height = 100, 34

	if strings.Contains(m.View(), "Custom policy JSON") {
		t.Fatal("the policy panel starts hidden")
	}

	// `p` reveals it and moves focus there so the arrows scroll it.
	next, _ := m.Update(key("p"))
	m = next.(*guideModel)
	if !m.showPolicy || m.focus != guideSectionPolicy {
		t.Fatalf("p must show the panel and focus it, show=%v focus=%d", m.showPolicy, m.focus)
	}
	out := m.View()
	for _, want := range []string{"Custom policy JSON", "orbit-readonly", "\"version\": \"2.0\"", "1. open the console", "how to paste"} {
		if !strings.Contains(out, want) {
			t.Fatalf("policy view is missing %q:\n%s", want, out)
		}
	}
	// The configuration panels are replaced by the policy view, not squeezed.
	if strings.Contains(out, "2. Product scope") {
		t.Fatalf("the policy view should own the screen:\n%s", out)
	}

	// Toggling it off returns to the configuration view.
	next, _ = m.Update(key("p"))
	m = next.(*guideModel)
	if m.showPolicy || m.focus != guideSectionList {
		t.Fatalf("p must leave the policy view, show=%v focus=%d", m.showPolicy, m.focus)
	}

	// Esc also returns to the guide rather than quitting.
	next, _ = m.Update(key("p"))
	m = next.(*guideModel)
	next, cmd := m.Update(key("esc"))
	m = next.(*guideModel)
	if m.showPolicy || cmd != nil {
		t.Fatalf("esc should return to the guide without quitting, show=%v cmd=%v", m.showPolicy, cmd != nil)
	}
}

func TestGuidePolicyPanelScrollsAndWraps(t *testing.T) {
	var long strings.Builder
	for i := 0; i < 60; i++ {
		long.WriteString("  \"ssl:DescribeCertificateBindResourceTaskResultWithALongName\",\n")
	}
	job := testGuideJob()
	job.PolicyJSON = func([]string, string, bool) string { return long.String() }

	bundle, _ := i18n.New("en-US")
	m := newGuideModel(bundle, job, true)
	m.width, m.height = 80, 30
	next, _ := m.Update(key("p"))
	m = next.(*guideModel)

	// Everything must fit the panel width, even mid-token.
	for _, line := range m.policyLines() {
		if w := lipgloss.Width(line); w > m.windowWidth()-4 {
			t.Fatalf("policy line is too wide (%d): %q", w, line)
		}
	}

	next, _ = m.Update(key("down"))
	m = next.(*guideModel)
	if m.policyScroll != 1 {
		t.Fatalf("policy scroll = %d, want 1", m.policyScroll)
	}
	for i := 0; i < 200; i++ {
		next, _ = m.Update(key("down"))
		m = next.(*guideModel)
	}
	if want := len(m.policyLines()) - m.policyRows(); m.policyScroll != want {
		t.Fatalf("scroll = %d, want the last page %d", m.policyScroll, want)
	}
}

func TestGuideInitialSelectionFromFlags(t *testing.T) {
	job := testGuideJob()
	job.InitialProducts = []string{"cdn"} // ssl is required and must stay on
	job.InitialScope = "apply"

	bundle, _ := i18n.New("en-US")
	m := newGuideModel(bundle, job, true)
	if !m.products["ssl"] {
		t.Fatal("a required product must stay enabled even when not listed")
	}
	if !m.products["cdn"] {
		t.Fatalf("the requested product must start enabled: %v", m.products)
	}
	if m.products["teo"] {
		t.Fatalf("unrequested products must stay off: %v", m.products)
	}
	if m.scope != 1 {
		t.Fatalf("scope = %d, want the apply level", m.scope)
	}
}
