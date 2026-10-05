// Package plain renders reports as plain text. It is the non-interactive output
// path and the fallback whenever stdout is not a terminal (plan §3.5).
package plain

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/i18n"
)

// Plan renders an update plan.
func Plan(w io.Writer, b *i18n.Bundle, plan core.Plan) {
	fmt.Fprintln(w, b.T("plan.title"))
	if len(plan.Changes) == 0 {
		fmt.Fprintln(w, "  "+b.T("plan.empty"))
		return
	}
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		b.T("plan.col.product"), b.T("plan.col.resource"), b.T("plan.col.domain"),
		b.T("plan.col.from"), b.T("plan.col.to"), b.T("plan.col.action"), b.T("plan.col.risk"))
	for _, ch := range plan.Changes {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			string(ch.Product), ch.ResourceName, ch.Domain,
			certLabel(ch.From), certLabel(ch.To), b.T(actionKey(ch.Kind)), b.T(riskKey(ch.Risk)))
	}
	tw.Flush()
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
		tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", "ID", "Alias", "Expires", "Superseded by")
		for _, c := range plan.Candidates {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", c.Cert.ID, c.Cert.Alias, date(c.Cert.NotAfter), c.SupersededBy)
		}
		tw.Flush()
	}
	if len(plan.Skipped) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  "+b.T("clean.skipped", "count", len(plan.Skipped)))
		tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
		for _, s := range plan.Skipped {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", s.Cert.ID, s.Cert.Alias, b.T(s.Reason))
		}
		tw.Flush()
	}
}

func Checks(w io.Writer, b *i18n.Bundle, checks []core.Check) {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
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
		fmt.Fprintf(tw, "  [%s]\t%s\t%s\n", statusLabel(b, c.Status), b.T(c.Key), detail)
	}
	tw.Flush()
}

// Results renders execution results and returns the number of failures.
func Results(w io.Writer, b *i18n.Bundle, results []core.Result) int {
	failed := 0
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
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
		fmt.Fprintf(tw, "  [%s]\t%s\t%s\t%s\t%s\n", state, string(r.Change.Product), r.Change.Domain, target, detail)
	}
	tw.Flush()
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
	case core.KindBlocked:
		return "action.blocked"
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
