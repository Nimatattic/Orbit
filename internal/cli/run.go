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

// planInputs gathers everything a plan needs for one account.
type planInputs struct {
	certs     []core.Certificate
	resources []core.Resource
	evidence  map[string]core.BindingEvidence
}

func (a *App) collect(ctx context.Context, provider cloud.Provider, account string) (*planInputs, error) {
	products, err := a.products(account)
	if err != nil {
		return nil, err
	}
	scope := cloud.ScanScope{Products: products, Domains: splitList(a.Domains)}
	certs, err := provider.ListCertificates(ctx, account, scope)
	if err != nil {
		return nil, a.mapProviderErr(err, provider.Name())
	}
	resources, err := provider.ListResources(ctx, account, scope)
	if err != nil {
		return nil, a.mapProviderErr(err, provider.Name())
	}
	ids := make([]string, 0, len(certs))
	for _, c := range certs {
		ids = append(ids, c.ID)
	}
	evidence, err := provider.ListBindings(ctx, account, ids)
	if err != nil {
		return nil, a.mapProviderErr(err, provider.Name())
	}
	return &planInputs{certs: certs, resources: resources, evidence: evidence}, nil
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
		inputs, err := a.collect(ctx, provider, account)
		if err != nil {
			return err
		}
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
		results, err := provider.Apply(ctx, account, plan, cloud.ApplyOptions{ExperimentalWrite: a.ExpWrite})
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
		failures += countFailures(results)
	}

	if failures > 0 {
		return exitcode.New(exitcode.Partial, "doctor.fail")
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

	failures := 0
	for _, account := range targets {
		provider, err := a.providerFor(account)
		if err != nil {
			return err
		}
		inputs, err := a.collect(ctx, provider, account)
		if err != nil {
			return err
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
		if err := backupMetadata(dir, account, plan.Candidates); err != nil {
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
		failures += countFailures(results)
	}
	if failures > 0 {
		return exitcode.New(exitcode.Partial, "doctor.fail")
	}
	return nil
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

// backupMetadata writes one JSON file per candidate before deletion. M4 fills in
// the certificate material when the SSL API allows downloading it.
func backupMetadata(dir, account string, candidates []core.CleanCandidate) error {
	target := filepath.Join(dir, account)
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	stamp := time.Now().Format("20060102T150405")
	for _, c := range candidates {
		doc := map[string]any{
			"certificate_id": c.Cert.ID,
			"alias":          c.Cert.Alias,
			"domain":         c.Cert.Domain,
			"sans":           c.Cert.SANs,
			"not_after":      c.Cert.NotAfter.Format(time.RFC3339),
			"from":           c.Cert.From,
			"superseded_by":  c.SupersededBy,
			"evidence":       c.Evidence,
			"backed_up_at":   time.Now().Format(time.RFC3339),
		}
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		name := fmt.Sprintf("%s-%s.json", c.Cert.ID, stamp)
		if err := os.WriteFile(filepath.Join(target, name), raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) runStatus(ctx context.Context) error {
	targets, err := a.accountTargets()
	if err != nil {
		return err
	}
	type summary struct {
		Account        string `json:"account"`
		Certificates   int    `json:"certificates"`
		Uploaded       int    `json:"uploaded"`
		InUse          int    `json:"in_use"`
		UnusedUploaded int    `json:"unused_uploaded"`
		Resources      int    `json:"resources"`
		Unbound        int    `json:"unbound"`
		PendingApply   int    `json:"pending_apply"`
		ForeignCerts   int    `json:"foreign_certs"`
	}
	var out []summary

	for _, account := range targets {
		provider, err := a.providerFor(account)
		if err != nil {
			return err
		}
		inputs, err := a.collect(ctx, provider, account)
		if err != nil {
			return err
		}
		opts, err := a.updateOptions(account)
		if err != nil {
			return err
		}
		plan := core.BuildUpdatePlan(inputs.resources, inputs.certs, opts)

		s := summary{Account: account, Certificates: len(inputs.certs), Resources: len(inputs.resources), PendingApply: plan.Count(core.KindApply)}
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
		return a.jsonOut(out)
	}
	for _, s := range out {
		fmt.Fprintf(a.Out, "%s: %s\n", a.Bundle.T("account.header"), s.Account)
		fmt.Fprintf(a.Out, "  certificates: total=%d uploaded=%d in_use=%d unused=%d\n", s.Certificates, s.Uploaded, s.InUse, s.UnusedUploaded)
		fmt.Fprintf(a.Out, "  resources: total=%d unbound=%d foreign_cert_domains=%d\n", s.Resources, s.Unbound, s.ForeignCerts)
		fmt.Fprintf(a.Out, "  pending_apply: %d\n", s.PendingApply)
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
	return checks
}
