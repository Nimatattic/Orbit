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
| `--json` | Machine-readable JSON on stdout — only on the commands listed below |
| `--demo` | Offline fixture provider (no cloud access) |
| `--no-tui` | Force text output even on a terminal |
| `--progress` / `--no-progress` | Force / suppress progress lines on stderr |
| `--no-cache` | Ignore the cloud association cache |

`--json` is honoured by: `status`, `update`, `clean`, `account list`,
`account test`, `account guide`, and every `dns` subcommand (`zones`, `ls`, `check`,
`set`, `rm`, `enable`, `disable`). The flag is still accepted everywhere — it is a
persistent flag — but it is ignored by `version`, `doctor`, `about`, `config show`,
`config path` and `completion` (they keep their human output), and `dash` stays the
interactive console (it fails outside a TTY) whatever `--json` says. The list above is
the authoritative one; `--help` cannot express the difference.

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
| `--ssl` | Accepted for compatibility, hidden from `--help`, **no effect** — omit it |
| `--dry-run` | Plan only; wins over `--yes` |
| `--yes` | Write without prompting |
| `--domains a.example.com,b.example.com` | Limit to these domains |
| `--strategy auto\|product\|deploy` | `auto`/`product` use each product's own API; `deploy` uses the unified SSL deploy API. All three are supported with no extra switch; `replace` is gone and fails with `Unknown strategy` |
| `--fail-on-changes` | Plan-mode query: exit `3` only when writes are pending and `--yes` was not given |
| `--accounts`, `--all-accounts`, `--platforms`, `--products` | Targeting |

```bash
orbit update --dry-run
orbit update --accounts=main --domains=www.example.com --yes
orbit update --all-accounts --yes --json --no-progress
```

`--json` prints one document per account, `{account, plan, warnings?}`; with `--yes` a
second document `{account, results}` follows each plan. Every key is snake_case
(`plan.changes[].kind`, `to.cert_id`, `results[].verified`, …) and stdout carries nothing
but JSON — progress, warnings and the "done" summary go to stderr, and the exit code is the
verdict. `warnings` is omitted when the scan was complete — see
[modes.md](modes.md#json-output).

Semantics (matching, verification, idempotency): [certificates.md](certificates.md).

## `orbit clean`

Delete certificates no cloud resource uses. Four gates must pass, evidence must be
complete, and a backup is written before every delete.

| Flag | Meaning |
| --- | --- |
| `--ssl` | Accepted for compatibility, hidden from `--help`, **no effect** — omit it |
| `--older-than 7d` | Grace period (gate G4); `0` disables the age check |
| `--dry-run` / `--yes` | Plan / execute |
| `--products a,b` | Narrow the scan, validated like `update`/`status` (bad value exits `1`) |
| `--include-free` | Also consider free certificates |
| `--permit-partial-scan` | Clean even with incomplete evidence (**dangerous**) |
| `--backup-dir <path>` | Where to write the backup |
| `--accounts`, `--all-accounts`, `--platforms` | Targeting |

```bash
orbit clean --dry-run
orbit clean --yes --older-than=7d --accounts=main
orbit clean --products=cdn --all-accounts --dry-run
```

`--json` prints `{account, clean, warnings?}` per account, followed by
`{account, results}` when it deletes. Gate details:
[certificates.md](certificates.md#cleanup-gates).

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
| `list` | Registered accounts; `--json` emits an array of `{account, secret_id, source}`, `[]` when empty |
| `remove --alias main` | Forget an account |
| `test --accounts=main` | Per-product probes + credential identity; `--json` emits `{account, checks:[…]}` with untranslated `message_key` |
| `guide` | Permission list and policy document |

There is **no `--secret-id-env` flag**: `account add` reads the pair interactively, from
stdin (`--from-stdin`), or overwrites an alias with `--force`. Environment variables are a
*config* choice (`credential.source: env`), not a flag.

Details: [accounts.md](accounts.md), [permissions.md](permissions.md).

## `orbit doctor`

Self-check of configuration, credentials, permissions, locales. Every line is `ok`,
`warn` or `fail`: any `fail` makes the command exit `1`, while `warn` lines still exit
`0` (for example the service role before the first write, or a config file that does not
exist yet). The full check list is always printed before the exit code is decided.

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
