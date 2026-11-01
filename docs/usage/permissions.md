# Permissions

[← usage index](README.md) · Command: [`orbit account guide`](commands.md#orbit-account)

Orbit needs a small, explicit set of CAM actions. You can attach a coarse preset policy
(one click, wider than necessary) or generate an exact custom policy — this page is about
the exact one.

## Tiers

Every action belongs to one of three authority levels, ordered by what Orbit does:

| Tier | Used by | What it can change |
| --- | --- | --- |
| `read` | `status`, `doctor`, `account test`, `dns zones`, `dns ls`, every `--dry-run` | nothing |
| `write` | `orbit update --ssl --yes`, `dns set` | binds certificates, writes records |
| `delete` | `orbit clean --ssl --yes`, `dns rm` | removes certificates and records |

Each action is documented with the reason Orbit needs it, and cites the official Tencent
Cloud interface list it comes from (`ssl`, `cdn`, `teo`, `dnspod`).

## Ready-made files

The repository ships three generated policies, sourced from the same table the guide uses:

| File | Actions | Use it for |
| --- | --- | --- |
| [`docs/cam/readonly.json`](../cam/readonly.json) | 10 | inspection machine, `--dry-run` |
| [`docs/cam/execute.json`](../cam/execute.json) | 15 | apply certificates and records |
| [`docs/cam/delete.json`](../cam/delete.json) | 19 | apply **and** delete |

They are generated — do not hand-edit. Change the table and regenerate:

```bash
# edit internal/cloud/tencent/permissions.go, then
go test ./internal/cloud/tencent/ -run TestPolicyFilesMatch -update
```

Multi-account setups: give the inspection machine the read-only account and the writing
machine the execute account. Deleting is a third decision — keep it on the narrowest
machine that needs it.

## Generating a policy

```bash
# complete policy: read + write + delete in one document
orbit account guide --scope=full --policy > orbit-policy.json

# one tier at a time
orbit account guide --scope=read  --policy > readonly.json
orbit account guide --scope=apply --policy > execute.json

# DNS only (6 actions), or certificates only (no DNS)
orbit account guide --scope=full --products=dns          --policy > dns.json
orbit account guide --scope=full --products=ssl,cdn,teo  --policy > certs.json

# write the file directly (0600) instead of redirecting
orbit account guide --scope=full --policy-out ./orbit-policy.json
```

`--policy` prints **only** the JSON on stdout (the console steps go to stderr), so it pipes
cleanly. Without `--products` every product is included: `ssl`, `cdn`, `teo`, `dnspod`.

### In the TUI

```bash
orbit account guide
```

Pick vendor, products and usage level; the action list updates live. Press `p` to see the
policy document with the console steps, then `w` to save it (default
`./orbit-account-policy.json`; a directory or `~/…` also works). `esc`/`p` returns to the
guide, `q` quits.

## Applying it in the console

1. **Access management → Policies → Create custom policy**
2. choose **create by policy syntax** (the template content is replaced)
3. paste the JSON into the policy content, name it (`orbit-readonly`, `orbit-apply`,
   `orbit-full`), save
4. attach the policy to the API sub-account (or to its user group)

The document has exactly `version` and `statement`; each statement has `effect`, `action`,
`resource`. There is **no `sid`** field in Tencent Cloud's grammar — adding one risks the
console rejecting the document.

## The identity action is not in the policy

`sts:GetCallerIdentity` appears in the guide but is **never written into the policy**: the
identity probe needs no grant, and CAM rejects a document containing an unknown action.
That single bad entry would invalidate the whole policy.

## The linked service role

Certificate deployment and the binding checks that gate deletion need one console-side
grant that a policy cannot express:

```
SSL_QCSLinkedRoleInReplaceLoadCertificate
```

Grant it once per account. `orbit doctor` and `account test` report it as a warning because
it cannot be proven read-only; the first write verifies it for real.

## Verifying a grant

```bash
orbit account test --accounts=main            # per product: SSL, CDN, EdgeOne, DNSPod
orbit account test --accounts=main --json | jq '.[] | select(.status!="ok")'
orbit dns zones --accounts=main               # cheapest DNS read
```

`UnauthorizedOperation` (or `AuthFailure`) means the policy is missing something.
`OperationDenied.NoPermissionToOperateDomain` from DNS is **not** a policy problem: the
zone belongs to another account — see [dns.md](dns.md#listing).

## Official sources

| Prefix | Interface list |
| --- | --- |
| `ssl` | <https://cloud.tencent.com/document/product/598/98168> |
| `cdn` | <https://cloud.tencent.com/document/product/598/98110> |
| `teo` | <https://cloud.tencent.com/document/product/598/99327> |
| `dnspod` | <https://cloud.tencent.com/document/product/598/99243> |

Policy grammar: <https://cloud.tencent.com/document/product/598/10604> ·
custom policy walkthrough: <https://cloud.tencent.com/document/product/598/80789>
