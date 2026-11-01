# Getting started

[← usage index](README.md)

## 1. Install

Static binaries (no runtime dependencies, `CGO_ENABLED=0`):

```bash
make release
# dist/orbit-linux-amd64   dist/orbit-linux-arm64   dist/orbit-darwin-arm64

scp dist/orbit-linux-amd64 root@server:/usr/local/bin/orbit
ssh root@server 'chmod 755 /usr/local/bin/orbit && orbit version'
```

From source (Go 1.27+, `make`):

```bash
git clone https://github.com/Nimatattic/Orbit.git
cd Orbit
make build            # -> bin/orbit
make install          # -> /usr/local/bin/orbit
```

`go install` works because the module path matches the repository
(`github.com/Nimatattic/Orbit`, upper case included — Go paths are case-sensitive):

```bash
# once a v* tag exists on the repository (see the release workflow in OPERATIONS.md)
go install github.com/Nimatattic/Orbit/cmd/orbit@latest
# before the first tag: branch install, or use the clone-and-build path above
go install github.com/Nimatattic/Orbit/cmd/orbit@main
```

The result lands in `$(go env GOPATH)/bin/orbit` (or `$GOBIN`). It is the same
`CGO_ENABLED=0` static binary `make release` produces, with the version taken from the
tag Go resolved; `make build` additionally stamps the local commit and date.

## 2. Register an account

`orbit account add` reads the secret interactively (no echo) and stores it in
`~/.orbit/credentials.yaml` with mode `0600`:

```bash
orbit account add --alias main
```

Non-interactive and overwrite variants:

```bash
# two lines on stdin: SecretId, then SecretKey
printf '%s\n%s\n' "$SECRET_ID" "$SECRET_KEY" | orbit account add --alias main --from-stdin

# re-register an existing alias with a rotated key
orbit account add --alias main --force
```

To keep secrets out of `~/.orbit` entirely, declare the account in `config.yaml`
with `credential.source: env` instead — nothing is stored, the process reads the two
named variables. See [accounts.md](accounts.md) and
[configuration.md](configuration.md#credentials).

Orbit never reads `~/.tccli` credentials.

## 3. Verify access

```bash
orbit doctor                        # config, credentials, permissions, locales
orbit account test --accounts=main  # per-product probes + credential identity
orbit about                         # build, paths, credential mode, audit status
```

`account test` prints one line per product (`SSL`, `CDN`, `EdgeOne`, `DNSPod`) plus
the identity the credential belongs to. A `warn` on the service role is expected until
the first write; see [permissions.md](permissions.md#the-linked-service-role).

## 4. Look before you act

Every write command is dry-run by default:

```bash
orbit status                              # inventory + pending changes
orbit update --ssl --dry-run              # what would be applied, and why
orbit clean --ssl --dry-run --older-than=7d
```

Modes and exit codes are explained in [modes.md](modes.md).

## 5. Try it without a cloud account

`--demo` swaps the provider for an offline fixture (2 zones, 7 certificates, 4 domains):

```bash
orbit --demo status
orbit --demo update --ssl --dry-run
orbit --demo dns ls example.com
orbit --demo dash
```

Demo state lives in one process: writes are visible inside that command only, and a
new invocation starts from the original fixture.

## 6. Next steps

- Apply certificates for real: [certificates.md](certificates.md)
- Manage DNS: [dns.md](dns.md)
- Learn the interactive screens: [tui.md](tui.md)
- Grant the minimum permissions: [permissions.md](permissions.md)
- Put it on a schedule: [operations manual](../OPERATIONS.md)
