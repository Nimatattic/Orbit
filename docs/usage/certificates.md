# Certificates: matching, applying, cleaning up

[← usage index](README.md) · Commands: [`update`](commands.md#orbit-update) ·
[`clean`](commands.md#orbit-clean)

Orbit reads two things — the certificate pool and the live domain inventory — and then
either binds a certificate to a domain or deletes a certificate nothing uses. Both
decisions are made from pure functions in `internal/core` and executed by the provider.

## Matching

A certificate is usable for a domain when the domain matches one of its SANs, following
RFC 6125:

- exact match (`www.example.com`);
- wildcard `*.example.com` matches `a.example.com` but **not** `example.com` and **not**
  `a.b.example.com` — a wildcard covers exactly one label.

When several certificates qualify, they are ranked by:

1. validity (not expired, already valid);
2. specificity (exact SAN beats a wildcard);
3. fewer wildcard labels;
4. longer remaining lifetime (`NotAfter`);
5. newer `InsertTime`;
6. source preference (user uploads before cloud-issued);
7. certificate id, as a stable tie-breaker.

The winner is what `update` binds. The full ranking is deterministic, so two runs on the
same data produce the same plan.

## The plan

`update` never writes before it has a plan. Every domain appears exactly once:

| Row | Meaning |
| --- | --- |
| `apply` | A better certificate exists; a write is planned |
| `up to date` | The domain already holds the winning certificate |
| `no usable certificate` | Nothing in the pool covers the domain (no write possible) |

Reasons are printed per row (expired current certificate, wildcard upgrade, …). Because
"up to date" is computed from the same ranking, re-running `update` after a successful
apply is a no-op — that is what makes it safe in cron.

## Applying

Two execution paths exist, selected by `--strategy` (or `defaults.strategy`):

| Strategy | How it writes | Notes |
| --- | --- | --- |
| `auto` (default) / `product` | CDN: partial config update on `Https.CertInfo.CertId`; EdgeOne: `ModifyHostsCertificate` in `sslcert` mode | `auto` resolves to this path. Touches only the certificate field, leaves the rest of the domain config alone |
| `deploy` | Unified SSL deploy API (`DeployCertificateInstance`), then polls the deploy record | First-class strategy: no extra flag, no config switch. Needs two extra CAM actions — see [permissions.md](permissions.md#ready-made-files) |

`replace` no longer exists: an unknown strategy fails immediately with
`Unknown strategy: <value>` and the accepted list (`auto, deploy, product`).

Writes are grouped **per certificate** (decision T7): everything that targets one
certificate runs as one serialised group, so two strategies can never race each other on
the same certificate. Different certificates run concurrently
(`defaults.concurrency.changes`, default `2`). Inside a group the provider's own ordering
rules win: EdgeOne hosts of one site must go in a single call, so a site is always one
unit. Results are printed in group order so logs stay comparable between runs.

## Verification

Every write is read back from the product:

| Result | Meaning | What to do |
| --- | --- | --- |
| `[ok]` | The product reports the new certificate | nothing |
| `[unverified]` | Write accepted, read-back did not confirm within the budget (2 min) | check with `orbit status`; do not re-run blindly |
| `[failed]` | The cloud rejected the call | read the code + hint, fix, retry |

A timeout is **not** a failure: the write may land after the budget. The budget exists so
a batch run cannot hang on a single domain.

## Cleanup gates

`clean` deletes a certificate only when **all four** gates pass. They are evaluated in
the order below and the **first** failing gate is the reason printed for that certificate —
the order is deliberate: "still in use" is more actionable than "too young", and an
expensive-to-recompute supersession check runs last.

| Gate | Condition | Reason shown when it fails |
| --- | --- | --- |
| **G1** | It is yours: `FilterSource=upload`. Cloud-issued, free and hosted certificates are never deleted | `not_uploaded`, `free`, `hosted` |
| **G2** | Nothing uses it — no local resource points at it **and** the cloud's binding evidence is complete with `TotalCount=0` | `in_use`, `incomplete_evidence`, `no_evidence` |
| **G4** | It is old enough (older than `--older-than`, default `7d`) and matches no `protect_patterns` entry | `too_young`, `protected` |
| **G3** | Every domain it covers already has a **strictly better** certificate | `not_superseded` |

How each gate is decided:

- **G1** — the certificate must be an upload. Free certificates are additionally skipped
  unless `--include-free`; certificates the cloud reports as hosted (`HostingStatus`) are
  skipped too, because Orbit did not create them and cannot reason about their lifecycle.
- **G2** — two independent halves. The local cross-check marks anything a resource points
  at (including every id in a multi-certificate EdgeOne host) as in use; then the
  authoritative association query must be **complete** and report zero bindings.
  `Status=2` (query not finished) and a missing result are *incomplete*, not *unused*:
  Orbit errs towards keeping the certificate. A product that could not be scanned at all
  makes `clean` refuse the whole run with exit `4` instead of guessing;
  `--permit-partial-scan` disables that completeness requirement and is a deliberate,
  auditable risk.
- **G4** runs before G3 on purpose: a candidate that is too young *and* superseded reports
  `too_young`, so the message tells you to wait rather than implying the data is wrong.
  The reference date is `NotBefore` (falling back to `InsertTime`); `protect_patterns`
  globs are matched against the alias, the certificate id and every SAN.
- **G3** — for each domain the certificate covers, the ranking must pick a different
  certificate. A certificate that is still the best for even one of its domains stays.
  "Strictly better" is decided by the same ranking used in `update` (see
  [matching](#matching)), so plan and cleanup can never disagree.

Before each delete Orbit writes `~/.orbit/backups/<account>/<cert>-<stamp>/`:

| File | Mode | Content |
| --- | --- | --- |
| `cert.pem` | 0644 | Server certificate, when exportable |
| `chain.pem` | 0644 | Root chain, when available |
| `key.pem` | **0600** | Private key, when exportable |
| `meta.json` | 0600 | Ids, aliases, SANs, expiry, superseded-by, evidence, run id |

When the cloud refuses to export material (managed certificates usually do), the
metadata is still written and the certificate ids are listed in the output.

## Examples

```bash
# review, then apply
orbit update --ssl --dry-run
orbit update --ssl --accounts=main --yes

# one domain only
orbit update --ssl --domains=www.example.com --yes

# clean up, with a longer grace period
orbit clean --ssl --dry-run --older-than=14d
orbit clean --ssl --yes --older-than=14d --accounts=main

# alert when there is work to do, without writing
orbit update --all-accounts --dry-run --fail-on-changes
```

`--ssl` is optional everywhere: it is still accepted so older scripts keep working, but it
is hidden from `--help` and changes nothing (see
[modes.md](modes.md#--ssl-is-a-compatibility-no-op)).

Every change is appended to `~/.orbit/audit/audit.jsonl`; see the
[operations manual](../OPERATIONS.md#7-日常查看) for queries.
