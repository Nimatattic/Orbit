package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Crequency/orbit/internal/cloud/tencent"
	"github.com/Crequency/orbit/internal/exitcode"
	"github.com/Crequency/orbit/internal/render/tui"
)

// guideScopes are the usage levels the guide offers, least authority first. The
// ids are also the --scope flag values.
var guideScopes = []struct{ ID, LabelKey, NoteKey string }{
	{"read", "guide.scope.read", "guide.scope.read_note"},
	{"apply", "guide.scope.apply", "guide.scope.apply_note"},
	{"full", "guide.scope.full", "guide.scope.full_note"},
}

// guideProducts are the product scopes, certificate inventory first.
var guideProducts = []struct {
	ID, LabelKey, Scope string
	Required            bool
}{
	{"ssl", "guide.product.ssl", tencent.ScopeSSL, true},
	{"cdn", "guide.product.cdn", tencent.ScopeCDN, false},
	{"teo", "guide.product.teo", tencent.ScopeTEO, false},
}

// newAccountGuideCmd builds `orbit account guide`.
func (a *App) newAccountGuideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guide",
		Short: a.Bundle.T("cmd.account.guide.short"),
		Long:  a.Bundle.T("cmd.account.guide.long"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runGuide(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&a.Products, "products", "", "")
	cmd.Flags().StringVar(&a.GuideScope, "scope", "full", "")
	cmd.Flags().BoolVar(&a.GuidePolicy, "policy", false, "")
	return cmd
}

// runGuide shows which permissions an account needs for the chosen platform,
// product scope and usage level.
func (a *App) runGuide(ctx context.Context) error {
	if a.Interactive && !a.NoTUI && !a.JSON && !a.GuidePolicy {
		return tui.RunGuide(ctx, a.Bundle, a.guideJob())
	}
	return a.renderGuide()
}

// guideJob describes the interactive guide.
func (a *App) guideJob() tui.GuideJob {
	products := make([]tui.GuideProduct, 0, len(guideProducts))
	for _, p := range guideProducts {
		products = append(products, tui.GuideProduct{
			ID: p.ID, Label: a.Bundle.T(p.LabelKey), Required: p.Required,
		})
	}
	scopes := make([]tui.GuideScope, 0, len(guideScopes))
	for _, s := range guideScopes {
		scopes = append(scopes, tui.GuideScope{
			ID: s.ID, Label: a.Bundle.T(s.LabelKey), Note: a.Bundle.T(s.NoteKey),
		})
	}

	return tui.GuideJob{
		Platforms: []tui.GuidePlatform{
			{ID: DefaultPlatform, Label: a.Bundle.T("guide.platform.tencent"), Available: true},
			{ID: "aliyun", Label: a.Bundle.T("guide.platform.aliyun"), Available: false,
				Unavailable: a.Bundle.T("guide.platform.planned")},
		},
		Products: products,
		Scopes:   scopes,
		Permissions: func(productIDs []string, scopeID string, experimental bool) []tui.GuidePermission {
			items := a.selectedPermissions(productIDs, scopeID, experimental)
			out := make([]tui.GuidePermission, 0, len(items))
			for _, p := range items {
				out = append(out, tui.GuidePermission{
					Action:       p.Action,
					Tier:         string(p.Tier),
					Purpose:      a.Bundle.T(p.PurposeKey),
					Experimental: p.Experimental,
				})
			}
			return out
		},
		ServiceRole: tencent.ServiceRole,
		PolicyHint:  a.Bundle.T("guide.policy_hint"),
	}
}

// renderGuide prints the same information as text, for scripts and pipes.
func (a *App) renderGuide() error {
	if err := a.validateGuideScope(); err != nil {
		return err
	}
	productIDs := a.guideProductsFromFlag()
	scope := a.normalisedGuideScope()
	items := a.selectedPermissions(productIDs, scope, a.ExpWrite)

	if a.GuidePolicy {
		return a.jsonOut(tencent.PolicyDocument(tencent.PermissionFilter{
			Scopes:              a.scopeIDs(productIDs),
			MaxTier:             a.guideMaxTier(scope),
			IncludeExperimental: a.ExpWrite,
		}))
	}
	if a.JSON {
		payload := map[string]any{
			"platform":     DefaultPlatform,
			"products":     productIDs,
			"scope":        scope,
			"service_role": tencent.ServiceRole,
			"permissions": func() []map[string]any {
				out := make([]map[string]any, 0, len(items))
				for _, p := range items {
					out = append(out, map[string]any{
						"action":       p.Action,
						"tier":         string(p.Tier),
						"purpose":      a.Bundle.T(p.PurposeKey),
						"experimental": p.Experimental,
					})
				}
				return out
			}(),
		}
		return a.jsonOut(payload)
	}

	fmt.Fprintf(a.Out, "%s: %s\n", a.Bundle.T("guide.platform"), a.Bundle.T("guide.platform.tencent"))
	fmt.Fprintf(a.Out, "%s: %s\n", a.Bundle.T("guide.products"), strings.Join(a.productLabels(productIDs), ", "))
	fmt.Fprintf(a.Out, "%s: %s\n", a.Bundle.T("guide.scope"), a.Bundle.T(a.scopeLabelKey(scope)))
	fmt.Fprintln(a.Out)
	fmt.Fprintln(a.Out, a.Bundle.T("guide.permissions_count", "count", len(items)))
	for _, p := range items {
		line := fmt.Sprintf("  %-7s %-44s %s", p.Tier, p.Action, a.Bundle.T(p.PurposeKey))
		if p.Experimental {
			line += " (" + a.Bundle.T("guide.experimental") + ")"
		}
		fmt.Fprintln(a.Out, line)
	}
	if a.guideMaxTier(scope) != tencent.TierRead {
		fmt.Fprintln(a.Out)
		fmt.Fprintf(a.Out, "%s: %s\n", a.Bundle.T("guide.extra_requirements"), tencent.ServiceRole)
	}
	fmt.Fprintln(a.Out)
	fmt.Fprintln(a.Out, a.Bundle.T("guide.policy_hint"))
	return nil
}

// selectedPermissions resolves the table for one selection.
func (a *App) selectedPermissions(productIDs []string, scopeID string, experimental bool) []tencent.Permission {
	return tencent.SelectPermissions(tencent.PermissionFilter{
		Scopes:              a.scopeIDs(productIDs),
		MaxTier:             a.guideMaxTier(scopeID),
		IncludeExperimental: experimental,
	})
}

// scopeIDs maps the guide's product ids onto the tencent package's scope ids.
func (a *App) scopeIDs(productIDs []string) []string {
	out := make([]string, 0, len(productIDs))
	for _, id := range productIDs {
		for _, p := range guideProducts {
			if p.ID == id {
				out = append(out, p.Scope)
			}
		}
	}
	return out
}

func (a *App) guideMaxTier(scopeID string) tencent.PermissionTier {
	switch scopeID {
	case "read":
		return tencent.TierRead
	case "apply":
		return tencent.TierWrite
	default:
		return tencent.TierDelete
	}
}

func (a *App) normalisedGuideScope() string {
	scope := strings.ToLower(strings.TrimSpace(a.GuideScope))
	for _, s := range guideScopes {
		if s.ID == scope {
			return scope
		}
	}
	return "full"
}

func (a *App) scopeLabelKey(scopeID string) string {
	for _, s := range guideScopes {
		if s.ID == scopeID {
			return s.LabelKey
		}
	}
	return "guide.scope.full"
}

// guideProductsFromFlag turns --products into guide product ids, defaulting to
// every product so the printed list is complete.
func (a *App) guideProductsFromFlag() []string {
	if v := strings.TrimSpace(a.Products); v != "" {
		wanted := map[string]bool{}
		for _, id := range splitList(v) {
			wanted[strings.ToLower(id)] = true
		}
		out := []string{}
		for _, p := range guideProducts {
			if wanted[p.ID] || (p.Required && wanted[p.ID]) {
				out = append(out, p.ID)
			}
		}
		// The certificate inventory is never optional.
		hasSSL := false
		for _, id := range out {
			if id == "ssl" {
				hasSSL = true
			}
		}
		if !hasSSL {
			out = append([]string{"ssl"}, out...)
		}
		if len(out) > 0 {
			return out
		}
	}
	out := make([]string, 0, len(guideProducts))
	for _, p := range guideProducts {
		out = append(out, p.ID)
	}
	return out
}

func (a *App) productLabels(ids []string) []string {
	labels := make([]string, 0, len(ids))
	for _, p := range guideProducts {
		for _, id := range ids {
			if p.ID == id {
				labels = append(labels, a.Bundle.T(p.LabelKey))
			}
		}
	}
	return labels
}

// validateGuideScope rejects a typo instead of silently falling back to "full",
// which would over-grant when someone meant to ask for read-only.
func (a *App) validateGuideScope() error {
	scope := strings.ToLower(strings.TrimSpace(a.GuideScope))
	if scope == "" {
		return nil
	}
	for _, s := range guideScopes {
		if s.ID == scope {
			return nil
		}
	}
	values := make([]string, 0, len(guideScopes))
	for _, s := range guideScopes {
		values = append(values, s.ID)
	}
	return exitcode.New(exitcode.Fatal, "err.guide_scope", "values", strings.Join(values, ", "))
}
