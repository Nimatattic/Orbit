# Accounts and credentials

[← usage index](README.md)

Orbit keeps its own credential store. It **never** reads `~/.tccli`, and it refuses to run
if `~/.orbit/credentials.yaml` is readable by anyone but you.

## Register an account

```bash
orbit account add --alias main          # interactive: SecretId, SecretKey (no echo), confirm
orbit account add --alias main --from-stdin   # non-interactive: SecretId\nSecretKey on stdin
orbit account add --alias main --force  # replace an existing alias
```

```bash
printf '%s\n%s\n' "$SECRET_ID" "$SECRET_KEY" | orbit account add --alias backup --from-stdin
```

What it does: stores the pair in `~/.orbit/credentials.yaml` (`0600`) **and** registers the
alias in `~/.orbit/config.yaml`, so `list`, `doctor` and every account selector see it.

## List and remove

```bash
orbit account list
orbit account list --json
orbit account remove --alias backup
```

## Two ways to hold secrets

| Source | Where the secret lives | How to set it up |
| --- | --- | --- |
| `store` (default) | `~/.orbit/credentials.yaml`, mode `0600` | `orbit account add --alias main` |
| `env` | Never stored; read from two environment variables at run time | declare the account in `config.yaml` with `credential.source: env` |

```yaml
platforms:
  tencent:
    accounts:
      - alias: main
        credential:
          source: env
          secret_id_env: ORBIT_TENCENT_MAIN_SECRET_ID
          secret_key_env: ORBIT_TENCENT_MAIN_SECRET_KEY
```

`env` is the right choice for containers and CI: the file cannot leak what it never had.
See [configuration.md](configuration.md#credentials).

## Check what a credential can do

```bash
orbit account test --accounts=main          # per-product probes + identity
orbit account test --accounts=main --json
orbit doctor                                # config, credentials, permissions, locales
```

`account test` prints one check per product (`SSL`, `CDN`, `EdgeOne`, `DNSPod`) and the
identity the credential belongs to (`AccountId`, identity type). The service-role line
stays a warning until the first certificate write — the role cannot be probed read-only.

A failure here is about the credential or its CAM policy, not about Orbit: see
[permissions.md](permissions.md) and [troubleshooting.md](troubleshooting.md).

## Choosing accounts per command

| Command | Default | Flags |
| --- | --- | --- |
| `status`, `dns zones` | every registered account | `--accounts`, `--all-accounts` |
| `update`, `clean` | **must be explicit** when several exist (exit `4` otherwise) | `--accounts`, `--all-accounts` |
| `dns set/rm/enable/disable` | exactly one account | `--accounts` (one alias only) |
| `dns ls` | every account, skipping zones it does not host | `--accounts` |
| `dash`, `dns` (console) | picker when several exist | `--accounts` to skip the picker |
| `account test` | every registered account | `--accounts`, `--all-accounts` |

Targeting also narrows the scan: `--platforms`, `--products` (see
[commands.md](commands.md)).

## Rotation

```bash
orbit account add --alias main --force      # overwrite the stored pair
orbit account test --accounts=main          # confirm the new key works
```

Rotating with `source: env` means updating the environment and restarting whatever holds
it — the file never changes.

## Where the data lives

| Path | Mode | Content |
| --- | --- | --- |
| `~/.orbit/config.yaml` | 0644 | aliases, products, defaults (no secrets) |
| `~/.orbit/credentials.yaml` | **0600** | SecretId/SecretKey pairs |
| `~/.orbit/audit/audit.jsonl` | 0600 | one line per change, never a secret |

Set `ORBIT_HOME` to relocate all of it (see [configuration.md](configuration.md)).
