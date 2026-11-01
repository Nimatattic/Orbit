# Configuration

[← usage index](README.md) · Command: [`orbit config`](commands.md#orbit-config)

## Directory layout

Everything lives under `ORBIT_HOME` (default `~/.orbit`):

| Path | Mode | Content |
| --- | --- | --- |
| `config.yaml` | 0644 | accounts, products, defaults — **no secrets** |
| `credentials.yaml` | **0600** | SecretId/SecretKey pairs (enforced on read) |
| `audit/audit.jsonl` | 0600 | append-only change log |
| `backups/<account>/<cert>-<stamp>/` | dir 0700 | `cert.pem`, `chain.pem`, `key.pem` (0600), `meta.json` |
| `cache/` | — | **does not exist**: nothing creates it. `status` reuses the cloud's 30-minute association cache (disable with `--no-cache`), there is no local cache file |

```bash
orbit config path          # which files are read, in order
orbit config show          # the merged result
ORBIT_HOME=/srv/orbit orbit status
```

## Precedence

`--config <path>` > `./.orbit.yaml` > `~/.orbit/config.yaml` > built-in defaults.

Higher-precedence layers are merged **over** the lower ones, key by key. `--local` /
`--global` pick the file that `orbit config` writes to.

## Writing keys

```bash
orbit config defaults.products=cdn,teo --global        # ~/.orbit/config.yaml
orbit config defaults.concurrency.changes=4 --local    # ./.orbit.yaml
orbit config language=en-US --global
```

`config set` is not a subcommand — pass `key=value` directly. Keys written this way are
validated: an unknown name exits `1`. A **hand-written** `config.yaml` is decoded
non-strictly, though, so an unknown key in the file is silently ignored; verify the merged
result with `orbit config show`.

## Full example

```yaml
version: 1
language: en-US
defaults:
  platforms: [tencent]
  products: [cdn, teo]
  strategy: product          # auto | product | deploy (auto = each product's own API)
  concurrency:
    products: 3
    changes: 2               # write groups in flight
  timeouts: {}               # provider defaults apply
  cert_preference: []        # e.g. [upload, trustasia]
  domain_match: {}
  edgeone:
    replace_free_cert: true  # switch EdgeOne free certs to pool certificates
  clean:
    older_than: 7d
    require_full_evidence: true
    include_free: false
    protect_patterns: []     # certificate aliases never deleted
platforms:
  tencent:
    accounts:
      - alias: main
        credential:
          source: store      # store | env
        products: [cdn, teo] # optional per-account override
```

## Credentials

Two sources, described in [accounts.md](accounts.md#two-ways-to-hold-secrets):

```yaml
      - alias: main
        credential:
          source: store      # secrets in ~/.orbit/credentials.yaml (0600)

      - alias: ci
        credential:
          source: env        # nothing stored; read from the environment
          secret_id_env: ORBIT_TENCENT_CI_SECRET_ID
          secret_key_env: ORBIT_TENCENT_CI_SECRET_KEY
```

An over-permissive `credentials.yaml` (anything wider than `0600`) is a hard error, not a
warning.

## Language

`language:` accepts `en-US` or `zh-CN`. Precedence: `--lang` > `ORBIT_LANG` > this key >
`LC_ALL`/`LC_MESSAGES`/`LANG`. POSIX placeholders (`LC_ALL=C`) count as "no preference", so
a cron environment does not force a language.

## Keys worth knowing

| Key | Default | Effect |
| --- | --- | --- |
| `defaults.products` | `[cdn, teo]` | Products scanned and written |
| `defaults.concurrency.changes` | `2` | Write groups in flight (raise it on large fleets) |
| `defaults.strategy` | `auto` | `auto`/`product` use each product's own API, `deploy` uses the unified SSL deploy API. All three values are supported without any switch; unknown values fail with the accepted list |
| `defaults.edgeone.replace_free_cert` | `true` | Replace EdgeOne's free certificate with pool certificates |
| `defaults.clean.older_than` | `7d` | Deletion grace period |
| `defaults.clean.require_full_evidence` | `true` | Refuse cleanup when a product could not be scanned |
| `defaults.clean.include_free` | `false` | Allow free certificates as candidates |
| `defaults.clean.protect_patterns` | — | Alias patterns that are never deleted |

Option defaults and their reasons are recorded in [DECISIONS.md](../../DECISIONS.md).
