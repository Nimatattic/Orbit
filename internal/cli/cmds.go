package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/Crequency/orbit/internal/config"
	"github.com/Crequency/orbit/internal/core"
	"github.com/Crequency/orbit/internal/credential"
	"github.com/Crequency/orbit/internal/exitcode"
	"github.com/Crequency/orbit/internal/render/plain"
	"github.com/Crequency/orbit/internal/tty"
	"github.com/Crequency/orbit/internal/version"
)

func (a *App) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "version.short",
		RunE: func(*cobra.Command, []string) error {
			fmt.Fprintf(a.Out, "%s %s\n", a.Bundle.T("app.name"), version.String())
			return nil
		},
	}
}

func (a *App) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config [key=value ...]",
		Short: "config.short",
		RunE: func(_ *cobra.Command, args []string) error {
			return a.runConfigSet(args)
		},
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "config.show.short",
		RunE: func(*cobra.Command, []string) error {
			raw, err := yaml.Marshal(a.Loaded.Config)
			if err != nil {
				return err
			}
			fmt.Fprint(a.Out, string(raw))
			fmt.Fprintln(a.Out, a.Bundle.T("config.path", "path", a.Loaded.Config.Path()))
			return nil
		},
	}
	pathCmd := &cobra.Command{
		Use:   "path",
		Short: "config.path.short",
		RunE: func(*cobra.Command, []string) error {
			fmt.Fprintf(a.Out, "%s\n%s\n%s\n",
				a.Loaded.GlobalPath,
				config.LocalConfigPath(a.WorkDir),
				a.credentialsPath())
			return nil
		},
	}
	cmd.AddCommand(show, pathCmd)
	return cmd
}

func (a *App) configTarget() string {
	switch {
	case a.ConfigPath != "":
		return a.ConfigPath
	case a.Local:
		return config.LocalConfigPath(a.WorkDir)
	default:
		return config.GlobalConfigPath()
	}
}

func (a *App) runConfigSet(args []string) error {
	if len(args) == 0 {
		return exitcode.New(exitcode.Fatal, "config.usage")
	}
	type kv struct{ key, value string }
	var pairs []kv
	if len(args) == 2 && !strings.Contains(args[0], "=") {
		pairs = append(pairs, kv{args[0], args[1]})
	} else {
		for _, arg := range args {
			k, v, ok := strings.Cut(arg, "=")
			if !ok {
				return exitcode.New(exitcode.Fatal, "config.usage")
			}
			pairs = append(pairs, kv{strings.TrimSpace(k), strings.Trim(v, `"`)})
		}
	}

	known := map[string]bool{}
	for _, k := range config.Keys() {
		known[k] = true
	}

	target := a.configTarget()
	doc := map[string]any{}
	if raw, err := os.ReadFile(target); err == nil {
		_ = yaml.Unmarshal(raw, &doc)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	for _, p := range pairs {
		if !known[p.key] {
			return exitcode.New(exitcode.Fatal, "config.unknown_key", "key", p.key)
		}
		setNested(doc, p.key, parseScalar(p.value))
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, a.Bundle.T("config.saved", "path", target))
	return nil
}

// setNested writes value at a dotted path, creating intermediate maps.
func setNested(doc map[string]any, dotted string, value any) {
	parts := strings.Split(dotted, ".")
	cur := doc
	for i, part := range parts {
		if i == len(parts)-1 {
			cur[part] = value
			return
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
}

func parseScalar(v string) any {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	if strings.Contains(v, ",") {
		return splitList(v)
	}
	return v
}

// registerAccount ensures the alias exists in the target configuration file, so
// that adding credentials is enough to make an account visible everywhere.
func (a *App) registerAccount(alias string) error {
	target := a.configTarget()
	doc := map[string]any{}
	if raw, err := os.ReadFile(target); err == nil {
		_ = yaml.Unmarshal(raw, &doc)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	platforms, _ := doc["platforms"].(map[string]any)
	if platforms == nil {
		platforms = map[string]any{}
		doc["platforms"] = platforms
	}
	platform, _ := platforms[DefaultPlatform].(map[string]any)
	if platform == nil {
		platform = map[string]any{}
		platforms[DefaultPlatform] = platform
	}
	accounts, _ := platform["accounts"].([]any)
	for _, item := range accounts {
		if m, ok := item.(map[string]any); ok && fmt.Sprint(m["alias"]) == alias {
			return nil
		}
	}
	platform["accounts"] = append(accounts, map[string]any{
		"alias":      alias,
		"credential": map[string]any{"source": "store"},
	})
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(target, raw, 0o644)
}

func (a *App) newAccountCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "account", Short: "account.short"}
	cmd.AddCommand(a.newAccountListCmd(), a.newAccountAddCmd(), a.newAccountRemoveCmd(), a.newAccountTestCmd())
	return cmd
}

func (a *App) newAccountListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "account.list.short",
		RunE: func(*cobra.Command, []string) error {
			creds, err := a.creds()
			if err != nil {
				return err
			}
			aliases := a.knownAliases()
			if len(aliases) == 0 {
				fmt.Fprintln(a.Out, a.Bundle.T("account.none"))
				return nil
			}
			tw := tabwriter.NewWriter(a.Out, 2, 4, 2, ' ', 0)
			fmt.Fprintf(tw, "%s\t%s\t%s\n", a.Bundle.T("account.header"), a.Bundle.T("account.header_secret"), "Source")
			for _, alias := range aliases {
				stored := creds.Accounts[alias]
				source := "store"
				if acc, ok := a.Loaded.Config.Account(DefaultPlatform, alias); ok && acc.Credential.Source != "" {
					source = acc.Credential.Source
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", alias, credential.Redact(stored.SecretID), source)
			}
			tw.Flush()
			return nil
		},
	}
}

func (a *App) newAccountAddCmd() *cobra.Command {
	var alias, idEnv string
	var fromStdin, force bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "account.add.short",
		RunE: func(*cobra.Command, []string) error {
			if strings.TrimSpace(alias) == "" {
				return exitcode.New(exitcode.Fatal, "account.alias_required")
			}
			store := credential.NewStore(a.credentialsPath())
			existing, err := store.Load()
			if err != nil {
				var me *credential.ModeError
				if errors.As(err, &me) {
					return exitcode.Wrap(err, exitcode.Fatal, "err.credentials_mode",
						"mode", fmt.Sprintf("%04o", me.Mode.Perm()), "path", me.Path)
				}
				return err
			}
			if _, ok := existing.Accounts[alias]; ok && !force {
				return exitcode.New(exitcode.Fatal, "account.exists", "alias", alias)
			}

			var secretID, secretKey string
			switch {
			case idEnv != "":
				return exitcode.New(exitcode.Fatal, "err.account_needs_input")
			case fromStdin:
				reader := bufio.NewReader(a.In)
				line1, err := reader.ReadString('\n')
				if err != nil && line1 == "" {
					return err
				}
				line2, err := reader.ReadString('\n')
				if err != nil && line2 == "" {
					return err
				}
				secretID, secretKey = strings.TrimSpace(line1), strings.TrimSpace(line2)
			case a.Interactive:
				var err error
				secretID, err = a.readLine(a.Bundle.T("account.prompt.secret_id"))
				if err != nil {
					return err
				}
				secretKey, err = a.readSecret(a.Bundle.T("account.prompt.secret_key"))
				if err != nil {
					return err
				}
				again, err := a.readSecret(a.Bundle.T("account.prompt.confirm"))
				if err != nil {
					return err
				}
				if again != secretKey {
					return exitcode.New(exitcode.Fatal, "account.mismatch")
				}
			default:
				return exitcode.New(exitcode.Fatal, "err.account_needs_input")
			}
			if secretID == "" || secretKey == "" {
				return exitcode.New(exitcode.Fatal, "err.account_needs_input")
			}
			if err := store.Set(alias, secretID, secretKey); err != nil {
				return err
			}
			// Registering the alias in configuration keeps `account list`,
			// `doctor` and account selection consistent with the store.
			if err := a.registerAccount(alias); err != nil {
				return err
			}
			fmt.Fprintln(a.Out, a.Bundle.T("account.added", "alias", alias))
			return nil
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "account alias")
	cmd.Flags().StringVar(&idEnv, "secret-id-env", "", "")
	cmd.Flags().BoolVar(&fromStdin, "from-stdin", false, "read SecretId and SecretKey from stdin (two lines)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing account")
	return cmd
}

func (a *App) newAccountRemoveCmd() *cobra.Command {
	var alias string
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "account.remove.short",
		RunE: func(*cobra.Command, []string) error {
			if strings.TrimSpace(alias) == "" {
				return exitcode.New(exitcode.Fatal, "account.alias_required")
			}
			removed, err := credential.NewStore(a.credentialsPath()).Remove(alias)
			if err != nil {
				return err
			}
			if !removed {
				return exitcode.New(exitcode.Fatal, "account.not_found", "alias", alias)
			}
			fmt.Fprintln(a.Out, a.Bundle.T("account.removed", "alias", alias))
			return nil
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "account alias")
	return cmd
}

// checkProduct maps a preflight check to the product it probes, so that
// `account test --products=teo` can re-check a single product after a
// permission change.
var checkProduct = map[string]string{
	"cloud.check.ssl": "ssl",
	"cloud.check.cdn": "cdn",
	"cloud.check.teo": "teo",
}

func (a *App) newAccountTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "account.test.short",
		RunE: func(cmd *cobra.Command, _ []string) error {
			targets, err := a.accountTargets()
			if err != nil {
				return err
			}
			failed := false
			for _, alias := range targets {
				fmt.Fprintf(a.Out, "%s %s\n", a.Bundle.T("account.header"), alias)
				provider, err := a.providerFor(alias)
				if err != nil {
					return err
				}
				checks, err := provider.Preflight(cmd.Context(), alias)
				if err != nil {
					return a.mapProviderErr(err, provider.Name())
				}
				wanted := splitList(a.Products)
				checks = filterChecks(checks, wanted)
				plain.Checks(a.Out, a.Bundle, checks)
				for _, c := range checks {
					if c.Status == core.CheckFail {
						failed = true
					}
				}
			}
			if failed {
				return exitcode.New(exitcode.Partial, "msg.partial")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&a.Accounts, "accounts", "", "")
	cmd.Flags().BoolVar(&a.AllAccounts, "all-accounts", false, "")
	cmd.Flags().StringVar(&a.Products, "products", "", "")
	return cmd
}

// filterChecks keeps credential/role checks and only the requested products.
func filterChecks(checks []core.Check, products []string) []core.Check {
	if len(products) == 0 {
		return checks
	}
	want := map[string]bool{}
	for _, p := range products {
		want[p] = true
	}
	out := make([]core.Check, 0, len(checks))
	for _, c := range checks {
		product, isProduct := checkProduct[c.Key]
		if !isProduct || want[product] {
			out = append(out, c)
		}
	}
	return out
}

func (a *App) newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "doctor.short",
		RunE: func(*cobra.Command, []string) error {
			checks := a.doctorChecks()
			fmt.Fprintln(a.Out, a.Bundle.T("doctor.title"))
			plain.Checks(a.Out, a.Bundle, checks)
			for _, c := range checks {
				if c.Status == "fail" {
					return exitcode.New(exitcode.Partial, "msg.partial")
				}
			}
			return nil
		},
	}
}

func (c *App) readLine(prompt string) (string, error) {
	fmt.Fprint(c.Out, prompt)
	reader := bufio.NewReader(c.In)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (c *App) readSecret(prompt string) (string, error) {
	if f, ok := c.In.(*os.File); ok && tty.IsTerminal(f) {
		fmt.Fprint(c.Out, prompt)
		raw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(c.Out)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	return c.readLine(prompt)
}
