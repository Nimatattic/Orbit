// Package cli wires configuration, credentials, providers and the renderers
// into the `orbit` command tree. It is the only layer allowed to know about
// flags, terminals and translation.
package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Crequency/orbit/internal/cloud"
	"github.com/Crequency/orbit/internal/cloud/fake"
	"github.com/Crequency/orbit/internal/cloud/tencent"
	"github.com/Crequency/orbit/internal/config"
	"github.com/Crequency/orbit/internal/credential"
	"github.com/Crequency/orbit/internal/exitcode"
	"github.com/Crequency/orbit/internal/i18n"
	"github.com/Crequency/orbit/internal/tty"
)

// DefaultPlatform is the only platform this batch implements (decision A3).
const DefaultPlatform = "tencent"

// DemoAccount is the synthetic account used by --demo when nothing is configured.
const DemoAccount = "demo"

// App carries resolved state across one command invocation.
type App struct {
	// Persistent flags.
	ConfigPath string
	Lang       string
	Local      bool
	Global     bool
	NoTUI      bool
	Demo       bool
	JSON       bool

	// Command flags shared by update/clean/status.
	Accounts    string
	AllAccounts bool
	Platforms   string
	Products    string
	Domains     string
	DryRun      bool
	Yes         bool
	FailOnChg   bool
	Strategy    string
	OlderThan   string
	IncludeFree bool
	PartialScan bool
	BackupDir   string
	ExpWrite    bool

	Loaded      *config.Loaded
	Bundle      *i18n.Bundle
	Creds       *credential.Credentials
	Out         io.Writer
	Err         io.Writer
	In          io.Reader
	WorkDir     string
	Home        string
	Interactive bool
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	app := &App{
		Out:     os.Stdout,
		Err:     os.Stderr,
		In:      os.Stdin,
		WorkDir: ".",
		Home:    config.Home(),
	}
	root := app.newRootCmd()
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	if err := root.Execute(); err != nil {
		return app.fail(err)
	}
	return exitcode.OK
}

func (a *App) fail(err error) int {
	bundle := a.Bundle
	if bundle == nil {
		bundle, _ = i18n.New(i18n.FromEnv(a.Lang, ""))
	}
	if bundle == nil {
		fmt.Fprintln(a.Err, err)
		return exitcode.CodeOf(err)
	}
	var e *exitcode.Error
	if errors.As(err, &e) {
		fmt.Fprintln(a.Err, bundle.T(e.Key, e.Args...))
		if e.Err != nil {
			fmt.Fprintf(a.Err, "  %v\n", e.Err)
		}
		return e.Code
	}
	fmt.Fprintln(a.Err, bundle.T("err.generic", "error", err))
	return exitcode.CodeOf(err)
}

// load resolves configuration and language. Missing files are not an error:
// Orbit runs on built-in defaults until `orbit config` creates a file.
func (a *App) load() error {
	loaded, err := config.Load(config.Options{
		Explicit: a.ConfigPath,
		UseLocal: a.Local,
		WorkDir:  a.WorkDir,
		Home:     a.Home,
	})
	if err != nil {
		return exitcode.Wrap(err, exitcode.Fatal, "err.config_load", "error", err)
	}
	a.Loaded = loaded
	bundle, err := i18n.New(i18n.FromEnv(a.Lang, loaded.Config.Language))
	if err != nil {
		return exitcode.Wrap(err, exitcode.Fatal, "err.config_load", "error", err)
	}
	a.Bundle = bundle
	a.Interactive = tty.Interactive() && !a.NoTUI
	return nil
}

func (a *App) creds() (*credential.Credentials, error) {
	if a.Creds != nil {
		return a.Creds, nil
	}
	store := credential.NewStore(a.credentialsPath())
	c, err := store.Load()
	if err != nil {
		var me *credential.ModeError
		if errors.As(err, &me) {
			return nil, exitcode.Wrap(err, exitcode.Fatal, "err.credentials_mode",
				"mode", fmt.Sprintf("%04o", me.Mode.Perm()), "path", me.Path)
		}
		return nil, exitcode.Wrap(err, exitcode.Fatal, "err.credentials_load", "error", err)
	}
	a.Creds = c
	return c, nil
}

func (a *App) credentialsPath() string {
	return a.Home + string(os.PathSeparator) + "credentials.yaml"
}

// credentialStore returns the credential store for this run.
func (a *App) credentialStore() *credential.Store {
	return credential.NewStore(a.credentialsPath())
}

// providerFor builds the cloud provider for one account.
func (a *App) providerFor(alias string) (cloud.Provider, error) {
	if a.Demo {
		return fake.NewDemo(), nil
	}
	acc, ok := a.accountConfig(alias)
	if !ok {
		return nil, exitcode.New(exitcode.Fatal, "err.account_not_found", "alias", alias)
	}
	creds, err := a.creds()
	if err != nil {
		return nil, err
	}
	secret, err := credential.Resolve(alias, acc.Credential, creds)
	if err != nil {
		return nil, exitcode.Wrap(err, exitcode.Fatal, "err.account_not_found", "alias", alias)
	}
	return tencent.New(alias, tencent.Options{SecretID: secret.SecretID, SecretKey: secret.SecretKey}), nil
}

// accountTargets decides which accounts a command runs against. Ambiguity is
// refused rather than guessed (plan §3.5, exit code 4).
//
// The known set is the union of accounts declared in configuration and aliases
// present in the credential store: storing credentials is enough to make an
// account usable, which is what `orbit account add` relies on.
func (a *App) accountTargets() ([]string, error) {
	configured := a.knownAliases()
	if a.Demo && len(configured) == 0 {
		return []string{DemoAccount}, nil
	}
	if a.AllAccounts {
		if len(configured) == 0 {
			return nil, exitcode.New(exitcode.Fatal, "err.no_accounts")
		}
		return configured, nil
	}
	if v := strings.TrimSpace(a.Accounts); v != "" {
		return splitList(v), nil
	}
	if len(configured) == 0 {
		return nil, exitcode.New(exitcode.Fatal, "err.no_accounts")
	}
	if len(configured) == 1 {
		return configured, nil
	}
	return nil, exitcode.New(exitcode.GateRefused, "err.accounts_ambiguous")
}

// knownAliases merges configuration accounts with stored credentials.
func (a *App) knownAliases() []string {
	seen := map[string]bool{}
	var out []string
	for _, acc := range a.Loaded.Config.Accounts(DefaultPlatform) {
		if !seen[acc.Alias] {
			seen[acc.Alias] = true
			out = append(out, acc.Alias)
		}
	}
	if creds, err := a.creds(); err == nil {
		for alias := range creds.Accounts {
			if !seen[alias] {
				seen[alias] = true
				out = append(out, alias)
			}
		}
	}
	sort.Strings(out)
	return out
}

// accountConfig resolves one account, falling back to a store-backed entry when
// credentials exist but the configuration does not mention the alias.
func (a *App) accountConfig(alias string) (config.Account, bool) {
	if acc, ok := a.Loaded.Config.Account(DefaultPlatform, alias); ok {
		return acc, true
	}
	if creds, err := a.creds(); err == nil {
		if _, ok := creds.Accounts[alias]; ok {
			return config.Account{Alias: alias, Credential: config.CredentialRef{Source: "store"}}, true
		}
	}
	return config.Account{}, false
}

// mapProviderErr turns provider sentinels into actionable CLI errors.
func (a *App) mapProviderErr(err error, platform string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, cloud.ErrNotImplemented) {
		return exitcode.Wrap(err, exitcode.Fatal, "err.provider_unimplemented", "platform", platform)
	}
	return err
}

// confirm asks a yes/no question when an interactive terminal is attached.
func (a *App) confirm(question string) bool {
	if !a.Interactive {
		return false
	}
	fmt.Fprint(a.Out, question+" [y/N] ")
	reader := bufio.NewReader(a.In)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func (a *App) jsonOut(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *App) platformList() []string {
	if v := strings.TrimSpace(a.Platforms); v != "" {
		return splitList(v)
	}
	if p := a.Loaded.Config.Defaults.Platforms; len(p) > 0 {
		return p
	}
	return []string{DefaultPlatform}
}

// products returns the effective product list, validating it early.
func (a *App) products(account string) ([]string, error) {
	if v := strings.TrimSpace(a.Products); v != "" {
		items := splitList(v)
		if err := config.ValidateProducts(items); err != nil {
			return nil, exitcode.Wrap(err, exitcode.Fatal, "err.invalid_products", "value", v)
		}
		return items, nil
	}
	acc, ok := a.accountConfig(account)
	if ok {
		return a.Loaded.Config.ProductsFor(acc), nil
	}
	return a.Loaded.Config.Defaults.Products, nil
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// newRootCmd builds the command tree with localized help text.
func (a *App) newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "orbit",
		Short:         "orbit",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.load(); err != nil {
				return err
			}
			localize(cmd.Root(), a.Bundle)
			return nil
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&a.ConfigPath, "config", "", "")
	pf.StringVar(&a.Lang, "lang", "", "")
	pf.BoolVar(&a.Local, "local", false, "")
	pf.BoolVar(&a.Global, "global", false, "")
	pf.BoolVar(&a.NoTUI, "no-tui", false, "")
	pf.BoolVar(&a.Demo, "demo", false, "")
	pf.BoolVar(&a.JSON, "json", false, "")

	root.AddCommand(
		a.newStatusCmd(),
		a.newUpdateCmd(),
		a.newCleanCmd(),
		a.newAccountCmd(),
		a.newConfigCmd(),
		a.newDoctorCmd(),
		a.newVersionCmd(),
	)
	return root
}
