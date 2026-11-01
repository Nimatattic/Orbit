# Orbit usage

Everything about *using* Orbit lives in this directory. Other documents
(the [README](../../README.md), [operations manual](../OPERATIONS.md), design docs)
link here instead of repeating commands.

All examples use synthetic data: the account alias `main`, the domains
`example.com` / `example.org`, and the documentation address ranges
`203.0.113.0/24`, `198.51.100.0/24` and `2001:db8::/32`.

## Where to look

| I want to… | Read |
| --- | --- |
| install Orbit and register a first account | [getting-started.md](getting-started.md) |
| understand dry-run vs `--yes`, TUI vs text, exit codes | [modes.md](modes.md) |
| look up a command, a flag or an example | [commands.md](commands.md) |
| match, apply and clean up certificates | [certificates.md](certificates.md) |
| manage DNS zones and records | [dns.md](dns.md) |
| use the interactive screens | [tui.md](tui.md) |
| add accounts, choose credentials, target several accounts | [accounts.md](accounts.md) |
| grant exactly the CAM permissions Orbit needs | [permissions.md](permissions.md) |
| change configuration, understand `~/.orbit` | [configuration.md](configuration.md) |
| fix an error or a surprising result | [troubleshooting.md](troubleshooting.md) |

Deployment (cron, systemd, rollback, audit queries) is covered separately in the
[operations manual](../OPERATIONS.md).

## Command inventory

Every command, in one table. Follow the link for flags and examples.

| Command | What it does | Details |
| --- | --- | --- |
| `orbit` | Status overview of all accounts (TUI on a terminal) | [tui.md](tui.md) |
| `orbit status` | Certificate/resource inventory, pending changes, cleanup candidates | [commands.md](commands.md#orbit-status) |
| `orbit update` | Match certificates to domains and apply them | [certificates.md](certificates.md) |
| `orbit clean` | Delete certificates no cloud resource uses | [certificates.md](certificates.md) |
| `orbit dash` | Console for one account: apply, clean, overview, self-test | [tui.md](tui.md#console--orbit-dash) |
| `orbit dns …` | DNS zones and records (CLI and console) | [dns.md](dns.md) |
| `orbit account add\|list\|remove\|test` | Credentials and access checks | [accounts.md](accounts.md) |
| `orbit account guide` | Minimal permission list and paste-ready policy | [permissions.md](permissions.md) |
| `orbit doctor` | Environment self-check | [commands.md](commands.md#orbit-doctor) |
| `orbit config show\|path`, `orbit config k=v` | Inspect and change configuration | [configuration.md](configuration.md) |
| `orbit about` | Build and environment identity card | [commands.md](commands.md#orbit-about) |
| `orbit version` | Version, commit and build date | [commands.md](commands.md#orbit-version) |

## Conventions used everywhere

- **Writes need `--yes`.** Without it (or with `--dry-run`) a command prints what it
  would change and exits `0`. See [modes.md](modes.md).
- **Accounts are explicit when ambiguous.** `--accounts=main` or `--all-accounts`;
  [accounts.md](accounts.md) explains the rules per command.
- **`--json` makes output machine-readable** on `status`, `update`, `clean`,
  `account list`/`test`/`guide` and every `dns` subcommand. It does **not** produce JSON
  for `version`, `doctor`, `about`, `config show`/`path` or `completion` (they keep their
  human output), and `dash` stays interactive-only.
- **Exit codes carry meaning** (`0` ok, `1` fatal, `2` partial, `3` pending changes,
  `4` refused by a safety gate) — see [modes.md](modes.md#exit-codes).
- **Nothing is stored outside `~/.orbit`** unless you point `ORBIT_HOME` elsewhere;
  see [configuration.md](configuration.md#directory-layout).
