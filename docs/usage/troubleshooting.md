# Troubleshooting

[← usage index](README.md) · Exit codes: [modes.md](modes.md#exit-codes)

Find the symptom, then the cause. Every entry ends with the command that confirms the fix.

## Exit codes

| Code | What it usually means | Start here |
| --- | --- | --- |
| `1` | Bad flags, invalid input, authentication failure, unexpected cloud error | read the message; `orbit account test` |
| `2` | Some accounts/records/changes failed, others succeeded | scroll up: every failure is printed with its account |
| `3` | `--fail-on-changes` found work, or `dns check` has not propagated / could not confirm the answer (a failed lookup is `3`, never `1`) | `orbit status`, `orbit dns check` |
| `4` | A safety gate refused to act | the message names the gate |

## Certificates

### `clean` refuses to run (exit `4`)

Deletion needs complete evidence. If a product could not be scanned — no permission,
service not enabled — Orbit refuses to guess which certificates are unused.

```bash
orbit clean --ssl --dry-run          # the warning lines name the product
orbit account test --accounts=main   # which product is inaccessible?
```

Fix the permission. Only if you accept the risk: `--permit-partial-scan`.

### `[unverified]` on a write

The write was accepted but the product did not report the new certificate within the
read-back budget (2 minutes). It is probably applied.

```bash
orbit status --accounts=main         # look at the domain again
```

Do **not** re-run the whole plan blindly; re-running is safe (idempotent) but noisy.

### A CDN domain fails with `FailedOperation.CdnConfigError`

The common cause is that CDN acceleration is switched off for that domain, so the config
change is rejected.

> 域名加速服务已关闭，请先前往域名管理启动域名

Enable the domain under domain management in the console, then retry.

### A TEO domain fails with `UnauthorizedOperation` on a site

EdgeOne permissions are per site. One inaccessible site no longer aborts the product —
the warning names the site. EdgeOne Pages-style sites are skipped with a progress note;
they cannot hold domains.

### Every domain reports `no usable certificate`

The pool has nothing that covers the domain. Check what is actually in the pool and what
the domains need:

```bash
orbit status --accounts=main          # counts + pending
orbit status --accounts=main --json | jq '.[0] | {account, resources, unbound, in_use}'
```

Orbit does not upload certificates; upload them where they are issued (for example your
ACME/1Panel pipeline) and re-run.

## DNS

### `OperationDenied.NoPermissionToOperateDomain`

Not a policy problem: the zone is not in **that account's** DNSPod. With several accounts,
`dns ls` queries all of them and skips the ones that do not host the zone.

```bash
orbit dns ls example.com --accounts=main     # query the owning account
orbit dns zones --accounts=main              # does it appear here at all?
```

If no account lists the zone, it is hosted elsewhere — add it to DNSPod or point `--accounts`
at the right account.

### `dns set` says the record is ambiguous

Several records share the name and type (round-robin). Orbit refuses to pick one:

```bash
orbit dns ls example.com --name www --type A     # see the ids
orbit dns set example.com www A 203.0.113.51 --record-id 42 --yes
```

### `dns check` exits `3`

The record is configured but public DNS does not answer it yet, **or the lookup could not
confirm anything**. Negative caching and TTL make this normal for a few minutes; a refused
or timed-out resolver also lands here, because Orbit reports "unconfirmed" as `3` rather
than claiming the record is broken. `--json` behaves identically (same exit code, with the
lookup diagnostic on stderr and a pure JSON document on stdout).

Only a record that is **not configured at all** in the provider exits `1` — there is
nothing to compare, which is a different problem.

```bash
orbit dns check example.com _acme-challenge TXT
orbit dns check example.com _acme-challenge TXT --server 203.0.113.53
```

### `dns` writes need `--accounts`

Write commands target exactly one account. Pass it explicitly:

```bash
orbit dns set example.com app A 203.0.113.50 --accounts=main --yes
```

## Credentials and permission

### `AuthFailure` / `UnauthorizedOperation`

The credential is wrong or the policy is missing an action.

```bash
orbit account test --accounts=main
orbit account add --alias main --force      # re-enter the pair
```

Then compare with the generated policy — see [permissions.md](permissions.md).

### `credentials.yaml has permissions 0644, want 0600`

Orbit refuses to read a world-readable secret file.

```bash
chmod 600 ~/.orbit/credentials.yaml
```

### Identity differs from the one you granted permissions to

The credential belongs to a different sub-account than the one you attached the policy to.
`orbit account test` prints the identity; compare it in the CAM console.

### The service role is reported as a warning

Expected until the first certificate write: the role cannot be proven read-only. If the
first write fails with `FailedOperation.RoleNotFoundAuthorization`, grant
`SSL_QCSLinkedRoleInReplaceLoadCertificate` in the console.

## Behaviour that surprises people

| Symptom | Explanation |
| --- | --- |
| Two runs of `update` show no changes the second time | Plans are idempotent: the first run applied everything |
| `--demo` writes disappear between commands | Demo state lives in one process, by design |
| Language ignores `$LANG` in cron | `LC_ALL=C` is treated as "no preference" and falls through to `$LANG`; see [modes.md](modes.md#language) |
| Progress lines are missing | Progress goes to stderr and is off when stderr is not a terminal; add `--progress` |
| `status` is slow the first time | The association query is an asynchronous cloud task (up to 5 minutes); `status` reuses the cache for 30 minutes |
| A `dns ls` of one domain queries every account | Intentional: the zone may be in any of them; `--accounts` narrows it |

## Still stuck?

Collect this before asking for help — it contains no secrets:

```bash
orbit version
orbit about            # paths, credential mode, account count
orbit doctor
orbit account test --accounts=main --json
```

Open an issue: <https://github.com/Nimatattic/Orbit/issues>
