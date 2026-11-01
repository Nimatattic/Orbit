package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Crequency/orbit/internal/i18n"
)

// commandKeys maps command names to message keys.
var commandKeys = map[string]string{
	"orbit":       "root.short",
	"status":      "status.short",
	"update":      "update.short",
	"clean":       "clean.short",
	"account":     "account.short",
	"config":      "config.short",
	"doctor":      "doctor.short",
	"version":     "version.short",
	"dash":        "cmd.dash.short",
	"about":       "cmd.about.short",
	"dns":         "cmd.dns.short",
	"dns zones":   "cmd.dns.zones.short",
	"dns ls":      "cmd.dns.ls.short",
	"dns set":     "cmd.dns.set.short",
	"dns rm":      "cmd.dns.rm.short",
	"dns enable":  "cmd.dns.enable.short",
	"dns disable": "cmd.dns.disable.short",
	"dns check":   "cmd.dns.check.short",
	"list":        "account.list.short",
	"add":         "account.add.short",
	"remove":      "account.remove.short",
	"test":        "account.test.short",
	"guide":       "cmd.account.guide.short",
	"show":        "config.show.short",
	"path":        "config.path.short",
	"set":         "config.set.short",
}

// commandLongKeys maps command names to a long description key for the commands
// that carry one.
var commandLongKeys = map[string]string{
	"dash":      "cmd.dash.long",
	"guide":     "cmd.account.guide.long",
	"about":     "cmd.about.long",
	"dns":       "cmd.dns.long",
	"dns set":   "cmd.dns.set.long",
	"dns check": "cmd.dns.check.long",
}

// flagKeys maps flag names to message keys.
var flagKeys = map[string]string{
	"config":                    "flag.config",
	"lang":                      "flag.lang",
	"local":                     "flag.local",
	"global":                    "flag.global",
	"no-tui":                    "flag.no_tui",
	"demo":                      "flag.demo",
	"json":                      "flag.json",
	"accounts":                  "flag.accounts",
	"all-accounts":              "flag.all_accounts",
	"platforms":                 "flag.platforms",
	"products":                  "flag.products",
	"domains":                   "flag.domains",
	"dry-run":                   "flag.dry_run",
	"yes":                       "flag.yes",
	"fail-on-changes":           "flag.fail_on_changes",
	"strategy":                  "flag.strategy",
	"older-than":                "flag.older_than",
	"include-free":              "flag.include_free",
	"permit-partial-scan":       "flag.permit_partial_scan",
	"backup-dir":                "flag.backup_dir",
	"enable-experimental-write": "flag.experimental_write",
}

// localize rewrites help text of the whole command tree into the resolved
// language. It runs after configuration is loaded, so `--lang`/`$LANG`/config
// all take effect on help output too.
func localize(cmd *cobra.Command, b *i18n.Bundle) {
	if cmd == nil || b == nil {
		return
	}
	// Subcommands can share a name (`config set` vs `dns set`), so a qualified
	// "parent name" key wins over the bare name.
	lookup := func(m map[string]string) (string, bool) {
		if cmd.HasParent() && cmd.Parent().Name() != "orbit" {
			if key, ok := m[cmd.Parent().Name()+" "+cmd.Name()]; ok {
				return key, true
			}
		}
		key, ok := m[cmd.Name()]
		return key, ok
	}
	if key, ok := lookup(commandKeys); ok {
		cmd.Short = b.T(key)
	}
	if key, ok := lookup(commandLongKeys); ok {
		cmd.Long = b.T(key)
	}
	applyFlagUsage(cmd.Flags(), b)
	applyFlagUsage(cmd.PersistentFlags(), b)
	for _, child := range cmd.Commands() {
		localize(child, b)
	}
}

func applyFlagUsage(fs *pflag.FlagSet, b *i18n.Bundle) {
	if fs == nil {
		return
	}
	fs.VisitAll(func(f *pflag.Flag) {
		if key, ok := flagKeys[f.Name]; ok {
			f.Usage = b.T(key)
		}
	})
}
