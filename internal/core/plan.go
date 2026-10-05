package core

import (
	"strings"
	"time"
)

// Reason message keys emitted by the planner. Presentation layers translate
// them; the engine never builds prose.
const (
	ReasonNewerCert       = "core.reason.newer_cert_available"
	ReasonFirstBind       = "core.reason.first_bind"
	ReasonCurrentExpired  = "core.reason.current_cert_expired"
	ReasonNoCert          = "core.reason.no_cert"
	ReasonEdgeOneFreeKept = "core.reason.edgeone_free_cert_kept"
	ReasonExperimental    = "core.reason.experimental_blocked"
)

// PlanOptions configures BuildUpdatePlan.
type PlanOptions struct {
	Account string
	Now     time.Time
	// Products limits which products participate (lower-case names).
	Products []string
	// Domains, when non-empty, limits the plan to these domains.
	Domains []string
	// Match tunes certificate selection.
	Match MatchOptions
	// EdgeOneReplaceFreeCert controls whether EdgeOne domains currently using a
	// free certificate are switched to the best pool certificate (decision B2b).
	EdgeOneReplaceFreeCert bool
	// ExperimentalWrite allows otherwise gated strategies.
	ExperimentalWrite bool
	// Strategy is auto|deploy|product|replace; empty means auto.
	Strategy string
}

// ChooseStrategy maps the configured strategy plus the product to a concrete
// execution path (plan §2.2).
func ChooseStrategy(configured string, _ Product) Strategy {
	switch strings.ToLower(strings.TrimSpace(configured)) {
	case "deploy":
		return StrategyDeploy
	case "product":
		return StrategyProduct
	case "replace":
		return StrategyReplace
	default:
		// auto: use each product's native API, which is the most expressive and
		// the only path that can set EdgeOne's Mode explicitly.
		return StrategyProduct
	}
}

// BuildUpdatePlan computes, for every resource, whether its certificate should
// change. It is a pure function: the same inputs always yield the same plan,
// which is what makes `update` idempotent (plan §2.1).
func BuildUpdatePlan(resources []Resource, certs []Certificate, opts PlanOptions) Plan {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	allowed := map[string]bool{}
	for _, p := range opts.Products {
		allowed[strings.ToLower(strings.TrimSpace(p))] = true
	}
	filter := map[string]bool{}
	for _, d := range opts.Domains {
		if n := NormalizeName(d); n != "" {
			filter[n] = true
		}
		if n := NormalizeName(strings.TrimPrefix(d, "*.")); n != "" {
			filter[n] = true
		}
	}

	// Resolving bound ids against the pool lets the plan show the source of the
	// certificate currently in place, not just its id.
	pool := make(map[string]Certificate, len(certs))
	for _, c := range certs {
		pool[c.ID] = c
	}

	plan := Plan{Account: opts.Account, GeneratedAt: now}
	for _, res := range resources {
		if len(allowed) > 0 && !allowed[string(res.Product)] {
			continue
		}
		domain := NormalizeName(res.Domain)
		if domain == "" {
			continue
		}
		if len(filter) > 0 && !filter[domain] {
			continue
		}

		change := Change{
			Account:      opts.Account,
			Product:      res.Product,
			ZoneID:       res.ZoneID,
			ResourceID:   res.ID,
			ResourceName: res.Name,
			Domain:       domain,
			InstanceKey:  res.InstanceKey,
			From:         boundRef(res, pool),
		}

		best, ok := BestFor(domain, certs, now, opts.Match)
		if !ok {
			change.Kind = KindNoCert
			change.Reasons = []string{ReasonNoCert}
			plan.Changes = append(plan.Changes, change)
			continue
		}
		change.To = Ref(best)

		if res.BoundCert != "" && res.BoundCert == best.ID {
			change.Kind = KindUpToDate
			plan.Changes = append(plan.Changes, change)
			continue
		}

		if res.Product == ProductTEO && strings.HasPrefix(res.BoundMode, "eofreecert") && !opts.EdgeOneReplaceFreeCert {
			change.Kind = KindForeign
			change.Reasons = []string{ReasonEdgeOneFreeKept}
			plan.Changes = append(plan.Changes, change)
			continue
		}

		change.Strategy = ChooseStrategy(opts.Strategy, res.Product)
		if change.Strategy == StrategyReplace && !opts.ExperimentalWrite {
			change.Kind = KindBlocked
			change.Reasons = []string{ReasonExperimental}
			plan.Changes = append(plan.Changes, change)
			continue
		}

		change.Kind = KindApply
		change.Risk = riskFor(res, now)
		change.Reasons = reasonsFor(res, now)
		plan.Changes = append(plan.Changes, change)
	}
	sortChanges(plan.Changes)
	return plan
}

func boundRef(res Resource, pool map[string]Certificate) *CertRef {
	if res.BoundCert == "" {
		return nil
	}
	if c, ok := pool[res.BoundCert]; ok {
		return Ref(c)
	}
	// Not in the pool: an EdgeOne free certificate, or one issued elsewhere.
	return &CertRef{ID: res.BoundCert, NotAfter: res.BoundNotAfter}
}

func riskFor(res Resource, now time.Time) Risk {
	switch {
	case res.BoundCert == "":
		return RiskLow
	case !res.BoundNotAfter.IsZero() && now.After(res.BoundNotAfter):
		return RiskHigh
	default:
		return RiskMedium
	}
}

func reasonsFor(res Resource, now time.Time) []string {
	out := []string{ReasonNewerCert}
	if res.BoundCert == "" {
		out = []string{ReasonFirstBind}
	}
	if !res.BoundNotAfter.IsZero() && now.After(res.BoundNotAfter) {
		out = append(out, ReasonCurrentExpired)
	}
	return out
}
