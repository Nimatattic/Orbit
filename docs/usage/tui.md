# Interactive screens

[← usage index](README.md) · Which mode you get: [modes.md](modes.md)

Orbit takes over the terminal only when you are on a TTY **and** none of `--yes`,
`--dry-run`, `--fail-on-changes` or `--json` was given (those flags keep the CLI, because
their output — a written change, a plan, or an exit code — is the answer). Every screen
leaves the terminal exactly as it found it, and text results are printed again after exit
so the scrollback keeps a record.

## Status overview

`orbit` (no arguments) or `orbit status` on a terminal.

```text
╭─ Orbit · status overview ────────────────────────────────────────────────────╮
│Multi-cloud certificate orchestration and inspection                          │
╰──────────────────────────────────────────────────────────────────────────────╯
╭─ registered accounts ────────────────────────────────────────────────────────╮
│▌1 main (7)▐                                                                 │
╰──────────────────────────────────────────────────────────────────────────────╯
╭─ main ───────────────────────────────────────────────────────────────────────╮
│certificates 16 (uploaded 16 / in use 9 / unused 7) · resources 38 …          │
╰──────────────────────────────────────────────────────────────────────────────╯
╭─ to apply (7)   ─────────────────────────────────────────────────────────────╮
│▸ [ ] CDN    static.example.com                       c-1002                  │
╰──────────────────────────────────────────────────────────────────────────────╯
```

| Key | Action |
| --- | --- |
| `↑` `↓` / `j` `k` | Move the cursor in the focused list |
| `space` | Toggle the row under the cursor |
| `a` / `n` | Select all / clear the selection |
| `s` | Switch focus between *to apply* and *cleanup candidates* |
| `u` / `enter` | Apply the selected changes (nothing selected = all) |
| `c` | Clean the selected candidates (nothing selected = all) |
| `tab` / `1`–`9` | Switch account |
| `r` | Rescan the current account |
| `q` / `esc` | Quit |

Accounts are scanned one at a time, so the first tab is usable immediately; a tab shows
`…` while scanning, `!` on failure and `(7)` when it has pending work.

## Console — `orbit dash`

One account, one screen, every action. The console reopens after each function, so a
session reads *apply → back → clean → back → quit*.

```text
╭─ Orbit · console ────────────────────────────────────────────────────────────╮
│account main · products cdn, teo  updated 12:33:53                            │
╰──────────────────────────────────────────────────────────────────────────────╯
╭─ overview ───────────────────────────────────────────────────────────────────╮
│certificates 7 (uploaded 6 / in use 4 / unused 2) · resources 4 …             │
│to apply 4 · cleanup candidates 1 · warnings 0                                │
╰──────────────────────────────────────────────────────────────────────────────╯
╭─ functions ──────────────────────────────────────────────────────────────────╮
│▸ Apply certificates  4 pending                                               │
│  Clean certificates  1 candidates                                             │
│  Status overview (all accounts)                                               │
│  Environment self-test                                                        │
╰──────────────────────────────────────────────────────────────────────────────╯
↑/↓ move  ·  enter run  ·  d checks  ·  r rescan  ·  q quit
```

| Key | Action |
| --- | --- |
| `↑` `↓`, `enter` (or `1`–`4`) | Run the selected function |
| `d` | Expand the self-test (identity, per-product access, service role) |
| `r` | Rescan the account and re-run the self-test |
| `q` | Quit |

If the account is unusable (missing credential, no permission), the console still opens and
shows the failure in the overview and the self-test; write functions are refused, the
read-only overview stays available.

## Run view — `update` / `clean`

```text
╭─ Orbit · update ─────────────────────────────────────────────────────────────╮
│apply SSL certificates   12s                                                  │
╰──────────────────────────────────────────────────────────────────────────────╯
╭─ plan (7) ───────────────────────────────────────────────────────────────────╮
│  apply  CDN  static.example.com  c-1002 → c-1002                             │
╰──────────────────────────────────────────────────────────────────────────────╯
press y / enter to apply, n / esc to cancel (nothing is written)
```

Phases: **scanning** (with the live scan log) → **plan** → **running** (per-row spinners
and elapsed time for writes waiting on the cloud, plus an overall progress bar) →
**result** (✓ effective, ? unconfirmed, ✗ failed).

| Key | Action |
| --- | --- |
| `y` / `enter` | Apply the plan |
| `n` / `esc` | Cancel — nothing is written |
| `q` / `ctrl+c` | Quit (mid-run: cancels the context; already-submitted writes are not rolled back) |

A row that stays `waiting` is **read-back verification**: Orbit polls the product until it
reports the new certificate. It is not stuck; comments on the exact states are in
[certificates.md](certificates.md#verification).

## Permission guide — `orbit account guide`

Three axes — vendor, product scope, usage level — and a live permission list with the
reason for every action.

| Key | Action |
| --- | --- |
| `tab` | Move between the three selectors and the list |
| `↑` `↓` | Move the cursor / scroll the list |
| `space` | Toggle a product, pick a vendor or a usage level |
| `p` | Open the policy view (paste-ready JSON + console steps) |
| `w` | (policy view) Save the JSON to a file, default `./orbit-account-policy.json` |
| `p` / `esc` | (policy view) Back to the guide |
| `q` | Quit |

The policy view owns the whole screen: the JSON must be complete and scrollable, and the
Save prompt accepts a directory (trailing `/`) or `~/…`, creating parent directories and
writing `0600`. See [permissions.md](permissions.md#generating-a-policy).

## DNS console — `orbit dns`

Zone list, record table, and create/edit/delete/enable. Keys:
[dns.md](dns.md#console--orbit-dns).

## Account picker

With several accounts registered, `orbit dash` asks for one (single choice) and
`orbit update` / `orbit clean` ask for one or more (checkbox list). Keys: `↑` `↓`, `space`,
`a` / `n` (multi-select only), `enter`, `esc`. Cancelling writes nothing.

## Universal behaviour

- `ctrl+c` always leaves the program; a completed screen prints its text summary after exit.
- No screen requires more than 60 columns; panels adapt to the terminal width and the
  footer drops the least important hints first.
- `--no-tui` gets you the same information as text, which is what cron should use.
