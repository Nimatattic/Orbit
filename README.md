<div align="center">

```text
         .-~~~~~~~-.
      .-~           ~-.
     /                 \
    |         o         |
     \                 /
      ~-.           .-~
         `-~~~~~~~-'

  ___  ____  ____  ___ _____
 / _ \|  _ \| __ )|_ _|_   _|
| | | | |_) |  _ \ | |  | |
| |_| |  _ <| |_) || |  | |
 \___/|_| \_\____/|___|  |_|

      cloud certificate orchestration
```

**Match certificates to your domains, apply them, and retire the leftovers — from the terminal.**

[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/platform-Tencent%20Cloud-006EFF)](docs/TENCENT_API_NOTES.md)
[![TUI](https://img.shields.io/badge/TUI-Bubble%20Tea-FF75B7)](https://github.com/charmbracelet/bubbletea)
[![i18n](https://img.shields.io/badge/i18n-English%20%7C%20%E7%AE%80%E4%BD%93%E4%B8%AD%E6%96%87-4B8BBE)](locales)
[![Status](https://img.shields.io/badge/status-alpha-orange)](docs/IMPLEMENTATION_PLAN.md)

[![Stars](https://img.shields.io/github/stars/Nimatattic/Orbit?style=flat&color=yellow)](https://github.com/Nimatattic/Orbit/stargazers)
[![Last commit](https://img.shields.io/github/last-commit/Nimatattic/Orbit)](https://github.com/Nimatattic/Orbit/commits/main)
[![Issues](https://img.shields.io/github/issues/Nimatattic/Orbit)](https://github.com/Nimatattic/Orbit/issues)

[Quick start](#quick-start) · [Installation](#installation) · [Usage](#usage-guide) · [Permissions](#permissions) · [How to dev](#how-to-dev) · [Docs](#documentation)

</div>

---

## Why Orbit

Most teams already automate the *easy* half of certificate management: ACME issues the
certificate and something pushes it to the cloud. What is left is the part nobody wants to
script — **pointing the right certificate at the right domain, and deleting the ones that no
longer serve anything.**

That is the gap Orbit fills. It reads the certificate pool and the live domain inventory,
decides per domain which certificate *should* be bound, applies it, verifies that the change
came back from the cloud, and — separately, behind four gates — removes certificates that no
cloud resource references any more.

```text
1Panel / ACME ──▶ local dir ──▶ upload to cloud ──▶ ┌─────────────────────┐
                                                    │ apply to domains    │ ◀── Orbit
                                                    │ delete orphans      │ ◀── Orbit
                                                    └─────────────────────┘
```

Orbit deliberately stops at the dotted line: it does not issue certificates and does not read
`tccli` credentials. It is the missing step in *your* pipeline, not another CA client.

## Features

| | |
| --- | --- |
| **Matching that respects RFC 6125** | Exact SANs, single-label wildcards (`*.example.com` matches `a.example.com`, never `a.b.example.com`), validity-aware ranking, deterministic tie-breaking. |
| **Idempotent plans** | `update` builds a plan first; domains already holding the best certificate are marked *up to date* and never written twice. |
| **Product-aware writes** | CDN uses a partial config update (`ModifyDomainConfig` on `Https.CertInfo.CertId`); EdgeOne uses `ModifyHostsCertificate` in `sslcert` mode. Groups run concurrently. |
| **Read-back verification** | Every write is confirmed by polling the product until the domain reports the new certificate. A timeout is reported as `unverified`, never as a silent success. |
| **Four gates before deletion** | Uploaded by you · superseded by another certificate · beyond the grace period · referenced by no cloud resource. Deletion is refused unless the evidence is complete. |
| **Backup before delete** | Certificate, chain and private key (mode `0600`) plus a metadata record, written before the API call. |
| **Append-only audit log** | One JSONL record per change plus a per-run summary. Never contains credentials. |
| **Honest semantics** | Dry-run by default, `--yes` required to write, exit codes that distinguish *pending changes* from *partial failure* from *safety refusal*. |
| **A real TUI** | Status overview, per-account console, permission guide, live apply progress with per-domain state. |
| **Static binary** | `CGO_ENABLED=0`, one file, no runtime dependencies. English and 简体中文 at parity. |

## Quick start

No cloud account, no credentials, no network — the demo provider is fully offline:

```bash
git clone https://github.com/Nimatattic/Orbit.git
cd Orbit
make build

./bin/orbit --demo status                # inventory + plan, read-only
./bin/orbit --demo update --ssl          # TUI on a terminal, plan-only otherwise
./bin/orbit --demo update --ssl --dry-run
```

```text
╭─ Orbit · status overview ────────────────────────────────────────────────────────────────────╮
│Multi-cloud certificate orchestration and inspection                                          │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ registered accounts ────────────────────────────────────────────────────────────────────────╮
│▌1 demo (4)▐                                                                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ demo ───────────────────────────────────────────────────────────────────────────────────────╮
│certificates 7 (uploaded 6 / in use 4 / unused 2) · resources 4 (unbound 2 · foreign 1)       │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ to apply (4)   ─────────────────────────────────────────────────────────────────────────────╮
│▸ [ ] CDN    static.example.com                       c-1002                                  │
│  [ ] CDN    www.example.com                          c-1002                                  │
│  [ ] TEO    api.example.com                          c-1003                                  │
│  [ ] TEO    cdn.example.com                          c-1002                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ cleanup candidates (1)   ───────────────────────────────────────────────────────────────────╮
│  [ ] SSL    1panel-test-example-com-2025             c-1005                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯

↑/↓ move  ·  space toggle  ·  a all  ·  n none  ·  u apply  ·  c cleanup  ·  s list  ·  q quit
```

## Installation

### Prebuilt static binaries

```bash
make release
# dist/orbit-linux-amd64   dist/orbit-linux-arm64   dist/orbit-darwin-arm64

scp dist/orbit-linux-amd64 root@server:/usr/local/bin/orbit
ssh root@server 'chmod 755 /usr/local/bin/orbit && orbit version'
```

### From source

Requires **Go 1.27+** and `make`. CGO is not needed.

```bash
git clone https://github.com/Nimatattic/Orbit.git
cd Orbit
make build            # -> bin/orbit
make install          # -> /usr/local/bin/orbit
```

> The Go module path is `github.com/Crequency/orbit`, so `go install` against the GitHub URL
> does not work yet. Use the clone-and-build path above.

### First steps

```bash
orbit account add --alias main      # secrets are read without echo and stored 0600
orbit doctor                        # config, credentials, access and locale self-check
orbit account test --accounts=main  # per-product probes and credential identity
orbit about                         # build, paths, credential and audit status
```

`orbit about` is the fastfetch-style identity card:

```text
         .-~~~~~~~-.           Orbit v0.1.0
      .-~           ~-.        a1b2c3d · 2026-10-18T04:00:00Z
     /                 \       ──────────────────────────────────────────
    |         o         |      platform     Tencent Cloud
     \                 /       accounts     2 (main, backup)
      ~-.           .-~        products     ssl, cdn, teo
         `-~~~~~~~-'           locale       en-US
                               data         ~/.orbit
  ___  ____  ____  ___ _____   config       ~/.orbit/config.yaml
 / _ \|  _ \| __ )|_ _|_   _|  credentials  ~/.orbit/credentials.yaml  0600
| | | | |_) |  _ \ | |  | |    audit        ~/.orbit/audit/audit.jsonl  12 runs
| |_| |  _ <| |_) || |  | |    runtime      go1.27.1 linux/amd64
 \___/|_| \_\____/|___|  |_|
```

## Usage Guide

### Modes

Orbit decides how to behave from the flags *and* the terminal:

| You run | What happens |
| --- | --- |
| `orbit` / `orbit status` on a TTY | Full-screen TUI, one tab per account |
| `orbit update --ssl` on a TTY | TUI: scan → plan → confirm with `y` → live progress |
| `orbit update --ssl --yes` | Batch mode: writes without prompting (cron, systemd) |
| `orbit update --ssl --dry-run` | Plan only, never writes |
| any command without a TTY | Plain text; without `--yes`, `update` only prints the plan |
| `--no-tui` / `--json` | Force text / structured output |

### Commands

| Command | Purpose |
| --- | --- |
| `orbit` | Status overview across all accounts (TUI on a terminal) |
| `orbit dash` | Console for one account: apply, clean, overview, self-test |
| `orbit status` | Certificate and resource inventory, pending changes, cleanup candidates |
| `orbit update --ssl` | Match certificates to domains and apply them |
| `orbit clean --ssl` | Delete certificates that no cloud resource uses |
| `orbit account add \| list \| remove \| test` | Manage credentials and verify access |
| `orbit account guide` | Choose platform/products/scope → minimal permission list + paste-ready policy |
| `orbit doctor` | Environment self-check |
| `orbit config show \| set` | Inspect and change configuration |
| `orbit about` | Identity card: logo, build, paths, audit status |
| `orbit version` | Version, commit and build date |

### The console — `orbit dash`

One account, one screen, every action. The console reopens after each function, so a session
reads as *apply → back → clean → back → quit*. If an account is unusable (missing credential),
the console still opens and shows the failure instead of failing at the door.

```text
╭─ Orbit · console ────────────────────────────────────────────────────────────────────────────╮
│account demo · products cdn, teo  updated 04:03:26                                            │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ overview ───────────────────────────────────────────────────────────────────────────────────╮
│certificates 7 (uploaded 6 / in use 4 / unused 2) · resources 4 (unbound 2 · foreign 1)       │
│to apply 4 · cleanup candidates 1 · warnings 0                                                │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ functions ──────────────────────────────────────────────────────────────────────────────────╮
│▸ Apply certificates  4 pending                                                               │
│  Clean certificates  1 candidates                                                            │
│  Status overview (all accounts)                                                              │
│  Environment self-test                                                                       │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯

↑/↓ move  ·  enter run  ·  d checks  ·  r rescan  ·  q quit
```

### The permission guide — `orbit account guide`

Stop guessing which CAM actions to grant. Choose the vendor, the products and how far you want
Orbit to go; the guide prints the exact action list, why each one is needed, and a policy
document you can paste into **CAM console → Policies → Create custom policy → by policy
syntax**.

```bash
orbit account guide                                  # TUI: p shows the JSON, w saves it
orbit account guide --scope=read --products=cdn      # plain text
orbit account guide --scope=full --policy > policy.json
```

```text
╭─ 3. Usage level ─────────────────────────────────────────────────────────────────────────────╮
│  (•) Inspection only  status / doctor / --dry-run, never writes                              │
│  ( ) Apply certificates  update --yes: deploy certificates to domains                        │
│  ( ) Full (apply + cleanup)  clean --yes: delete certificates no resource uses               │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ 9 permissions needed ───────────────────────────────────────────────────────────────────────╮
│read    cdn:DescribeDomainsConfig                  read CDN domains and their HTTPS certifica…│
│read    ssl:CreateCertificateBindResourceSyncTask  start the "which resources use this certif…│
│read    ssl:DescribeCertificates                   list the certificate pool for matching and…│
│read    sts:GetCallerIdentity                      (no grant needed) verify which account the…│
│… 2 more, scroll down                                                                         │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
tab list  ·  ↑/↓ move  ·  space toggle  ·  p show policy JSON  ·  q quit
```

Every grantable action cites the official Tencent Cloud interface list it comes from
(`ssl`, `cdn`, `teo`). `sts:GetCallerIdentity` appears in the list but is **never written into
the policy**: it needs no grant, and CAM rejects a document containing an unknown action.
Ready-made policies live in [`docs/cam/`](docs/cam).

### Applying certificates — `orbit update`

```bash
orbit update --ssl --dry-run                                  # review the plan
orbit update --ssl --accounts=main --domains=www.example.com --yes
orbit update --ssl --all-accounts --yes --json --no-progress  # cron
```

Groups *(product × target certificate × strategy)* run concurrently; results are printed in
group order so a cron log stays diff-able. Each domain reports one of:

| Result | Meaning |
| --- | --- |
| `[ok]` | Written **and** confirmed by reading the product back |
| `[unverified]` | Write accepted, but read-back did not confirm within the budget — check `orbit status`, do not blindly re-run |
| `[failed]` | The cloud rejected the call; error code plus an actionable hint are printed |

### Cleaning up — `orbit clean`

```bash
orbit clean --ssl --dry-run            # candidates and the reason for every skip
orbit clean --ssl --yes --older-than=7d
```

A certificate is only deleted when **all four gates** pass, the cloud's binding query returned
complete evidence, and the backup was written. Everything else is reported as a skipped
candidate with its reason — never silently ignored.

### TUI keys

| Key | Action |
| --- | --- |
| `↑` `↓` / `j` `k` | Move the cursor |
| `space` | Toggle the row under the cursor |
| `a` / `n` | Select all / clear the selection |
| `s` | Switch between the *to apply* and *cleanup* lists |
| `u` / `enter` | Apply the selected changes (nothing selected = all) |
| `c` | Clean the selected candidates (nothing selected = all) |
| `tab` / `1`–`9` | Switch account |
| `r` | Rescan the current account |
| `p` / `w` | (guide) Show the policy JSON / save it to a file |
| `q` / `esc` | Quit / cancel without writing |

### Exit codes

Designed for cron and CI:

| Code | Meaning |
| --- | --- |
| `0` | Success, including no-op runs and dry-runs |
| `1` | Fatal error (bad config, auth failure, unexpected cloud error) |
| `2` | Partial failure — some changes succeeded, some did not |
| `3` | Pending changes detected (`--fail-on-changes`) |
| `4` | Refused by a safety gate (incomplete evidence, ambiguous account, missing role) |

```bash
# Alert when the weekly plan is non-empty, without writing anything:
orbit update --ssl --all-accounts --dry-run --fail-on-changes --json --no-progress
```

## Configuration

Everything lives in `~/.orbit/` (override with `ORBIT_HOME`):

| Path | Contents | Mode |
| --- | --- | --- |
| `config.yaml` | Accounts, products, concurrency, cleanup policy | `0644` |
| `credentials.yaml` | Secrets (**never** read from `tccli`) | `0600` enforced |
| `audit/audit.jsonl` | Append-only change log | `0600` |
| `backups/<account>/<cert>-<stamp>/` | `cert.pem`, `chain.pem`, `key.pem`, `meta.json` | dir `0700`, key `0600` |
| `cache/` | Reserved for status caching | — |

```yaml
language: en-US
defaults:
  products: [cdn, teo]
  concurrency:
    changes: 2            # write groups in flight
  clean:
    older-than: 7d
    require-full-evidence: true
  edgeone:
    replace-free-cert: true
platforms:
  tencent:
    accounts:
      - alias: main
        credential:
          source: env     # store | env | file
          secret-id-env: ORBIT_TENCENT_MAIN_SECRET_ID
          secret-key-env: ORBIT_TENCENT_MAIN_SECRET_KEY
```

Configuration precedence is local file → global file → defaults; `--local` / `--global` choose
which file `orbit config set` writes.

## Permissions

Three least-privilege tiers, generated from the code and guarded by a test that reflects over
the provider's SDK interfaces:

| File | Actions | Use it for |
| --- | --- | --- |
| [`docs/cam/readonly.json`](docs/cam/readonly.json) | 8 | `status`, `doctor`, `account test`, every `--dry-run` |
| [`docs/cam/execute.json`](docs/cam/execute.json) | 10 | `orbit update --ssl --yes` |
| [`docs/cam/delete.json`](docs/cam/delete.json) | 13 | `orbit clean --ssl --yes` |

Recommended: a **read-only** API account on the machine that inspects, and a separate
**execute** account on the machine that writes. One console-side grant is also required: the
service role `SSL_QCSLinkedRoleInReplaceLoadCertificate`.

## Safety model

- **Dry-run by default.** Writing requires `--yes`; without a TTY the plan is printed and the
  process exits `0`.
- **Plans are idempotent** and explain every row.
- **Deletes are gated, evidenced and backed up** — see [Features](#features).
- **Refusals are loud.** Incomplete evidence, ambiguous accounts, a missing service role: all
  exit `4` rather than doing something plausible.
- **The audit log is append-only** and records before/after certificate IDs, so "what changed
  last Tuesday" is answerable without the cloud.

## How to Dev

```bash
source ../.toolchains/env.sh     # if you use the workspace-managed toolchain
make build                       # bin/orbit
make test                        # full suite, fully offline
make vet                         # go vet
make lint                        # vet + golangci-lint when installed
make release                     # three static binaries into dist/
```

The test suite never touches the network. The cloud is reached through narrow
`sslAPI` / `cdnAPI` / `teoAPI` / `stsAPI` interfaces, which tests implement with fakes:
pagination, retries, half-open poll loops, permission errors and connection reuse are all
replayed locally. TUI screens are Bubble Tea models, so their state machines are unit-tested
without a terminal.

| Package | What is covered |
| --- | --- |
| `internal/core` | Matching, ranking, plan building, cleanup gates |
| `internal/cloud/tencent` | SDK interaction, error mapping, permission table, policy generation |
| `internal/render/tui` | Model state machines, layout geometry (including CJK widths) |
| `internal/cli` | Flags, mode selection, output formats, backups, audit |
| `internal/config`, `internal/credential`, `internal/i18n` | Merge precedence, secret storage, catalogue parity |

Working on the UI? `--demo` gives you a complete fake account with certificates, domains,
pending changes and cleanup candidates:

```bash
./bin/orbit --demo status
./bin/orbit --demo update --ssl --progress
./bin/orbit --demo dash
```

## Structure

```text
cmd/orbit/                 entry point (thin main)
internal/
  cli/                     commands, flags, mode selection, output, about card
  core/                    pure domain logic: model, matching, plan, safety gates
  cloud/                   Provider interface + offline demo provider
    tencent/               Tencent Cloud: SSL, CDN, EdgeOne, STS; permissions & policies
  render/
    plain/                 text output for scripts
    tui/                   Bubble Tea screens: console, status, run, guide, picker
  audit/                   append-only JSONL run log
  config/                  layered config, paths, defaults
  credential/              secret storage (0600) and resolution
  i18n/                    bundle + locale negotiation (en-US, zh-CN)
  exitcode/                exit-code contract and error wrapping
  tty/  version/           terminal detection, build metadata
locales/                   embedded message catalogues (key parity enforced by test)
docs/                      design, operations, API notes, verification guides, CAM policies
scripts/                   cron / systemd examples
testdata/                  fixtures
```

## Documentation

| Document | What it covers |
| --- | --- |
| [docs/OPERATIONS.md](docs/OPERATIONS.md) | Install, cron/systemd, permissions, rollback, troubleshooting |
| [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) | Architecture, milestones, interface inventory |
| [docs/TENCENT_API_NOTES.md](docs/TENCENT_API_NOTES.md) | Verified cloud behaviour and every open question |
| [docs/VERIFY_M1.md](docs/VERIFY_M1.md) | Step-by-step verification against a real account |
| [DECISIONS.md](DECISIONS.md) | Decision log: every non-obvious choice and its reason |

## Roadmap

- [x] **M0–M2** — skeleton, read-only inventory against a real account, matching engine
- [x] **M3–M4** — apply with read-back verification, gated cleanup with backups and audit
- [x] **M5** — TUI: overview with tabs, per-account console, permission guide
- [x] **M6** — static releases, operations manual, CAM templates, `orbit about`
- [ ] Additional providers (Alibaba Cloud, AWS)
- [ ] Per-domain certificate pairs (RSA + SM2) instead of a single binding
- [ ] `orbit restore` — re-upload a certificate straight from `~/.orbit/backups`
- [ ] CI (vet + tests + release) and signed tags

## FAQ

<details>
<summary><b><code>clean</code> refuses to run — why?</b></summary>

Deletion needs complete evidence. If a product could not be scanned (no permission, service not
enabled), `clean` exits `4` instead of guessing. Fix the permission, or explicitly accept the
risk with `--permit-partial-scan`.
</details>

<details>
<summary><b>Why is <code>sts:GetCallerIdentity</code> missing from the generated policy?</b></summary>

Because it needs no grant, and CAM rejects a policy containing an unknown action — one bad
entry invalidates the whole document. The identity probe works without it.
</details>

<details>
<summary><b>What does <code>[unverified]</code> mean?</b></summary>

The write was accepted but the product did not report the new certificate within the read-back
budget (2 minutes). The change is probably applied; confirm with `orbit status` instead of
re-running blindly.
</details>

<details>
<summary><b>A CDN domain fails with <code>FailedOperation.CdnConfigError</code></b></summary>

The usual cause is that CDN acceleration is switched off for that domain. Enable it under
domain management in the console, then retry. Orbit maps the cloud message to that hint.
</details>

<details>
<summary><b>Why does Orbit not read my <code>tccli</code> credentials?</b></summary>

Deliberate: Orbit keeps its own store with `0600` and refuses to start when the file is more
permissive. Silently sharing another tool's credential cache is how secrets end up in the wrong
logs.
</details>

## Contributing

Issues and pull requests are welcome. Before opening one:

1. `make build && make test && make vet` must pass.
2. Keep the layering: pure logic in `internal/core`, cloud specifics in `internal/cloud`,
   presentation in `internal/render`.
3. If you touch a cloud API, update the permission table — a test reflects over the SDK
   interfaces and reports exactly which action is undocumented.
4. If you change user-visible text, add the key to **both** locales; parity is enforced.
5. Prefer a test over a comment. The suite is offline and fast.

## Security

- No credentials, tokens or private keys are stored in this repository.
- `~/.orbit/credentials.yaml` must be `0600`; Orbit refuses to start otherwise.
- Audit records never contain secrets — only identifiers and outcomes.
- Report vulnerabilities privately via GitHub **Security → Report a vulnerability** rather than
  a public issue.

## License

No license file has been added yet. Until one is, the code is all-rights-reserved. **Choose a
license before publishing** — MIT is the simplest option for a tool like this; Apache-2.0 adds
an explicit patent grant.

## Star History

<a href="https://star-history.com/#Nimatattic/Orbit&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=Nimatattic/Orbit&type=Date&theme=dark" />
    <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=Nimatattic/Orbit&type=Date" />
    <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=Nimatattic/Orbit&type=Date" width="600" />
  </picture>
</a>

## Acknowledgements

- [Tencent Cloud SDK for Go](https://github.com/TencentCloud/tencentcloud-sdk-go) — the API surface Orbit drives
- [Charm](https://charm.sh) — [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), [Lip Gloss](https://github.com/charmbracelet/lipgloss)
- [Cobra](https://github.com/spf13/cobra) — command structure
- [1Panel](https://1panel.cn) — the certificate workflow that motivated this tool

<div align="center">
<sub>Built with Go. Ships as one static binary. Never touches a credential it did not store itself.</sub>
</div>
