package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/config"
	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/exitcode"
	"github.com/Crequency/orbit/internal/i18n"
	"github.com/Crequency/orbit/internal/render/plain"
	"github.com/Crequency/orbit/internal/render/tui"
)

func (a *App) newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "status.short",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runStatus(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&a.Accounts, "accounts", "", "")
	cmd.Flags().BoolVar(&a.AllAccounts, "all-accounts", false, "")
	cmd.Flags().StringVar(&a.Platforms, "platforms", "", "")
	cmd.Flags().StringVar(&a.Products, "products", "", "")
	return cmd
}

func (a *App) newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "update.short",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runUpdate(cmd.Context())
		},
	}
	cmd.Flags().Bool("ssl", false, "apply SSL certificates")
	cmd.Flags().StringVar(&a.Accounts, "accounts", "", "")
	cmd.Flags().BoolVar(&a.AllAccounts, "all-accounts", false, "")
	cmd.Flags().StringVar(&a.Platforms, "platforms", "", "")
	cmd.Flags().StringVar(&a.Products, "products", "", "")
	cmd.Flags().StringVar(&a.Domains, "domains", "", "")
	cmd.Flags().StringVar(&a.Strategy, "strategy", "", "")
	cmd.Flags().BoolVar(&a.DryRun, "dry-run", false, "")
	cmd.Flags().BoolVar(&a.Yes, "yes", false, "")
	cmd.Flags().BoolVar(&a.FailOnChg, "fail-on-changes", false, "")
	cmd.Flags().BoolVar(&a.ExpWrite, "enable-experimental-write", false, "")
	return cmd
}

func (a *App) newCleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "clean.short",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runClean(cmd.Context())
		},
	}
	cmd.Flags().Bool("ssl", false, "clean up SSL certificates")
	cmd.Flags().StringVar(&a.Accounts, "accounts", "", "")
	cmd.Flags().BoolVar(&a.AllAccounts, "all-accounts", false, "")
	cmd.Flags().StringVar(&a.Platforms, "platforms", "", "")
	cmd.Flags().StringVar(&a.OlderThan, "older-than", "", "")
	cmd.Flags().BoolVar(&a.IncludeFree, "include-free", false, "")
	cmd.Flags().BoolVar(&a.PartialScan, "permit-partial-scan", false, "")
	cmd.Flags().BoolVar(&a.DryRun, "dry-run", false, "")
	cmd.Flags().BoolVar(&a.Yes, "yes", false, "")
	cmd.Flags().StringVar(&a.BackupDir, "backup-dir", "", "")
	return cmd
}

// planInputs gathers everything a plan needs for one account. Warnings record
// products that could not be scanned: the plan is still usable, but cleanup is
// not, because its evidence would be incomplete.
type planInputs struct {
	certs     []core.Certificate
	resources []core.Resource
	evidence  map[string]core.BindingEvidence
	warnings  []cloud.ScanWarning
	blocking  []cloud.ScanWarning
}

// collectOptions says which evidence the caller actually needs. Scanning the
// association query is the slowest step, so callers that do not use it must say
// so (update only plans from the pool and the resources).
type collectOptions struct {
	cachedEvidence bool
	needEvidence   bool
}

func (a *App) collect(ctx context.Context, provider cloud.Provider, account string, opts collectOptions) (*planInputs, error) {
	products, err := a.products(account)
	if err != nil {
		return nil, err
	}
	a.report("progress.header", "account", account, "products", strings.Join(products, ","))
	scan, err := provider.Scan(ctx, account, cloud.ScanScope{
		Products:       products,
		Domains:        splitList(a.Domains),
		CachedEvidence: opts.cachedEvidence,
		SkipEvidence:   !opts.needEvidence,
	})
	if err != nil {
		return nil, a.mapProviderErr(err, provider.Name())
	}
	return &planInputs{
		certs:     scan.Certificates,
		resources: scan.Resources,
		evidence:  scan.Evidence,
		warnings:  scan.Warnings,
		blocking:  scan.BlockingWarnings(),
	}, nil
}

// warningDTO is the JSON shape of a scan warning (errors do not marshal well).
type warningDTO struct {
	Product  string `json:"product"`
	Action   string `json:"action"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Blocking bool   `json:"blocking"`
}

func (a *App) warningDTOs(warnings []cloud.ScanWarning) []warningDTO {
	out := make([]warningDTO, 0, len(warnings))
	for _, w := range warnings {
		dto := warningDTO{
			Product: w.Product, Action: w.Action, Code: w.Code, Scope: w.Scope,
			Blocking: !w.Informational,
		}
		if w.MessageKey != "" {
			dto.Message = a.Bundle.T(w.MessageKey)
		}
		out = append(out, dto)
	}
	return out
}

// compactWarningCodes are per-product availability failures. Printing the SDK
// stack and the generic permission hint for those is pure noise: the product is
// simply not usable for this account, and nothing in the run will change that.
var compactWarningCodes = map[string]bool{
	"UnauthorizedOperation": true,
}

// isCompactWarning reports whether a warning should collapse to one short line.
func isCompactWarning(w cloud.ScanWarning) bool {
	if compactWarningCodes[w.Code] {
		return true
	}
	// Variants such as UnauthorizedOperation.CamUnauthorized behave the same way.
	return strings.HasPrefix(w.Code, "UnauthorizedOperation.")
}

// warningLines renders scan warnings as display lines. Identical availability
// failures are collapsed into one counted entry so the output stays readable.
func (a *App) warningLines(warnings []cloud.ScanWarning) []string {
	counts := map[string]int{}
	merged := make([]cloud.ScanWarning, 0, len(warnings))
	for _, w := range warnings {
		sig := w.Product + "|" + w.Code + "|" + w.Scope
		if _, seen := counts[sig]; !seen {
			merged = append(merged, w)
		}
		counts[sig]++
	}

	lines := make([]string, 0, len(merged))
	for _, w := range merged {
		sig := w.Product + "|" + w.Code + "|" + w.Scope
		if isCompactWarning(w) {
			scope := ""
			if w.Scope != "" {
				scope = " (" + w.Scope + ")"
			}
			line := a.Bundle.T("scan.warning_compact", "product", w.Product, "code", w.Code, "scope", scope)
			if n := counts[sig]; n > 1 {
				line += fmt.Sprintf(" ×%d", n)
			}
			lines = append(lines, line)
			continue
		}
		hint := ""
		if w.MessageKey != "" {
			hint = a.Bundle.T(w.MessageKey)
		}
		scope := ""
		if w.Scope != "" {
			if a.Bundle.Lang() == "zh-CN" {
				scope = "（" + w.Scope + "）"
			} else {
				scope = " (" + w.Scope + ")"
			}
		}
		lines = append(lines, a.Bundle.T("scan.warning",
			"product", w.Product, "scope", scope, "action", w.Action, "code", w.Code, "hint", hint))
	}
	return lines
}

// renderWarnings prints partial-scan warnings to stderr so that machine-readable
// stdout stays clean. Full detail (hint + raw cloud error) is reserved for
// warnings that need action; availability failures collapse to "product — code".
func (a *App) renderWarnings(warnings []cloud.ScanWarning) {
	for _, line := range a.warningLines(warnings) {
		fmt.Fprintln(a.Err, line)
	}
	for _, w := range warnings {
		// Informational warnings explain themselves; only blocking ones benefit
		// from the raw cloud error.
		if w.Err != nil && !w.Informational && !isCompactWarning(w) {
			fmt.Fprintf(a.Err, "    %v\n", w.Err)
		}
	}
}

func (a *App) updateOptions(account string) (core.PlanOptions, error) {
	products, err := a.products(account)
	if err != nil {
		return core.PlanOptions{}, err
	}
	cfg := a.Loaded.Config
	strategy := a.Strategy
	if strings.TrimSpace(strategy) == "" {
		strategy = cfg.Defaults.Strategy
	}
	if err := config.ValidateStrategy(strategy); err != nil {
		return core.PlanOptions{}, exitcode.Wrap(err, exitcode.Fatal, "err.invalid_strategy", "value", strategy)
	}
	return core.PlanOptions{
		Account:                account,
		Now:                    time.Now(),
		Products:               products,
		Domains:                splitList(a.Domains),
		Strategy:               strategy,
		ExperimentalWrite:      a.ExpWrite,
		EdgeOneReplaceFreeCert: cfg.Defaults.EdgeOne.ReplaceFreeCert,
		Match: core.MatchOptions{
			AllowExpired: cfg.Defaults.DomainMatch.AllowExpired,
			Preference:   cfg.Defaults.CertPreference,
		},
	}, nil
}

func (a *App) runUpdate(ctx context.Context) error {
	// Interactive mode resolves accounts through the picker, so the ambiguity
	// refusal below must not run first.
	if a.wantsTUI() {
		return a.tuiInteractive(ctx, "update")
	}
	targets, err := a.accountTargets()
	if err != nil {
		return err
	}

	failures := 0
	for _, account := range targets {
		provider, err := a.providerFor(account)
		if err != nil {
			return err
		}
		// update plans from the pool and the resources only: the association
		// query is not needed and would add minutes to every run.
		inputs, err := a.collect(ctx, provider, account, collectOptions{})
		if err != nil {
			return err
		}
		a.renderWarnings(inputs.warnings)
		a.report("progress.plan")
		opts, err := a.updateOptions(account)
		if err != nil {
			return err
		}
		plan := core.BuildUpdatePlan(inputs.resources, inputs.certs, opts)

		if a.JSON {
			if err := a.jsonOut(map[string]any{"account": account, "plan": plan}); err != nil {
				return err
			}
		} else {
			plain.Plan(a.Out, a.Bundle, plan)
		}

		if plan.ApplyCount() == 0 {
			if !a.JSON {
				fmt.Fprintln(a.Out, "  "+a.Bundle.T("msg.nothing_to_do"))
			}
			continue
		}
		if !a.Yes {
			if !a.JSON {
				fmt.Fprintln(a.Out, "  "+a.Bundle.T("msg.plan_only"))
			}
			continue
		}
		results, err := provider.Apply(ctx, account, plan, cloud.ApplyOptions{
			ExperimentalWrite: a.ExpWrite,
			Concurrency:       a.Loaded.Config.Defaults.Concurrency.Changes,
		})
		if err != nil {
			return a.mapProviderErr(err, provider.Name())
		}
		if a.JSON {
			if err := a.jsonOut(map[string]any{"account": account, "results": results}); err != nil {
				return err
			}
		} else {
			plain.Results(a.Out, a.Bundle, results)
		}
		a.reportSummary(results)
		a.auditRun("update", account, "apply", results)
		failures += countFailures(results)
	}

	if failures > 0 {
		return exitcode.New(exitcode.Partial, "msg.partial")
	}
	if a.FailOnChg && !a.Yes {
		return exitcode.New(exitcode.PendingChanges, "msg.fail_on_changes")
	}
	return nil
}

func (a *App) runClean(ctx context.Context) error {
	targets, err := a.accountTargets()
	if err != nil {
		return err
	}
	cfg := a.Loaded.Config
	olderThanRaw := a.OlderThan
	if strings.TrimSpace(olderThanRaw) == "" {
		olderThanRaw = cfg.Defaults.Clean.OlderThan
	}
	olderThan, err := config.ParseDuration(olderThanRaw)
	if err != nil {
		return exitcode.Wrap(err, exitcode.Fatal, "err.invalid_duration", "value", olderThanRaw)
	}
	requireEvidence := cfg.Defaults.Clean.RequireFullEvidence && !a.PartialScan
	if a.PartialScan {
		fmt.Fprintln(a.Err, "warning: --permit-partial-scan disables the completeness gate (G2)")
	}

	if a.wantsTUI() {
		return a.tuiInteractive(ctx, "clean")
	}

	failures := 0
	for _, account := range targets {
		provider, err := a.providerFor(account)
		if err != nil {
			return err
		}
		inputs, err := a.collect(ctx, provider, account, collectOptions{needEvidence: true})
		if err != nil {
			return err
		}
		a.renderWarnings(inputs.warnings)
		a.report("progress.plan")
		if len(inputs.blocking) > 0 && requireEvidence && !a.PartialScan {
			// Gate G2: cleanup on a partial scan could delete a certificate that
			// a product we failed to read is still using.
			return exitcode.New(exitcode.GateRefused, "scan.refused")
		}
		plan := core.CleanCandidates(inputs.certs, inputs.evidence, inputs.resources, core.CleanOptions{
			Account:             account,
			Now:                 time.Now(),
			OlderThan:           olderThan,
			RequireFullEvidence: requireEvidence,
			IncludeFree:         a.IncludeFree,
			ProtectPatterns:     cfg.Defaults.Clean.ProtectPatterns,
		})

		if a.JSON {
			if err := a.jsonOut(map[string]any{"account": account, "clean": plan}); err != nil {
				return err
			}
		} else {
			plain.CleanPlan(a.Out, a.Bundle, plan)
		}
		if len(plan.Candidates) == 0 || !a.Yes {
			if len(plan.Candidates) > 0 && !a.Yes && !a.JSON {
				fmt.Fprintln(a.Out, "  "+a.Bundle.T("msg.plan_only"))
			}
			continue
		}

		dir := a.BackupDir
		if dir == "" {
			dir = config.BackupsDir()
		}
		if err := a.backupCertificates(ctx, dir, account, plan.Candidates, provider); err != nil {
			return exitcode.Wrap(err, exitcode.Fatal, "err.generic", "error", err)
		}
		ids := make([]string, 0, len(plan.Candidates))
		for _, c := range plan.Candidates {
			ids = append(ids, c.Cert.ID)
		}
		results, err := provider.DeleteCertificates(ctx, account, ids, cloud.DeleteOptions{
			BackupDir:     dir,
			CheckBindings: true,
		})
		if err != nil {
			return a.mapProviderErr(err, provider.Name())
		}
		if a.JSON {
			if err := a.jsonOut(map[string]any{"account": account, "results": results}); err != nil {
				return err
			}
		} else {
			plain.Results(a.Out, a.Bundle, results)
		}
		a.auditRun("clean", account, "delete", results)
		failures += countFailures(results)
	}
	if failures > 0 {
		return exitcode.New(exitcode.Partial, "msg.partial")
	}
	return nil
}

// reportSummary prints the applied/unconfirmed/failed tally so a long run ends
// with an explicit result instead of just stopping.
func (a *App) reportSummary(results []core.Result) {
	ok, unverified, failed := 0, 0, 0
	for _, r := range results {
		switch {
		case !r.OK:
			failed++
		case !r.Verified:
			unverified++
		default:
			ok++
		}
	}
	// In batch mode this joins the progress stream; after a TUI run the stream is
	// gone, so the same tally is printed as plain text without a fake timestamp.
	if fn := a.ensureProgress(); fn != nil {
		fn(cloud.Progress{Key: "progress.apply.summary",
			Args: []any{"ok", ok, "unverified", unverified, "failed", failed}})
		return
	}
	fmt.Fprintln(a.Out, a.Bundle.T("tui.done", "ok", ok, "unconfirmed", unverified, "failed", failed))
}

func countFailures(results []core.Result) int {
	n := 0
	for _, r := range results {
		if !r.OK {
			n++
		}
	}
	return n
}

// backupCertificates writes one directory per candidate holding the certificate
// material (when the cloud lets us export it) plus a metadata record. It runs
// before anything is deleted; a metadata-only backup is the documented fallback
// when the private key is not exportable.
func (a *App) backupCertificates(ctx context.Context, dir, account string, candidates []core.CleanCandidate, provider cloud.Provider) error {
	if len(candidates) == 0 {
		return nil
	}
	target := filepath.Join(dir, account)
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	a.report("backup.header", "path", target)

	stamp := time.Now().Format("20060102T150405")
	fetcher, canFetch := provider.(cloud.MaterialFetcher)
	var withoutMaterial []string
	withKey := 0

	for _, c := range candidates {
		sub := filepath.Join(target, c.Cert.ID+"-"+stamp)
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return err
		}

		material := map[string]any{"available": false}
		if canFetch {
			mat, err := fetcher.CertificateMaterial(ctx, account, c.Cert.ID)
			switch {
			case err != nil:
				material["error"] = a.userError(err).Error()
			default:
				files := []string{}
				if mat.CertPEM != "" {
					if err := os.WriteFile(filepath.Join(sub, "cert.pem"), []byte(mat.CertPEM), 0o644); err != nil {
						return err
					}
					files = append(files, "cert.pem")
				}
				if mat.ChainPEM != "" {
					if err := os.WriteFile(filepath.Join(sub, "chain.pem"), []byte(mat.ChainPEM), 0o644); err != nil {
						return err
					}
					files = append(files, "chain.pem")
				}
				if mat.KeyPEM != "" {
					// The private key is the sensitive part: 0600, never inherited.
					if err := os.WriteFile(filepath.Join(sub, "key.pem"), []byte(mat.KeyPEM), 0o600); err != nil {
						return err
					}
					files = append(files, "key.pem")
					withKey++
				}
				material = map[string]any{
					"available":            len(files) > 0,
					"files":                files,
					"fingerprint":          mat.Fingerprint,
					"private_key_exported": mat.KeyPEM != "",
				}
			}
		}
		if available, _ := material["available"].(bool); !available {
			withoutMaterial = append(withoutMaterial, c.Cert.ID)
		}

		doc := map[string]any{
			"certificate_id": c.Cert.ID,
			"alias":          c.Cert.Alias,
			"domain":         c.Cert.Domain,
			"sans":           c.Cert.SANs,
			"not_after":      c.Cert.NotAfter.Format(time.RFC3339),
			"from":           c.Cert.From,
			"superseded_by":  c.SupersededBy,
			"evidence":       c.Evidence,
			"material":       material,
			"backed_up_at":   time.Now().Format(time.RFC3339),
			"orbit_run_id":   a.runID,
		}
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sub, "meta.json"), raw, 0o600); err != nil {
			return err
		}
	}

	a.report("backup.done", "count", len(candidates), "keys", withKey)
	if len(withoutMaterial) > 0 {
		fmt.Fprintln(a.Err, a.Bundle.T("backup.material_unavailable", "ids", strings.Join(withoutMaterial, ", ")))
	}
	return nil
}

// statusTargets resolves the accounts to display. Unlike update/clean, looking is
// harmless, so every registered account is shown by default instead of refusing
// on ambiguity.
func (a *App) statusTargets() ([]string, error) {
	if v := strings.TrimSpace(a.Accounts); v != "" {
		return splitList(v), nil
	}
	aliases := a.knownAliases()
	if len(aliases) == 0 {
		if a.Demo {
			return []string{DemoAccount}, nil
		}
		return nil, exitcode.New(exitcode.Fatal, "err.no_accounts")
	}
	return aliases, nil
}

func (a *App) runStatus(ctx context.Context) error {
	targets, err := a.statusTargets()
	if err != nil {
		return err
	}
	if a.wantsTUI() {
		return a.tuiStatusAndAct(ctx, targets)
	}
	type summary struct {
		Account        string       `json:"account"`
		Certificates   int          `json:"certificates"`
		Uploaded       int          `json:"uploaded"`
		InUse          int          `json:"in_use"`
		UnusedUploaded int          `json:"unused_uploaded"`
		Resources      int          `json:"resources"`
		Unbound        int          `json:"unbound"`
		PendingApply   int          `json:"pending_apply"`
		ForeignCerts   int          `json:"foreign_certs"`
		Warnings       []warningDTO `json:"warnings,omitempty"`
	}
	var (
		out      []summary
		failures []string
	)

	for _, account := range targets {
		// Looking is harmless: one unusable account must not hide the others
		// (the TUI shows the same per-account error state).
		provider, err := a.providerFor(account)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", account, a.userError(err)))
			continue
		}
		// status may reuse the cloud's short-lived association cache unless the
		// operator asks for fresh evidence.
		inputs, err := a.collect(ctx, provider, account, collectOptions{cachedEvidence: !a.NoCache, needEvidence: true})
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", account, a.userError(err)))
			continue
		}
		a.renderWarnings(inputs.warnings)
		a.report("progress.plan")
		opts, err := a.updateOptions(account)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", account, a.userError(err)))
			continue
		}
		plan := core.BuildUpdatePlan(inputs.resources, inputs.certs, opts)

		s := summary{
			Account:      account,
			Certificates: len(inputs.certs),
			Resources:    len(inputs.resources),
			PendingApply: plan.Count(core.KindApply),
			Warnings:     a.warningDTOs(inputs.warnings),
		}
		for _, c := range inputs.certs {
			if c.IsUploaded() {
				s.Uploaded++
			}
			if ev, ok := inputs.evidence[c.ID]; ok && ev.TotalCount > 0 {
				s.InUse++
			}
		}
		for _, r := range inputs.resources {
			if r.BoundCert == "" {
				s.Unbound++
			}
			if r.BoundForeign {
				s.ForeignCerts++
			}
		}
		s.UnusedUploaded = s.Uploaded - s.InUse
		if s.UnusedUploaded < 0 {
			s.UnusedUploaded = 0
		}
		out = append(out, s)
	}

	if a.JSON {
		if err := a.jsonOut(out); err != nil {
			return err
		}
		for _, f := range failures {
			fmt.Fprintln(a.Err, f)
		}
		if len(failures) > 0 {
			return exitcode.New(exitcode.Partial, "msg.partial")
		}
		return nil
	}
	for _, s := range out {
		fmt.Fprintf(a.Out, "%s: %s\n", a.Bundle.T("account.header"), s.Account)
		fmt.Fprintf(a.Out, "  certificates: total=%d uploaded=%d in_use=%d unused=%d\n", s.Certificates, s.Uploaded, s.InUse, s.UnusedUploaded)
		fmt.Fprintf(a.Out, "  resources: total=%d unbound=%d foreign_cert_domains=%d\n", s.Resources, s.Unbound, s.ForeignCerts)
		fmt.Fprintf(a.Out, "  pending_apply: %d\n", s.PendingApply)
	}
	for _, f := range failures {
		fmt.Fprintln(a.Err, a.Bundle.T("status.account_failed", "error", f))
	}
	if len(failures) > 0 {
		return exitcode.New(exitcode.Partial, "msg.partial")
	}
	return nil
}

func (a *App) doctorChecks() []core.Check {
	var checks []core.Check

	if _, err := os.Stat(a.Loaded.GlobalPath); err == nil {
		checks = append(checks, core.Check{Key: "doctor.check.config", Status: core.CheckOK, Detail: a.Loaded.GlobalPath})
	} else {
		checks = append(checks, core.Check{Key: "doctor.check.config", Status: core.CheckWarn, Detail: "not created yet"})
	}

	if _, err := os.Stat(a.Loaded.LocalPath); err == nil {
		checks = append(checks, core.Check{Key: "doctor.check.local_config", Status: core.CheckOK, Detail: a.Loaded.LocalPath})
	} else {
		checks = append(checks, core.Check{Key: "doctor.check.local_config", Status: core.CheckWarn, Detail: "not present"})
	}

	store := a.credentialStore()
	if info, err := os.Stat(store.Path()); err == nil {
		if info.Mode().Perm()&0o077 != 0 {
			checks = append(checks, core.Check{Key: "doctor.check.credential_mode", Status: core.CheckFail,
				Detail: fmt.Sprintf("%04o (expected 0600)", info.Mode().Perm())})
		} else {
			checks = append(checks, core.Check{Key: "doctor.check.credential_mode", Status: core.CheckOK, Detail: "0600"})
		}
		checks = append(checks, core.Check{Key: "doctor.check.credentials", Status: core.CheckOK, Detail: store.Path()})
	} else {
		checks = append(checks, core.Check{Key: "doctor.check.credentials", Status: core.CheckWarn, Detail: "not created yet"})
	}

	accounts := a.Loaded.Config.Accounts(DefaultPlatform)
	if len(accounts) == 0 {
		checks = append(checks, core.Check{Key: "doctor.check.accounts", Status: core.CheckWarn, Detail: "0"})
	} else {
		aliases := make([]string, 0, len(accounts))
		for _, acc := range accounts {
			aliases = append(aliases, acc.Alias)
		}
		checks = append(checks, core.Check{Key: "doctor.check.accounts", Status: core.CheckOK, Detail: strings.Join(aliases, ", ")})
	}

	checks = append(checks, core.Check{Key: "doctor.check.locales", Status: core.CheckOK, Detail: strings.Join(i18n.Supported(), ", ")})

	if a.Demo {
		checks = append(checks, core.Check{Key: "doctor.check.provider", Status: core.CheckWarn, Detail: "demo data (no Tencent Cloud access)"})
	} else {
		detail := "tencent SDK wired: ssl + cdn + teo"
		if accounts := a.Loaded.Config.Accounts(DefaultPlatform); len(accounts) > 0 {
			detail = fmt.Sprintf("%s, account %s", detail, accounts[0].Alias)
		}
		checks = append(checks, core.Check{Key: "doctor.check.provider", Status: core.CheckOK, Detail: detail})
	}

	// Per-product probes: this is what tells "UnauthorizedOperation" apart from
	// "the product is not activated for this account".
	probeAccounts := a.Loaded.Config.Accounts(DefaultPlatform)
	if len(probeAccounts) == 0 && a.Demo {
		probeAccounts = []config.Account{{Alias: DemoAccount}}
	}
	for _, acc := range probeAccounts {
		provider, err := a.providerFor(acc.Alias)
		if err != nil {
			continue
		}
		probes, err := provider.Preflight(context.Background(), acc.Alias)
		if err != nil {
			continue
		}
		for _, p := range probes {
			p.Detail = acc.Alias + ": " + p.Detail
			checks = append(checks, p)
		}
	}
	return checks
}

// mode selection -----------------------------------------------------------

// wantsTUI reports whether the command should take over the terminal.
//
// Contract: --dry-run keeps the CLI (a plan to read), --yes keeps the CLI batch
// mode (for cron and scripts), --no-tui forces text. Everything else on a
// terminal is interactive.
func (a *App) wantsTUI() bool {
	if a.DryRun || a.Yes || a.NoTUI || a.JSON {
		return false
	}
	return a.Interactive
}

// tuiUpdate runs `update` interactively: scan, show the plan, ask for
// confirmation, then apply with live per-change state.
func (a *App) tuiUpdate(ctx context.Context, account string, only map[string]bool) error {
	events := make(chan cloud.Progress, 512)
	a.progressSink = events
	a.progressFn = nil

	provider, err := a.providerFor(account)
	if err != nil {
		return err
	}
	products, err := a.products(account)
	if err != nil {
		return err
	}

	var plan core.Plan
	job := tui.Job{
		Title:   a.Bundle.T("cmd.update.short") + " · " + account,
		Summary: []string{a.Bundle.T("tui.summary.products", "products", strings.Join(products, ", "))},
		Scan: func(scanCtx context.Context) ([]tui.Item, error) {
			inputs, err := a.collect(scanCtx, provider, account, collectOptions{})
			if err != nil {
				return nil, a.userError(err)
			}
			a.renderWarnings(inputs.warnings)
			opts, err := a.updateOptions(account)
			if err != nil {
				return nil, a.userError(err)
			}
			plan = filterPlanByKeys(core.BuildUpdatePlan(inputs.resources, inputs.certs, opts), only)
			items := make([]tui.Item, 0, plan.ApplyCount())
			for _, ch := range plan.Pending() {
				target := ""
				if ch.To != nil {
					target = ch.To.ID
				}
				items = append(items, tui.NewItem(string(ch.Product), ch.Domain, target, ""))
			}
			return items, nil
		},
		Apply: func(applyCtx context.Context) ([]core.Result, error) {
			return provider.Apply(applyCtx, account, plan, cloud.ApplyOptions{
				ExperimentalWrite: a.ExpWrite,
				Concurrency:       a.Loaded.Config.Defaults.Concurrency.Changes,
			})
		},
	}

	outcome, err := tui.Run(ctx, a.Bundle, events, job)
	// Reset the sink before reporting: the channel may still receive events from
	// an in-flight apply, and those sends must never reach a closed channel (and
	// the summary belongs on the normal text path anyway).
	a.progressSink = nil
	a.progressFn = nil
	if err != nil {
		return a.mapProviderErr(err, provider.Name())
	}
	if outcome.Cancelled {
		fmt.Fprintln(a.Out, a.Bundle.T("tui.cancelled"))
		return nil
	}
	if len(outcome.Results) == 0 {
		fmt.Fprintln(a.Out, a.Bundle.T("msg.nothing_to_do"))
		return nil
	}
	fmt.Fprintln(a.Out, a.Bundle.T("tui.restored"))
	plain.Results(a.Out, a.Bundle, outcome.Results)
	a.reportSummary(outcome.Results)
	a.auditRun("update", account, "apply", outcome.Results)
	if failures := countFailures(outcome.Results); failures > 0 {
		return exitcode.New(exitcode.Partial, "msg.partial")
	}
	return nil
}

// tuiClean runs `clean` interactively: scan, show the candidates, ask for
// confirmation, then delete with live per-certificate state.
func (a *App) tuiClean(ctx context.Context, account string, onlyCerts map[string]bool) error {
	events := make(chan cloud.Progress, 512)
	a.progressSink = events
	a.progressFn = nil

	provider, err := a.providerFor(account)
	if err != nil {
		return err
	}
	cfg := a.Loaded.Config
	olderThanRaw := a.OlderThan
	if strings.TrimSpace(olderThanRaw) == "" {
		olderThanRaw = cfg.Defaults.Clean.OlderThan
	}
	olderThan, err := config.ParseDuration(olderThanRaw)
	if err != nil {
		return exitcode.Wrap(err, exitcode.Fatal, "err.invalid_duration", "value", olderThanRaw)
	}
	requireEvidence := cfg.Defaults.Clean.RequireFullEvidence && !a.PartialScan

	var (
		candidateIDs  []string
		candidatePlan []core.CleanCandidate
	)
	job := tui.Job{
		Title:   a.Bundle.T("cmd.clean.short") + " · " + account,
		Summary: []string{a.Bundle.T("tui.summary.clean", "older", olderThanRaw)},
		Scan: func(scanCtx context.Context) ([]tui.Item, error) {
			inputs, err := a.collect(scanCtx, provider, account, collectOptions{needEvidence: true})
			if err != nil {
				return nil, a.userError(err)
			}
			a.renderWarnings(inputs.warnings)
			if len(inputs.blocking) > 0 && requireEvidence && !a.PartialScan {
				return nil, a.userError(exitcode.New(exitcode.GateRefused, "scan.refused"))
			}
			plan := core.CleanCandidates(inputs.certs, inputs.evidence, inputs.resources, core.CleanOptions{
				Account:             account,
				Now:                 time.Now(),
				OlderThan:           olderThan,
				RequireFullEvidence: requireEvidence,
				IncludeFree:         a.IncludeFree,
				ProtectPatterns:     cfg.Defaults.Clean.ProtectPatterns,
			})
			items := make([]tui.Item, 0, len(plan.Candidates))
			candidateIDs = candidateIDs[:0]
			candidatePlan = plan.Candidates
			for _, c := range plan.Candidates {
				candidateIDs = append(candidateIDs, c.Cert.ID)
				items = append(items, tui.NewItem("ssl", c.Cert.Alias, c.SupersededBy, c.Cert.ID))
			}
			return items, nil
		},
		Apply: func(applyCtx context.Context) ([]core.Result, error) {
			dir := a.BackupDir
			if dir == "" {
				dir = config.BackupsDir()
			}
			// Backup before deleting: the metadata record is the operator's only
			// trace of what was removed.
			if err := a.backupCertificates(applyCtx, dir, account, candidatePlan, provider); err != nil {
				return nil, err
			}
			return provider.DeleteCertificates(applyCtx, account, candidateIDs, cloud.DeleteOptions{
				BackupDir:     dir,
				CheckBindings: true,
			})
		},
	}

	outcome, err := tui.Run(ctx, a.Bundle, events, job)
	// See tuiUpdate: never close the channel while an apply may still emit.
	a.progressSink = nil
	a.progressFn = nil
	if err != nil {
		return a.mapProviderErr(err, provider.Name())
	}
	if outcome.Cancelled {
		fmt.Fprintln(a.Out, a.Bundle.T("tui.cancelled"))
		return nil
	}
	if len(outcome.Results) == 0 {
		fmt.Fprintln(a.Out, a.Bundle.T("msg.nothing_to_do"))
		return nil
	}
	fmt.Fprintln(a.Out, a.Bundle.T("tui.restored"))
	plain.Results(a.Out, a.Bundle, outcome.Results)
	a.reportSummary(outcome.Results)
	a.auditRun("clean", account, "delete", outcome.Results)
	if failures := countFailures(outcome.Results); failures > 0 {
		return exitcode.New(exitcode.Partial, "msg.partial")
	}
	return nil
}

// interactive mode ----------------------------------------------------------

// tuiInteractive resolves the accounts for an interactive update/clean run.
// Explicit flags win; otherwise a multi-select screen is shown when more than one
// account is registered. Each selected account then gets its own run view, so the
// operator confirms per account and never touches one by accident.
func (a *App) tuiInteractive(ctx context.Context, action string) error {
	selected, confirmed, err := a.selectAccountsInteractive(ctx, action)
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintln(a.Out, a.Bundle.T("tui.cancelled"))
		return nil
	}
	for _, account := range selected {
		if action == "clean" {
			err = a.tuiClean(ctx, account, nil)
		} else {
			err = a.tuiUpdate(ctx, account, nil)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *App) selectAccountsInteractive(ctx context.Context, action string) ([]string, bool, error) {
	if v := strings.TrimSpace(a.Accounts); v != "" {
		return splitList(v), true, nil
	}
	all := a.knownAliases()
	if len(all) == 0 && a.Demo {
		// --demo works without any configuration.
		all = []string{DemoAccount}
	}
	if len(all) == 0 {
		return nil, false, exitcode.New(exitcode.Fatal, "err.no_accounts")
	}
	if a.AllAccounts || len(all) <= 1 {
		return all, true, nil
	}

	detail := make(map[string]string, len(all))
	for _, name := range all {
		source, products := "store", a.Loaded.Config.Defaults.Products
		if acc, ok := a.Loaded.Config.Account(DefaultPlatform, name); ok {
			if acc.Credential.Source != "" {
				source = acc.Credential.Source
			}
			products = a.Loaded.Config.ProductsFor(acc)
		}
		detail[name] = a.Bundle.T("picker.detail", "source", source, "products", strings.Join(products, ", "))
	}

	titleKey := "picker.title.update"
	if action == "clean" {
		titleKey = "picker.title.clean"
	}
	return tui.PickAccounts(ctx, a.Bundle, tui.PickerJob{
		Title:    a.Bundle.T(titleKey),
		Accounts: all,
		Detail:   detail,
	})
}

// accountView builds the read-only snapshot both the overview and the console
// display. It reuses the cloud's association cache: these screens are for
// looking, not for acting.
func (a *App) accountView(ctx context.Context, account string) (cloud.Provider, tui.AccountView, error) {
	provider, err := a.providerFor(account)
	if err != nil {
		return nil, tui.AccountView{}, a.userError(err)
	}
	products, err := a.products(account)
	if err != nil {
		return nil, tui.AccountView{}, a.userError(err)
	}
	res, err := provider.Scan(ctx, account, cloud.ScanScope{
		Products:       products,
		CachedEvidence: !a.NoCache,
	})
	if err != nil {
		return nil, tui.AccountView{}, a.userError(a.mapProviderErr(err, provider.Name()))
	}
	opts, err := a.updateOptions(account)
	if err != nil {
		return nil, tui.AccountView{}, a.userError(err)
	}
	cfg := a.Loaded.Config
	olderThanRaw := a.OlderThan
	if strings.TrimSpace(olderThanRaw) == "" {
		olderThanRaw = cfg.Defaults.Clean.OlderThan
	}
	olderThan, err := config.ParseDuration(olderThanRaw)
	if err != nil {
		return nil, tui.AccountView{}, a.userError(exitcode.Wrap(err, exitcode.Fatal, "err.invalid_duration", "value", olderThanRaw))
	}
	plan := core.BuildUpdatePlan(res.Resources, res.Certificates, opts)
	clean := core.CleanCandidates(res.Certificates, res.Evidence, res.Resources, core.CleanOptions{
		Account:             account,
		Now:                 time.Now(),
		OlderThan:           olderThan,
		RequireFullEvidence: cfg.Defaults.Clean.RequireFullEvidence && !a.PartialScan,
		IncludeFree:         a.IncludeFree,
		ProtectPatterns:     cfg.Defaults.Clean.ProtectPatterns,
	})

	view := tui.AccountView{
		Account:   account,
		Products:  products,
		Warnings:  a.warningLines(res.Warnings),
		Total:     len(res.Certificates),
		Resources: len(res.Resources),
		ScannedAt: time.Now(),
	}
	for _, c := range res.Certificates {
		if c.IsUploaded() {
			view.Uploaded++
		}
		if ev, ok := res.Evidence[c.ID]; ok && ev.TotalCount > 0 {
			view.InUse++
		}
	}
	view.Unused = view.Uploaded - view.InUse
	if view.Unused < 0 {
		view.Unused = 0
	}
	for _, r := range res.Resources {
		if r.BoundCert == "" {
			view.Unbound++
		}
		if r.BoundForeign {
			view.Foreign++
		}
	}
	for _, ch := range plan.Pending() {
		target := ""
		if ch.To != nil {
			target = ch.To.ID
		}
		view.Pending = append(view.Pending, tui.NewItem(string(ch.Product), ch.Domain, target, ""))
	}
	for _, cand := range clean.Candidates {
		view.Candidates = append(view.Candidates, tui.NewItem("ssl", cand.Cert.Alias, cand.SupersededBy, cand.Cert.ID))
	}
	return provider, view, nil
}

// tuiStatusAndAct shows the multi-tab overview; pressing u/c hands over to the
// corresponding interactive run for the account on screen.
func (a *App) tuiStatusAndAct(ctx context.Context, accounts []string) error {
	// The overview owns the screen: provider progress must not print over it.
	a.progressFn = func(cloud.Progress) {}
	defer func() { a.progressFn = nil }()

	scan := func(scanCtx context.Context, account string) (tui.AccountView, error) {
		_, view, err := a.accountView(scanCtx, account)
		return view, err
	}

	outcome, err := tui.RunStatus(ctx, a.Bundle, tui.StatusJob{
		Title:    a.Bundle.T("status.title"),
		Accounts: accounts,
		Scan:     scan,
	})
	if err != nil {
		return err
	}
	switch outcome.Action {
	case "update":
		if outcome.Account == "" {
			return nil
		}
		return a.tuiUpdate(ctx, outcome.Account, keySet(outcome.Keys))
	case "clean":
		if outcome.Account == "" {
			return nil
		}
		return a.tuiClean(ctx, outcome.Account, keySet(outcome.Certs))
	}
	return nil
}

// keySet turns a slice into a lookup set (nil when empty, meaning "everything").
func keySet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}

// filterPlanByKeys keeps only the selected pending changes. An empty selection
// means "everything", which keeps `u` without any selection behaving as before.
func filterPlanByKeys(plan core.Plan, only map[string]bool) core.Plan {
	if len(only) == 0 {
		return plan
	}
	out := core.Plan{Account: plan.Account, GeneratedAt: plan.GeneratedAt}
	for _, ch := range plan.Changes {
		if !ch.NeedsWrite() {
			continue
		}
		if only[strings.ToLower(string(ch.Product)+"|"+ch.Domain)] {
			out.Changes = append(out.Changes, ch)
		}
	}
	return out
}

// filterCandidates keeps only the selected cleanup candidates.
func filterCandidates(candidates []core.CleanCandidate, only map[string]bool) []core.CleanCandidate {
	if len(only) == 0 {
		return candidates
	}
	out := make([]core.CleanCandidate, 0, len(candidates))
	for _, c := range candidates {
		if only[c.Cert.ID] {
			out = append(out, c)
		}
	}
	return out
}
