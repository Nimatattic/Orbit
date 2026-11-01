# Execution modes and exit codes

[← usage index](README.md)

Orbit chooses how to behave from the flags **and** from the terminal it is attached to.
The rule is the same for every command, so a cron job and an interactive session never
disagree about what will happen.

## Mode selection

| Situation | Behaviour |
| --- | --- |
| `orbit`, `orbit status`, `orbit dash`, `orbit dns` on a terminal | Full-screen TUI |
| `orbit update` / `orbit clean` on a terminal | TUI: scan → plan → confirm → live progress |
| `--yes` | Batch mode: write without prompting (cron, systemd) |
| `--dry-run` | Plan only; never write — **even when `--yes` is also given** |
| `--fail-on-changes` | Plan mode on the CLI even on a terminal; exit `3` when the plan is not empty |
| No TTY (piped, cron, CI) | Plain text; writes still require `--yes` |
| `--no-tui` | Force text even on a terminal |
| `--json` | Structured output on stdout, warnings on stderr |
| `--demo` | Offline fixture provider instead of the cloud |

`--fail-on-changes` is a **query** whose answer is the exit code, so it always stays in
batch/plan mode and never hands the terminal to the TUI (a full-screen view would hide the
answer). `--dry-run`, `--yes` and `--json` keep the CLI for the same reason.

Write commands (`update`, `clean`, `dns set|rm|enable|disable`) treat *not* being told to
write as a dry run:

```bash
$ orbit update
38 changes: 7 to apply, 31 up to date, 0 without a usable certificate
…plan table…
(plan only; pass --yes to write to the cloud)
$ echo $?
0
```

### `--dry-run` wins over `--yes`

A cron line that contains both flags is still a dry run. In dry-run mode the CLI stops at
the plan: no cloud write call, **and no pre-delete backup**, so a dry run leaves the local
filesystem untouched as well. The flag also travels with the request
(`cloud.ApplyOptions.DryRun` / `cloud.DeleteOptions.DryRun`): the provider validates every
change locally, reports it as `ok` **but not verified**, and attaches the message key
`cloud.msg.dry_run` ("Dry run: this change was planned but not written to the cloud").
A provider therefore never has to trust its caller to decide whether writing is allowed.

### `--fail-on-changes`

| Situation | Exit code |
| --- | --- |
| Plan is empty | `0` — and no "pending changes" line is printed |
| Plan has pending writes, `--yes` **not** given | `3` |
| `--yes` given (the run wrote) | `0` / `2` — the query does not apply |

"Pending" is counted across every selected account, so one busy account is enough; an
empty plan never reports changes (that was the old bug that made a quiet weekly cron job
exit `3` forever). The count covers *planned writes*: `update` answers this question,
`clean` has no equivalent flag — read its plan output or use `--dry-run`.
The flag does not write, so it is safe in monitoring.

### `--ssl` is a compatibility no-op

`--ssl` is still **accepted** so older scripts and cron lines keep working, but it is
hidden from `--help` and changes nothing: `orbit update` and `orbit clean` always handle
SSL certificates. New scripts should simply omit it.

## Progress output

Progress lines go to **stderr** (prefixed with elapsed time), results go to **stdout**,
so `orbit update --yes --json > out.json` stays clean.

| Flag | Effect |
| --- | --- |
| `--progress` | Force progress lines even when stderr is not a terminal |
| `--no-progress` | Suppress progress lines (recommended in cron) |

## Exit codes

| Code | Meaning | Typical cause |
| --- | --- | --- |
| `0` | Success | Includes no-op runs, dry-runs, and `--fail-on-changes` on an empty plan |
| `1` | Fatal | Bad flags, invalid record or `--products` value, authentication failure, unknown cloud error; `doctor` when any check is `fail` |
| `2` | Partial | Some accounts/records/changes failed, others succeeded; `account test` when a probe fails |
| `3` | Pending changes | `--fail-on-changes` found plan work; `dns check` has not propagated or could not confirm the answer (a failed lookup is `3`, not `1`) |
| `4` | Refused by a safety gate | Incomplete evidence, ambiguous account, missing service role |

Within `doctor`, a `warn` line is not a failure (exit `0`); only `fail` produces exit `1`.

Scripting pattern — alert on pending changes without writing:

```bash
orbit update --all-accounts --dry-run --fail-on-changes --json --no-progress
case $? in
  0) echo "nothing to do" ;;
  3) echo "certificates need attention" ;;
  *) echo "orbit failed" ;;
esac
```

## JSON output

This section covers the commands that actually emit JSON: `status`, `update`,
`clean`, `account list`/`test`/`guide` and every `dns` subcommand. `version`, `doctor`,
`about`, `config show`/`path`, `completion` and `dash` never produce JSON even with the
flag — the full list lives in [commands.md](commands.md#global-flags).

Every key is **snake_case**, everything is 2-space indented, and **stdout carries JSON
only**. Progress lines, scan warnings, the "done" summary and DNS check diagnostics go to
stderr, and the verdict is the exit code — never a human-readable line mixed into the
document.

| Command | Shape |
| --- | --- |
| `status` | **array**, one object per account: `account`, `certificates`, `uploaded`, `in_use`, `unused_uploaded`, `resources`, `unbound`, `pending_apply`, `foreign_certs`, `warnings` (omitted when the scan was complete) |
| `update` | one object **per account**, concatenated on stdout (a stream of documents, not wrapped in an array): `{account, plan, warnings?}`; with `--yes` an `{account, results}` document follows each plan |
| `clean` | same shape: `{account, clean, warnings?}`, followed by `{account, results}` when it deletes |
| `account list` | **array** of `{account, secret_id, source}`; `[]` when nothing is registered (the secret id is redacted to `AKID***` style) |
| `account test` | **array** of `{account, checks: [{key, status, detail, message_key, error}]}` |
| `dns zones` | **array** of `{account, zone: {id, name, status, dns_status, grade, record_count, effective_ns, updated_on}}` |
| `dns ls` | **array** of `{id, zone, name, type, value, ttl, mx, line, enabled, remark, updated_on, fqdn}` |
| `dns check` | one object: `{zone, fqdn, type, configured, answers, missing}` |
| `dns set` / `rm` / `enable` / `disable` | one object: `{account, action, applied, verified?, records: [...]}` |

The `plan` and `clean` payloads are the CLI's own wire documents (`internal/cli/json.go`),
not the core Go structs:

```json
{
  "account": "main",
  "plan": {
    "account": "main",
    "generated_at": "2026-11-01T09:00:00Z",
    "changes": [
      {
        "account": "main",
        "product": "cdn",
        "resource_id": "www.example.com",
        "resource_name": "www.example.com",
        "domain": "www.example.com",
        "kind": "apply",
        "strategy": "product",
        "instance_key": "www.example.com|on",
        "from": { "cert_id": "c-1001", "alias": "1panel-example-com-2025", "source": "upload", "not_after": "2026-10-02T00:00:00Z" },
        "to": { "cert_id": "c-1002", "alias": "1panel-example-com-2026", "source": "upload", "not_after": "2027-10-07T00:00:00Z" },
        "risk": "high",
        "reasons": ["core.reason.newer_cert_available", "core.reason.current_cert_expired"]
      }
    ]
  }
}
```

`clean` documents are `{account, clean: {account, generated_at, candidates, skipped}}`: a
candidate is `{cert, superseded_by?, evidence}`, a skip is `{cert, reason}` with the same
reason keys the text output prints. An applied run repeats each plan row under
`results[].change` together with `ok`, `verified`, `error_code`, `error`,
`deploy_record_id` and `message_key`.

`warnings` is omitted when the scan was complete; each entry carries `product`,
`action`, `code`, `message`, `scope` and `blocking` (whether the warning would block
`clean`). `message_key` is the raw catalogue key, never a translated string, so `--json`
output is language independent (decision B5).

```bash
orbit status --json | jq '.[0] | {account, pending_apply}'
orbit account test --json | jq '.[] | .checks[] | select(.status!="ok")'
orbit dns ls example.com --json | jq '.[] | select(.type=="CNAME")'
orbit update --dry-run --json | jq '.plan.changes[] | select(.kind=="apply") | .domain'
```

`jq` reads a stream of documents, so the per-account documents of a multi-account
`update`/`clean` work with the same expressions; `--json --yes` therefore produces two
documents per account (plan, then results).

## Language

The UI follows, in order: `--lang`, `ORBIT_LANG`, `language:` in the config file,
`LC_ALL`/`LC_MESSAGES`/`LANG`. POSIX placeholders such as `LC_ALL=C` are treated as
"no preference" and fall through to `$LANG`, so cron does not silently force a language.

```bash
orbit --lang=zh-CN status
orbit --lang=en-US status
```
