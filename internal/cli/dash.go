package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/exitcode"
	"github.com/Crequency/orbit/internal/render/tui"
)

// newDashCmd builds `orbit dash`: the interactive control console.
func (a *App) newDashCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dash",
		Short: a.Bundle.T("cmd.dash.short"),
		Long:  a.Bundle.T("cmd.dash.long"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDash(cmd.Context())
		},
	}
	// --accounts is per command (like status/update/clean), so dash needs its own.
	cmd.Flags().StringVar(&a.Accounts, "accounts", "", "")
	return cmd
}

// runDash opens the console for one account and keeps reopening it after each
// function finishes, so the operator can chain actions without retyping commands.
func (a *App) runDash(ctx context.Context) error {
	if !a.Interactive || a.NoTUI {
		return exitcode.New(exitcode.Fatal, "err.dash_needs_tty")
	}

	account, confirmed, err := a.selectAccountInteractive(ctx)
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintln(a.Out, a.Bundle.T("tui.cancelled"))
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		// A provider is needed for the scan and the self-test, but a broken
		// credential must not abort the console: the operator should see the
		// failure on screen and still be able to reach the overview.
		provider, providerErr := a.providerFor(account)
		products := a.configuredProducts(account)
		if providerErr == nil {
			products, providerErr = a.products(account)
			if providerErr != nil {
				provider = nil
				products = a.configuredProducts(account)
			}
		}

		// The console owns the screen: keep provider progress off the terminal.
		a.progressFn = func(cloud.Progress) {}

		outcome, err := tui.RunDash(ctx, a.Bundle, tui.DashJob{
			Account:  account,
			Products: products,
			Scan: func(scanCtx context.Context) (tui.AccountView, error) {
				if providerErr != nil {
					return tui.AccountView{}, a.userError(providerErr)
				}
				_, view, err := a.accountView(scanCtx, account)
				return view, err
			},
			Checks: func(checkCtx context.Context) ([]core.Check, error) {
				if providerErr != nil {
					return nil, a.userError(providerErr)
				}
				checks, err := provider.Preflight(checkCtx, account)
				if err != nil {
					return nil, a.userError(a.mapProviderErr(err, provider.Name()))
				}
				return checks, nil
			},
		})
		a.progressFn = nil
		if err != nil {
			return a.mapProviderErr(err, providerNamed(provider))
		}

		switch outcome.Action {
		case "update":
			if err := a.tuiUpdate(ctx, account, nil); err != nil {
				return err
			}
		case "clean":
			if err := a.tuiClean(ctx, account, nil); err != nil {
				return err
			}
		case "status":
			// The overview has its own tabs and can hand off to update/clean.
			targets, err := a.statusTargets()
			if err != nil {
				return err
			}
			if err := a.tuiStatusAndAct(ctx, targets); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// configuredProducts reports the products declared in configuration, so the
// console can still label the account when credentials cannot be resolved.
func (a *App) configuredProducts(account string) []string {
	if acc, ok := a.Loaded.Config.Account(DefaultPlatform, account); ok {
		return a.Loaded.Config.ProductsFor(acc)
	}
	return a.Loaded.Config.Defaults.Products
}

// providerNamed keeps the error message useful when no provider could be built.
func providerNamed(provider cloud.Provider) string {
	if provider == nil {
		return DefaultPlatform
	}
	return provider.Name()
}

// selectAccountInteractive resolves exactly one account for the console.
// Explicit flags win; otherwise the operator picks from a list when more than one
// account is registered.
func (a *App) selectAccountInteractive(ctx context.Context) (string, bool, error) {
	if v := strings.TrimSpace(a.Accounts); v != "" {
		aliases := splitList(v)
		if len(aliases) != 1 {
			return "", false, exitcode.New(exitcode.GateRefused, "err.dash_one_account")
		}
		return aliases[0], true, nil
	}

	all := a.knownAliases()
	if len(all) == 0 && a.Demo {
		all = []string{DemoAccount}
	}
	if len(all) == 0 {
		return "", false, exitcode.New(exitcode.Fatal, "err.no_accounts")
	}
	if len(all) == 1 {
		return all[0], true, nil
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

	selected, confirmed, err := tui.PickAccounts(ctx, a.Bundle, tui.PickerJob{
		Title:    a.Bundle.T("picker.title.dash"),
		Accounts: all,
		Detail:   detail,
		Single:   true,
	})
	if err != nil || !confirmed || len(selected) == 0 {
		return "", false, err
	}
	return selected[0], true, nil
}
