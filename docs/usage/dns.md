# DNS management

[← usage index](README.md) · Command list: [commands.md](commands.md#orbit-dns)

Orbit manages the records of zones hosted at the provider's DNS service — DNSPod on
Tencent Cloud. Everything is available from the CLI and from a console.

## Concepts

| Term | Meaning |
| --- | --- |
| **zone** | A domain delegated to the DNS service (`example.com`) |
| **record** | One entry: name + type + value (+ TTL, line, priority) |
| **name** | The subdomain: `www`, `*`, or `@` for the zone apex |
| **line** | The resolution line (carrier); `默认` / *default* unless your plan has others |

The FQDN is derived (`www` in `example.com` → `www.example.com`); Orbit normalises case
and trailing dots for you, and rejects a name that already carries the zone suffix.

## Listing

```bash
orbit dns zones                                   # zones, record counts, plan
orbit dns zones --json | jq '.[] | {zone: .zone.Name, records: .zone.RecordCount}'
orbit dns ls example.com                          # every record
orbit dns ls example.com --type CNAME             # filter by type
orbit dns ls example.com --name www               # filter by name
orbit dns ls example.com --keyword cdn            # substring of name/value/remark
```

Several accounts registered? `dns ls <zone>` queries all of them and **skips the ones
that do not host the zone** — DNSPod answers `OperationDenied.NoPermissionToOperateDomain`,
which is an ownership fact, not a permission problem. Pass `--accounts=<alias>` to query
one account. If no account hosts the zone, the command fails with exit code `1`.

## Create or update — `dns set`

```bash
orbit dns set example.com app A 203.0.113.50
orbit dns set example.com app A 203.0.113.50 --yes
orbit dns set example.com _acme-challenge TXT "$TOKEN" --ttl 60 --yes
orbit dns set example.com @ MX mail.example.net --mx 10 --yes
orbit dns set example.com www CNAME static.example.net --ttl 300 --yes
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--ttl` | `600` | Record TTL, 1–86400 |
| `--mx` | `10` | Priority, only sent for `MX` |
| `--line` | provider default | Resolution line |
| `--remark` | empty | Free-form note |
| `--disabled` | off | Create the record disabled |
| `--record-id` | none | Update this record id instead of matching |

`dns set` is **idempotent**: it matches by *(name, type, line)* — an identical record is
left alone, a differing one is updated in place, a missing one is created. Running the same
command twice (or hourly in cron) does not create duplicates.

If **several** records share the name and type (round-robin A/MX, for example), the command
refuses to guess: target one explicitly with `--record-id`, or delete the extras first.

## Delete, enable, disable

```bash
orbit dns rm example.com 42 --yes
orbit dns disable example.com 42 --yes     # keep the record, stop answering it
orbit dns enable  example.com 42 --yes
```

Ids come from `dns ls` (first column). Without `--yes` the command prints what it would
do and exits `0`.

## Propagation check — `dns check`

```bash
orbit dns check example.com _acme-challenge TXT
orbit dns check example.com www CNAME --server 203.0.113.53
```

It reads the configured value from the API, queries public DNS for the same FQDN/type, and
compares them:

```
configured:  some-validation-token
answers:     some-validation-token
_challenge.example.com has propagated
```

Exit codes: `0` propagated, `3` not yet visible (the record exists but has not spread), `1`
the record is not configured at all or the lookup failed. `--server` points the query at a
specific resolver (`host` or `host:port`, default `:53`).

A CNAME query that returns the queried name itself means "no CNAME here" and is reported as
such rather than as a matching answer.

## Validation (before any API call)

| Type | Rule |
| --- | --- |
| `A` | IPv4 address |
| `AAAA` | IPv6 address |
| `CNAME` | hostname target; not allowed at the apex (`@`) |
| `MX` | priority 1–20 + hostname target |
| `TXT` | up to 255 bytes (split longer values or use another type) |
| `NS` | hostname target (sub-delegation) |
| `SRV` | `priority weight port target`, three numbers 0–65535 |
| `CAA` | `flags tag "value"`, tag one of `issue`, `issuewild`, `iodef` |

Names: lowercase letters, digits, `-`, `_`, `*`; each label ≤ 63 characters, name ≤ 253;
`@` addresses the apex. Nothing invalid ever reaches the provider — the tests assert that
no call is made for an invalid record.

## Console — `orbit dns`

On a terminal, `orbit dns` opens the record console:

```text
╭─ hosted zones ──────────────────────────────────────────────────────╮
│▸ example.com                  7        free                          │
│  example.org                  2        pro                           │
╰─────────────────────────────────────────────────────────────────────╯
╭─ records · example.com (7) ─────────────────────────────────────────╮
│    ID      NAME       TYPE   VALUE                    TTL   STATE   │
│▸  1       @          A      203.0.113.10             600   enabled │
│   5       @          MX     mail.example.net         600   enabled │
╰─────────────────────────────────────────────────────────────────────╯
↑/↓ move  ·  ←/→ zone  ·  n new  ·  e edit  ·  d delete  ·  space enable/disable  ·  / filter
```

| Key | Action |
| --- | --- |
| `↑` / `↓` | Move the record cursor |
| `←` / `→`, `tab` | Switch zone |
| `n` | New record (form: name, type, value, TTL, MX, remark) |
| `e` | Edit the selected record |
| `d` then `y` | Delete the selected record (two-step confirmation) |
| `space` | Enable/disable the selected record |
| `/` | Filter by keyword, `enter` applies, `esc` clears |
| `r` | Reload the zone |
| `q` | Quit |

Invalid input keeps the form open and shows the validation error inline. Every write
reloads the zone so the table always reflects the provider.

## Demo mode

```bash
orbit --demo dns zones
orbit --demo dns ls example.com
orbit --demo dns set example.com app A 203.0.113.50 --yes
orbit --demo dns
```

Two zones and nine records are built in. Demo writes live in the process only: a new
invocation starts from the original fixture.

## Permissions

Six `dnspod:*` actions cover everything: `DescribeDomainList` and `DescribeRecordList`
(read), `CreateRecord`, `ModifyRecord`, `ModifyRecordStatus` (write), `DeleteRecord`
(delete). Generate exactly what you need:

```bash
orbit account guide --scope=read  --products=dns --policy   # 2 actions
orbit account guide --scope=full  --products=dns --policy   # 6 actions
```

See [permissions.md](permissions.md).
