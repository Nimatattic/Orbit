package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// guidePolicyFile is the default file name offered by the save prompt.
const guidePolicyFile = "orbit-account-policy.json"

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
	// An empty default keeps "flag not set" distinguishable: the console then
	// starts at read-only while the text mode prints the complete list.
	cmd.Flags().StringVar(&a.GuideScope, "scope", "", "")
	cmd.Flags().BoolVar(&a.GuidePolicy, "policy", false, "")
	cmd.Flags().StringVar(&a.GuidePolicyOut, "policy-out", "", "")
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

	// Flags pre-select the console; when unset the guide starts from every
	// product and the safest level (read-only).
	initialProducts := a.guideInitialProducts()
	initialScope := a.guideInitialScope()

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
					NoGrant:      p.NoGrant,
				})
			}
			return out
		},
		ServiceRole: tencent.ServiceRole,
		PolicyHint:  a.Bundle.T("guide.policy_hint"),
		PolicyJSON: func(productIDs []string, scopeID string, experimental bool) string {
			text, err := tencent.PolicyJSON(a.policyFilter(productIDs, scopeID, experimental))
			if err != nil {
				return ""
			}
			return text
		},
		PolicyName: func(productIDs []string, scopeID string) string {
			return tencent.PolicyNameSuggestion(a.policyFilter(productIDs, scopeID, a.ExpWrite))
		},
		PolicySteps:     a.policySteps(),
		PolicySources:   a.policySources(),
		SaveDefault:     "./" + guidePolicyFile,
		InitialProducts: initialProducts,
		InitialScope:    initialScope,
	}
}

// guideInitialProducts returns the product ids passed on the command line, or nil
// when the flag was not used (meaning "everything").
func (a *App) guideInitialProducts() []string {
	if strings.TrimSpace(a.Products) == "" {
		return nil
	}
	return a.guideProductsFromFlag()
}

// guideInitialScope returns the --scope value, or "" when it was not set so the
// console can start at the safest level.
func (a *App) guideInitialScope() string {
	scope := strings.ToLower(strings.TrimSpace(a.GuideScope))
	for _, s := range guideScopes {
		if s.ID == scope {
			return scope
		}
	}
	return ""
}

// policyFilter builds the tencent filter for one selection.
func (a *App) policyFilter(productIDs []string, scopeID string, experimental bool) tencent.PermissionFilter {
	return tencent.PermissionFilter{
		Scopes:              a.scopeIDs(productIDs),
		MaxTier:             a.guideMaxTier(scopeID),
		IncludeExperimental: experimental,
	}
}

// policySources lists the official CAM interface lists behind the actions, so an
// operator can verify the names instead of trusting the tool.
func (a *App) policySources() []string {
	return []string{
		"ssl  " + tencent.SourceSSL,
		"cdn  " + tencent.SourceCDN,
		"teo  " + tencent.SourceTEO,
	}
}

// policySteps describes the console path, localised.
func (a *App) policySteps() []string {
	return []string{
		a.Bundle.T("guide.policy_step1"),
		a.Bundle.T("guide.policy_step2"),
		a.Bundle.T("guide.policy_step3"),
		a.Bundle.T("guide.policy_step4"),
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

	if a.GuidePolicy || a.GuidePolicyOut != "" {
		text, err := tencent.PolicyJSON(a.policyFilter(productIDs, scope, a.ExpWrite))
		if err != nil {
			return err
		}
		if a.GuidePolicyOut != "" {
			if err := os.WriteFile(a.GuidePolicyOut, []byte(text), 0o600); err != nil {
				return exitcode.Wrap(err, exitcode.Fatal, "err.generic", "error", err)
			}
			fmt.Fprintln(a.Err, a.Bundle.T("guide.policy_written", "path", a.GuidePolicyOut))
		}
		if a.GuidePolicy {
			// Keep stdout pure JSON so it can be piped into a file.
			fmt.Fprint(a.Out, text)
			for _, step := range a.policySteps() {
				fmt.Fprintln(a.Err, step)
			}
		}
		if !a.GuidePolicy {
			return nil
		}
		return nil
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
		if text, err := tencent.PolicyJSON(a.policyFilter(productIDs, scope, a.ExpWrite)); err == nil {
			payload["policy"] = json.RawMessage(text)
			payload["policy_name"] = tencent.PolicyNameSuggestion(a.policyFilter(productIDs, scope, a.ExpWrite))
			payload["policy_sources"] = map[string]string{
				"ssl": tencent.SourceSSL, "cdn": tencent.SourceCDN, "teo": tencent.SourceTEO,
			}
			noGrant := []string{}
			for _, p := range a.selectedPermissions(productIDs, scope, a.ExpWrite) {
				if p.NoGrant {
					noGrant = append(noGrant, p.Action)
				}
			}
			payload["no_grant_needed"] = noGrant
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
		if p.NoGrant {
			line += " (" + a.Bundle.T("guide.no_grant") + ")"
		}
		if p.Experimental {
			line += " (" + a.Bundle.T("guide.experimental") + ")"
		}
		fmt.Fprintln(a.Out, line)
	}
	fmt.Fprintln(a.Out)
	fmt.Fprintln(a.Out, a.Bundle.T("guide.policy_sources"))
	for _, src := range a.policySources() {
		fmt.Fprintln(a.Out, "  "+src)
	}
	if a.guideMaxTier(scope) != tencent.TierRead {
		fmt.Fprintln(a.Out)
		fmt.Fprintf(a.Out, "%s: %s\n", a.Bundle.T("guide.extra_requirements"), tencent.ServiceRole)
	}
	fmt.Fprintln(a.Out)
	fmt.Fprintln(a.Out, a.Bundle.T("guide.policy_hint"))
	fmt.Fprintln(a.Out)
	text, err := tencent.PolicyJSON(a.policyFilter(productIDs, scope, a.ExpWrite))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "%s %s\n\n", a.Bundle.T("guide.policy_title"), tencent.PolicyNameSuggestion(a.policyFilter(productIDs, scope, a.ExpWrite)))
	fmt.Fprint(a.Out, text)
	fmt.Fprintln(a.Out)
	for _, step := range a.policySteps() {
		fmt.Fprintln(a.Out, step)
	}
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
