# Execution modes and exit codes

[← usage index](README.md)

Orbit chooses how to behave from the flags **and** from the terminal it is attached to.
The rule is the same for every command, so a cron job and an interactive session never
disagree about what will happen.

## Mode selection

| Situation | Behaviour |
| --- | --- |
| `orbit`, `orbit status`, `orbit dash`, `orbit dns` on a terminal | Full-screen TUI |
| `orbit update --ssl` / `orbit clean --ssl` on a terminal | TUI: scan → plan → confirm → live progress |
| `--yes` | Batch mode: write without prompting (cron, systemd) |
| `--dry-run` | Plan only; never write |
| No TTY (piped, cron, CI) | Plain text; writes still require `--yes` |
| `--no-tui` | Force text even on a terminal |
| `--json` | Structured output on stdout, warnings on stderr |
| `--demo` | Offline fixture provider instead of the cloud |

Write commands (`update`, `clean`, `dns set|rm|enable|disable`) treat *not* being told to
write as a dry-run:

```bash
$ orbit update --ssl
38 changes: 7 to apply, 31 up to date, 0 without a usable certificate
…plan table…
(plan only; pass --yes to write to the cloud)
$ echo $?
0
```

## Progress output

Progress lines go to **stderr** (prefixed with elapsed time), results go to **stdout**,
so `orbit update --ssl --yes --json > out.json` stays clean.

| Flag | Effect |
| --- | --- |
| `--progress` | Force progress lines even when stderr is not a terminal |
| `--no-progress` | Suppress progress lines (recommended in cron) |

## Exit codes

| Code | Meaning | Typical cause |
| --- | --- | --- |
| `0` | Success | Includes no-op runs and dry-runs |
| `1` | Fatal | Bad flags, invalid record, authentication failure, unknown cloud error |
| `2` | Partial | Some accounts/records/changes failed, others succeeded |
| `3` | Pending changes | `--fail-on-changes` found work, or `dns check` has not propagated |
| `4` | Refused by a safety gate | Incomplete evidence, ambiguous account, missing service role |

Scripting pattern — alert on pending changes without writing:

```bash
orbit update --ssl --all-accounts --dry-run --fail-on-changes --json --no-progress
case $? in
  0) echo "nothing to do" ;;
  3) echo "certificates need attention" ;;
  *) echo "orbit failed" ;;
esac
```

## JSON output

Read commands (`status`, `dns zones`, `dns ls`, `dns check`, `account guide`, `account test`)
emit JSON with `--json`. Write commands emit an object per account containing `results`.
Field names are stable; timestamps are RFC 3339 in the account's cloud timezone.

```bash
orbit status --json | jq '.[0] | {account, pending_apply}'
orbit dns ls example.com --json | jq '.[] | select(.Type=="CNAME")'
```

## Language

The UI follows, in order: `--lang`, `ORBIT_LANG`, `language:` in the config file,
`LC_ALL`/`LC_MESSAGES`/`LANG`. POSIX placeholders such as `LC_ALL=C` are treated as
"no preference" and fall through to `$LANG`, so cron does not silently force a language.

```bash
orbit --lang=zh-CN status
orbit --lang=en-US status
```
