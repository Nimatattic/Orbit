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

Two strategies exist:

| Strategy | How it writes | Notes |
| --- | --- | --- |
| `product` (default) | CDN: partial config update on `Https.CertInfo.CertId`; EdgeOne: `ModifyHostsCertificate` in `sslcert` mode | Touches only the certificate field, leaves the rest of the domain config alone |
| `deploy` | Unified SSL deploy API, then polls the deploy record | Requires `--enable-experimental-write`; not yet verified against a real account |

Writes are grouped by *(product × target certificate × strategy)* and the groups run
concurrently (`defaults.concurrency.changes`, default `2`). Inside a group the provider's
own rules win: EdgeOne hosts of one site must go in a single call, so a site is always
one unit. Results are printed in group order so logs stay comparable between runs.

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

`clean` deletes a certificate only when **all four** gates pass:

| Gate | Condition |
| --- | --- |
| G1 | The certificate is yours (uploaded by you, not cloud-issued/free/hosted) |
| G2 | A strictly better certificate supersedes it (same coverage, longer validity) |
| G3 | It is older than the grace period (`--older-than`, default `7d`) |
| G4 | No cloud resource references it — from the cloud's binding query, not a guess |

Additionally the binding evidence must be **complete**. If a product could not be scanned
(missing permission, product not enabled), `clean` exits `4` instead of assuming the
certificate is unused. `--permit-partial-scan` overrides this and is a deliberate,
auditable risk.

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
orbit update --ssl --all-accounts --dry-run --fail-on-changes
```

Every change is appended to `~/.orbit/audit/audit.jsonl`; see the
[operations manual](../OPERATIONS.md#7-日常查看) for queries.
