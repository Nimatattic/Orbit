// Package plain renders reports as plain text. It is the non-interactive output
// path and the fallback whenever stdout is not a terminal (plan §3.5).
package plain

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Nimatattic/Orbit/internal/core"
	"github.com/Nimatattic/Orbit/internal/i18n"
)

// Table geometry. These mirror the tabwriter configuration these tables used to
// be built with -- tabwriter.NewWriter(w, 2, 4, 2, ' ', 0) -- so switching to
// display-width alignment does not move any column by a single space.
const (
	// tableMinWidth is tabwriter's minwidth: the smallest cell width, padding
	// included.
	tableMinWidth = 4
	// tablePadding is tabwriter's padding: the gap between two columns.
	tablePadding = 2
)

// table accumulates cells and renders them aligned by display width.
//
// text/tabwriter counts runes, which misaligns every table whose non-final column
// holds CJK text: a two-character Chinese header occupies four terminal cells but
// counts as two, so the following columns drift left. Measuring cells with
// lipgloss.Width fixes the columns while keeping the previous gap.
type table struct {
	rows [][]string
}

// add appends one row of cells.
func (t *table) add(cells ...string) {
	t.rows = append(t.rows, cells)
}

// render writes the accumulated rows. Only the last cell of a row stays
// unpadded, matching tabwriter (no trailing blanks).
func (t *table) render(w io.Writer) {
	if len(t.rows) == 0 {
		return
	}
	columns := 0
	for _, row := range t.rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	widths := make([]int, columns)
	for _, row := range t.rows {
		for i, cell := range row {
			if n := lipgloss.Width(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for i, content := range widths {
		cell := content + tablePadding
		if cell < tableMinWidth {
			cell = tableMinWidth
		}
		widths[i] = cell
	}

	var b strings.Builder
	for _, row := range t.rows {
		for i, cell := range row {
			b.WriteString(cell)
			if i == len(row)-1 {
				break
			}
			if gap := widths[i] - lipgloss.Width(cell); gap > 0 {
				b.WriteString(strings.Repeat(" ", gap))
			}
		}
		b.WriteString("\n")
	}
	io.WriteString(w, b.String())
}

// Plan renders an update plan.
func Plan(w io.Writer, b *i18n.Bundle, plan core.Plan) {
	fmt.Fprintln(w, b.T("plan.title"))
	if len(plan.Changes) == 0 {
		fmt.Fprintln(w, "  "+b.T("plan.empty"))
		return
	}
	tbl := &table{}
	tbl.add("  "+b.T("plan.col.product"), b.T("plan.col.resource"), b.T("plan.col.domain"),
		b.T("plan.col.from"), b.T("plan.col.to"), b.T("plan.col.action"), b.T("plan.col.risk"))
	for _, ch := range plan.Changes {
		tbl.add("  "+string(ch.Product), ch.ResourceName, ch.Domain,
			certLabel(ch.From), certLabel(ch.To), b.T(actionKey(ch.Kind)), b.T(riskKey(ch.Risk)))
	}
	tbl.render(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  "+b.T("plan.summary",
		"total", len(plan.Changes),
		"apply", plan.Count(core.KindApply),
		"uptodate", plan.Count(core.KindUpToDate),
		"nocert", plan.Count(core.KindNoCert)))
	for _, ch := range plan.Changes {
		if len(ch.Reasons) == 0 {
			continue
		}
		fmt.Fprintf(w, "  - %s/%s: %s\n", string(ch.Product), ch.Domain, reasons(b, ch.Reasons))
	}
}

// CleanPlan renders a cleanup plan.
func CleanPlan(w io.Writer, b *i18n.Bundle, plan core.CleanPlan) {
	fmt.Fprintln(w, b.T("clean.title"))
	if len(plan.Candidates) == 0 {
		fmt.Fprintln(w, "  "+b.T("msg.nothing_to_do"))
	} else {
		fmt.Fprintln(w, "  "+b.T("clean.candidates", "count", len(plan.Candidates)))
		tbl := &table{}
		tbl.add("  ID", "Alias", "Expires", "Superseded by")
		for _, c := range plan.Candidates {
			tbl.add("  "+c.Cert.ID, c.Cert.Alias, date(c.Cert.NotAfter), c.SupersededBy)
		}
		tbl.render(w)
	}
	if len(plan.Skipped) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  "+b.T("clean.skipped", "count", len(plan.Skipped)))
		tbl := &table{}
		for _, s := range plan.Skipped {
			tbl.add("  "+s.Cert.ID, s.Cert.Alias, b.T(s.Reason))
		}
		tbl.render(w)
	}
}

func Checks(w io.Writer, b *i18n.Bundle, checks []core.Check) {
	tbl := &table{}
	for _, c := range checks {
		detail := c.Detail
		if c.MessageKey != "" {
			if detail != "" {
				detail += " "
			}
			detail += b.T(c.MessageKey)
		}
		if detail == "" && c.Err != nil {
			detail = c.Err.Error()
		}
		tbl.add("  ["+statusLabel(b, c.Status)+"]", b.T(c.Key), detail)
	}
	tbl.render(w)
}

// Results renders execution results and returns the number of failures.
func Results(w io.Writer, b *i18n.Bundle, results []core.Result) int {
	failed := 0
	tbl := &table{}
	for _, r := range results {
		state := "ok"
		if !r.OK {
			state = "failed"
			failed++
		} else if !r.Verified {
			state = "unverified"
		}
		target := ""
		if r.Change.To != nil {
			target = r.Change.To.ID
		}
		detail := ""
		if r.MessageKey != "" {
			detail = b.T(r.MessageKey)
		}
		if r.Err != nil {
			if detail != "" {
				detail += " | "
			}
			detail += r.Err.Error()
		}
		if r.ErrCode != "" {
			detail = r.ErrCode + " " + detail
		}
		tbl.add("  ["+state+"]", string(r.Change.Product), r.Change.Domain, target, detail)
	}
	tbl.render(w)
	return failed
}

func certLabel(ref *core.CertRef) string {
	if ref == nil || ref.ID == "" {
		return "-"
	}
	if ref.Source == "" && ref.NotAfter.IsZero() {
		return ref.ID
	}
	label := ref.ID
	if ref.Source != "" {
		label += "/" + ref.Source
	}
	if !ref.NotAfter.IsZero() {
		label += " " + date(ref.NotAfter)
	}
	return label
}

func reasons(b *i18n.Bundle, keys []string) string {
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += "; "
		}
		out += b.T(k)
	}
	return out
}

func actionKey(kind core.ChangeKind) string {
	switch kind {
	case core.KindApply:
		return "action.apply"
	case core.KindDelete:
		return "action.delete"
	case core.KindUpToDate:
		return "action.uptodate"
	case core.KindForeign:
		return "action.foreign"
	case core.KindNoCert:
		return "action.nocert"
	default:
		return "action.needs_manual"
	}
}

func riskKey(risk core.Risk) string {
	switch risk {
	case core.RiskHigh:
		return "risk.high"
	case core.RiskMedium:
		return "risk.medium"
	default:
		return "risk.low"
	}
}

func statusLabel(b *i18n.Bundle, s core.CheckStatus) string {
	switch s {
	case core.CheckOK:
		return b.T("doctor.ok")
	case core.CheckWarn:
		return b.T("doctor.warn")
	default:
		return b.T("doctor.fail")
	}
}

func date(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02")
}
