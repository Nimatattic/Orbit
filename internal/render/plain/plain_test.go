package plain

import (
	"bytes"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Nimatattic/Orbit/internal/core"
	"github.com/Nimatattic/Orbit/internal/i18n"
)

// tabwriterRows renders rows exactly like the previous implementation did, so
// the display-width table can be checked for byte equality on ASCII input.
func tabwriterRows(rows [][]string) string {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 2, 4, 2, ' ', 0)
	for _, row := range rows {
		tw.Write([]byte(strings.Join(row, "\t") + "\n"))
	}
	tw.Flush()
	return buf.String()
}

// TestTableMatchesTabwriterForASCII locks the ASCII layout: switching from
// rune-based to display-width alignment must not move a column for content that
// is one cell per rune (including empty and very short cells, where tabwriter's
// minwidth applies).
func TestTableMatchesTabwriterForASCII(t *testing.T) {
	rows := [][]string{
		{"  Product", "Resource", "Domain", "From", "To", "Action", "Risk"},
		{"  ssl", "x", "www.example.com", "c-old", "c-new", "apply", "low"},
		{"  cdn", "res-1", "a.example.com", "-", "c-9", "apply", "medium"},
		{"  teo", "", "very-long-resource-name", "-", "-", "delete", "high"},
		// A trailing empty cell (an execution result without a detail) must not
		// gain trailing blanks: tabwriter discards it.
		{"  [ok]", "cdn", "www.example.com", "c-new", ""},
	}
	tbl := &table{}
	for _, row := range rows {
		tbl.add(row...)
	}
	var got bytes.Buffer
	tbl.render(&got)
	if want := tabwriterRows(rows); got.String() != want {
		t.Fatalf("table layout drifted from tabwriter:\n got: %q\nwant: %q", got.String(), want)
	}
}

func TestEmptyTableWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	(&table{}).render(&buf)
	if buf.Len() != 0 {
		t.Fatalf("an empty table must not write anything, got %q", buf.String())
	}
}

// TestTableAlignsCJKColumns is the mixed Chinese/English assertion: a CJK cell is
// two terminal cells per rune, so rune padding would push the next column left.
func TestTableAlignsCJKColumns(t *testing.T) {
	tbl := &table{}
	tbl.add("  产品", "资源", "域名")
	tbl.add("  cdn", "c-old", "www.example.com")

	var got bytes.Buffer
	tbl.render(&got)

	// Column 0 is 6 cells of content (two spaces + 产品) plus the two-space gap,
	// so the ASCII row pads by three. Column 1 is 5 cells ("c-old"), the CJK cell
	// counts as 4 and pads by three as well -- a rune-counting tabwriter would
	// have padded it by one and pulled the domain column left.
	want := "" +
		"  产品  资源   域名\n" +
		"  cdn   c-old  www.example.com\n"
	if got.String() != want {
		t.Fatalf("CJK columns are misaligned:\n got: %q\nwant: %q", got.String(), want)
	}
}

// columnStart is the display column (in terminal cells) at which text begins, or
// -1 when the line does not contain it.
func columnStart(line, text string) int {
	i := strings.Index(line, text)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(line[:i])
}

func zhBundle(t *testing.T) *i18n.Bundle {
	t.Helper()
	b, err := i18n.New("zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestPlanTableKeepsColumnsForChineseHeaders renders a real plan with the Chinese
// catalog: the domain column must start at the same cell on every row even though
// the header cells are CJK and the rows are ASCII.
func TestPlanTableKeepsColumnsForChineseHeaders(t *testing.T) {
	b := zhBundle(t)
	plan := core.Plan{Changes: []core.Change{
		{
			Product: core.ProductCDN, ResourceName: "主站", Domain: "www.example.com",
			Kind: core.KindApply, Risk: core.RiskLow,
			From: &core.CertRef{ID: "c-old"}, To: &core.CertRef{ID: "c-new"},
		},
		{
			Product: core.ProductTEO, ResourceName: "api", Domain: "api.example.com",
			Kind: core.KindDelete, Risk: core.RiskHigh,
		},
	}}

	var buf bytes.Buffer
	Plan(&buf, b, plan)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")

	// The table header is the line carrying the translated column titles; the
	// plan title sits above it.
	header := ""
	for _, line := range lines {
		if strings.Contains(line, b.T("plan.col.domain")) && strings.Contains(line, b.T("plan.col.action")) {
			header = line
			break
		}
	}
	if header == "" {
		t.Fatalf("the header is missing its column titles:\n%s", buf.String())
	}
	domainHeader := columnStart(header, b.T("plan.col.domain"))
	certColumn := columnStart(header, b.T("plan.col.action"))
	for _, line := range lines {
		switch {
		case strings.Contains(line, "www.example.com"):
			if got := columnStart(line, "www.example.com"); got != domainHeader {
				t.Fatalf("domain column starts at %d, header says %d: %q", got, domainHeader, line)
			}
			if got := columnStart(line, b.T("action.apply")); got != certColumn {
				t.Fatalf("action column starts at %d, header says %d: %q", got, certColumn, line)
			}
		case strings.Contains(line, "api.example.com"):
			if got := columnStart(line, "api.example.com"); got != domainHeader {
				t.Fatalf("domain column starts at %d, header says %d: %q", got, domainHeader, line)
			}
		}
	}
}

// TestChecksTableAlignsStatusColumn is the CJK-in-first-column case: the status
// label itself is translated, and the check names are too.
func TestChecksTableAlignsStatusColumn(t *testing.T) {
	b := zhBundle(t)
	checks := []core.Check{
		{Key: "doctor.check.config", Status: core.CheckOK, Detail: "orbit.yaml"},
		{Key: "doctor.check.credentials", Status: core.CheckWarn, Detail: "not found"},
	}

	var buf bytes.Buffer
	Checks(&buf, b, checks)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected one line per check, got %q", buf.String())
	}
	nameColumn := columnStart(lines[0], b.T("doctor.check.config"))
	if nameColumn < 0 {
		t.Fatalf("the first check name is missing: %q", lines[0])
	}
	if got := columnStart(lines[1], b.T("doctor.check.credentials")); got != nameColumn {
		t.Fatalf("second check starts at %d, first at %d: %q", got, nameColumn, lines[1])
	}
	detailColumn := columnStart(lines[0], "orbit.yaml")
	if got := columnStart(lines[1], "not found"); got != detailColumn {
		t.Fatalf("detail column drifted: %d vs %d", got, detailColumn)
	}
}

// TestCleanPlanTablesAlignCJKAlias covers the two cleanup tables: the candidate
// alias is operator-supplied (often Chinese) and sits before the date columns.
func TestCleanPlanTablesAlignCJKAlias(t *testing.T) {
	b := zhBundle(t)
	notAfter := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	plan := core.CleanPlan{
		Candidates: []core.CleanCandidate{
			{Cert: core.Certificate{ID: "abc123", Alias: "主站证书", NotAfter: notAfter}, SupersededBy: "def456"},
			{Cert: core.Certificate{ID: "def456", Alias: "www", NotAfter: notAfter}},
		},
		Skipped: []core.CleanSkipped{
			{Cert: core.Certificate{ID: "c-1", Alias: "旧证书"}, Reason: "clean.reason.in_use"},
			{Cert: core.Certificate{ID: "c-2", Alias: "old"}, Reason: "clean.reason.too_young"},
		},
	}

	var buf bytes.Buffer
	CleanPlan(&buf, b, plan)
	out := buf.String()
	date := notAfter.Format("2006-01-02")

	var candidateLines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, date) {
			candidateLines = append(candidateLines, line)
		}
	}
	if len(candidateLines) != len(plan.Candidates) {
		t.Fatalf("expected %d candidate rows, got %d:\n%s", len(plan.Candidates), len(candidateLines), out)
	}
	if want := columnStart(candidateLines[0], date); columnStart(candidateLines[1], date) != want {
		t.Fatalf("date column drifted between a CJK and an ASCII alias:\n%s", out)
	}

	// The skipped table ends with the translated reason: it must start at the
	// same cell for a Chinese and an English alias.
	firstReason := b.T("clean.reason.in_use")
	secondReason := b.T("clean.reason.too_young")
	var skippedLines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, firstReason) || strings.Contains(line, secondReason) {
			skippedLines = append(skippedLines, line)
		}
	}
	if len(skippedLines) != 2 {
		t.Fatalf("expected two skipped rows, got %d:\n%s", len(skippedLines), out)
	}
	if want, got := columnStart(skippedLines[0], firstReason), columnStart(skippedLines[1], secondReason); got != want {
		t.Fatalf("reason column drifted: %d vs %d:\n%s", got, want, out)
	}
}
