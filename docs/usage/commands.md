# Command reference

[← usage index](README.md)

`orbit --help` and `orbit <command> --help` are the source of truth; this page adds the
context the built-in help cannot carry. Behaviour shared by all commands (dry-run,
`--yes`, exit codes, JSON) is described in [modes.md](modes.md).

## Global flags

Accepted by every command:

| Flag | Meaning |
| --- | --- |
| `--config <path>` | Use this configuration file |
| `--local` / `--global` | Read the project `.orbit.yaml` / the global file, and make `config` writes target it |
| `--lang <zh-CN\|en-US>` | Output language |
| `--json` | Structured output on stdout |
| `--demo` | Offline fixture provider (no cloud access) |
| `--no-tui` | Force text output even on a terminal |
| `--progress` / `--no-progress` | Force / suppress progress lines on stderr |
| `--no-cache` | Ignore the cloud association cache |

Account and product targeting is **per command** (`--accounts`, `--all-accounts`,
`--platforms`, `--products`); see [accounts.md](accounts.md).

## `orbit`

```
orbit
```

Status overview. On a terminal this opens the tabbed TUI; without a TTY it prints the
same information as text. Shorthand for `orbit status` when the TUI cannot run.
Details: [tui.md](tui.md#status-overview).

## `orbit status`

Read-only inventory: certificates, resources, pending changes, cleanup candidates.

| Flag | Meaning |
| --- | --- |
| `--accounts a,b` | Accounts to inspect |
| `--all-accounts` | Every registered account |
| `--platforms`, `--products` | Narrow the scan |

```bash
orbit status
orbit status --accounts=main --json | jq '.[0].pending_apply'
```

## `orbit update`

Match certificates to domains and apply them.

| Flag | Meaning |
| --- | --- |
| `--ssl` | Apply SSL certificates (required today) |
| `--dry-run` | Plan only |
| `--yes` | Write without prompting |
| `--domains a.example.com,b.example.com` | Limit to these domains |
| `--strategy auto\|product\|deploy` | `product` uses each product's own API; `deploy` uses the unified SSL deploy API |
| `--enable-experimental-write` | Allow the `deploy` strategy |
| `--fail-on-changes` | Exit `3` when the plan is not empty |
| `--accounts`, `--all-accounts`, `--platforms`, `--products` | Targeting |

```bash
orbit update --ssl --dry-run
orbit update --ssl --accounts=main --domains=www.example.com --yes
orbit update --ssl --all-accounts --yes --json --no-progress
```

Semantics (matching, verification, idempotency): [certificates.md](certificates.md).

## `orbit clean`

Delete certificates no cloud resource uses. Four gates must pass, evidence must be
complete, and a backup is written before every delete.

| Flag | Meaning |
| --- | --- |
| `--ssl` | Clean up SSL certificates |
| `--older-than 7d` | Grace period |
| `--dry-run` / `--yes` | Plan / execute |
| `--include-free` | Also consider free certificates |
| `--permit-partial-scan` | Clean even with incomplete evidence (**dangerous**) |
| `--backup-dir <path>` | Where to write the backup |
| `--accounts`, `--all-accounts`, `--platforms` | Targeting |

```bash
orbit clean --ssl --dry-run
orbit clean --ssl --yes --older-than=7d --accounts=main
```

Gate details: [certificates.md](certificates.md#cleanup-gates).

## `orbit dash`

Console for one account. With several accounts and no `--accounts`, a single-select
picker appears first. Keys and screens: [tui.md](tui.md#console--orbit-dash).

## `orbit dns`

```
orbit dns zones
orbit dns ls <zone>
orbit dns set <zone> <name> <type> <value>
orbit dns rm|enable|disable <zone> <record-id...>
orbit dns check <zone> <name> <type>
orbit dns                      # console
```

`--yes` / `--dry-run` are inherited by every subcommand. Full reference, validation
rules and multi-account behaviour: [dns.md](dns.md).

## `orbit account`

| Subcommand | Purpose |
| --- | --- |
| `add --alias main` | Register and store credentials (no echo, `0600`) |
| `add --alias main --from-stdin` | Non-interactive: two lines on stdin |
| `add --alias main --force` | Overwrite an existing alias |
| `list` | Registered accounts |
| `remove --alias main` | Forget an account |
| `test --accounts=main` | Per-product probes + credential identity |
| `guide` | Permission list and policy document |

Details: [accounts.md](accounts.md), [permissions.md](permissions.md).

## `orbit doctor`

Self-check of configuration, credentials, permissions, locales. Exit `0` when healthy,
`1` when something needs fixing; each line is `ok`, `warn` or `fail`. Warnings are
acceptable (for example the service role before the first write).

## `orbit config`

```bash
orbit config show                  # merged view
orbit config path                  # which files would be read
orbit config defaults.products=cdn,teo --global     # write to ~/.orbit/config.yaml
orbit config defaults.concurrency.changes=4 --local # write ./.orbit.yaml
```

`config set` is **not** a subcommand: pass `key=value` directly. Key names, precedence
and the full file layout: [configuration.md](configuration.md).

## `orbit about`

Identity card: ASCII wordmark, version, platform, accounts, products, locale, paths,
credential file mode, audit status, runtime. Plain text when stdout is not a TTY.

## `orbit version`

Version, commit and build date, injected at build time.

## `orbit completion`

Shell completion (`bash`, `zsh`, `fish`, `powershell`) generated by Cobra:

```bash
orbit completion bash > /etc/bash_completion.d/orbit
```
