# M1 验证指南（真实腾讯云账号）

> 目标：用最小风险确认 M1 的采集与计划逻辑与你的实际账号一致。
> **第 1–7 步不会向云端写入任何数据**；第 8 步是唯一的写操作，且只作用于你指定的单个域名。

---

## 0. 准备：只读子账号与权限

**不要用主账号密钥。** 建一个 CAM 子账号，只授予只读权限即可跑完第 1–7 步：

```json
{
  "version": "2.0",
  "statement": [
    {
      "sid": "orbit-read-only",
      "effect": "allow",
      "action": [
        "ssl:DescribeCertificates",
        "ssl:DescribeCertificate",
        "ssl:DescribeCertificateDetail",
        "ssl:DescribeHostCdnInstanceList",
        "ssl:DescribeHostTeoInstanceList",
        "ssl:CreateCertificateBindResourceSyncTask",
        "ssl:DescribeCertificateBindResourceTaskResult"
      ],
      "resource": ["*"]
    },
    {
      "sid": "orbit-read-products",
      "effect": "allow",
      "action": [
        "cdn:DescribeDomains",
        "cdn:DescribeDomainsConfig",
        "teo:DescribeZones",
        "teo:DescribeAccelerationDomains"
      ],
      "resource": ["*"]
    }
  ]
}
```

需要跑第 8 步（写入验证）时，再临时追加：`ssl:DeployCertificateInstance`、`ssl:DescribeHostDeployRecord`、`cdn:ModifyDomainConfig`、`cdn:DescribeDomainsConfig`（已有）、`teo:ModifyHostsCertificate`。

> 服务角色 `SSL_QCSLinkedRoleInReplaceLoadCertificate` 只在**写入**路径需要；Orbit 的 `doctor` / `account test` 会把它标成警告（只读探测不到），第一次写入时才会真正校验。

## 1. 构建

```bash
cd /path/to/orbit
source ../.toolchains/env.sh
make build          # -> bin/orbit
./bin/orbit version
```

## 2. 录入凭证（不回显，写入 ~/.orbit/credentials.yaml 并强制 0600）

```bash
./bin/orbit account add --alias main
# 依次输入 SecretId、SecretKey、再确认一次 SecretKey
./bin/orbit account list
```

非交互场景（CI）可以用：`printf 'AKIDxxx\nSECRETxxx\n' | ./bin/orbit account add --alias hk --from-stdin`

**核对点**：`ls -l ~/.orbit/credentials.yaml` 必须是 `-rw-------`。若你手工把权限改成 644，Orbit 会拒绝启动并提示 `chmod 600`（这一步可以故意试一下，确认它真的会拒绝）。

## 3. 环境自检

```bash
./bin/orbit doctor
./bin/orbit account test --accounts=main
```

期望：`doctor` 全绿（项目级配置可以是提示）；`account test` 的「云平台凭证」为通过、服务角色为警告。

## 4. 只读巡检，与控制台逐项核对（**第 1 个关键验证**）

```bash
./bin/orbit status --accounts=main
./bin/orbit status --accounts=main --json
```

请对照腾讯云控制台核对这几个数字：

| 字段 | 与什么核对 |
| --- | --- |
| `certificates.total` | SSL 证书列表的总数（注意：证书是全局的，不需要切地域） |
| `certificates.uploaded` | 其中「来源 = 用户上传」的数量 |
| `certificates.in_use` | 关联查询判定为「被云资源引用」的数量 |
| `resources.total` | CDN 加速域名数 + EdgeOne 全部站点的加速域名数 |
| `resources.unbound` | 其中当前没有绑定证书的域名数（CDN 看 `Https.CertInfo` 是否为空） |
| `resources.foreign_cert_domains` | **EdgeOne 上正在使用 EdgeOne 自带免费证书的域名数**（见第 6 步） |
| `pending_apply` | 计划中「需要换证书」的条数 |

如果 `certificates.total` 与 `resources.total` 都对得上，说明 M1 的采集是对的。**对不上就停下来告诉我**，并附上 `--json` 的输出。

## 5. 应用计划演练（不写云端）

```bash
./bin/orbit update --ssl --accounts=main --dry-run
```

逐条检查：

1. 「当前证书」列是否与你控制台上该域名实际绑定的证书一致？
2. 「目标证书」列是否选了**到期更晚**的那张？（同域名多张时；若到期时间相同，应优先 `upload`）
3. 泛域名是否只匹配一级子域？（`*.example.com` 不应匹配 `example.com`，也不应匹配 `a.b.example.com`）
4. 已经是正确证书的域名，是否显示为「已最新」而不是「应用证书」？

## 6. EdgeOne 免费证书语义（**第 2 个关键验证**）

M1 里最容易出错的一点：EdgeOne 域名的证书配置由 `Certificate.Mode` 决定，`eofreecert*` 表示用的是 EdgeOne 自带免费证书（`CertId` 形如 `teo-xxxx`，不在腾讯云 SSL 证书池里）。

```bash
./bin/orbit update --ssl --accounts=main --dry-run          # 默认：免费证书域名会被列为「应用证书」
./bin/orbit config --global defaults.edgeone.replace_free_cert=false
./bin/orbit update --ssl --accounts=main --dry-run          # 现在：应显示为「外部证书」且不产生写入
```

请确认：

- 在控制台上找一个确实在用 EdgeOne 免费证书的域名；
- 上面第一条命令里，它是否被识别为「当前证书 = teo-xxx」而不是「未绑定」；
- 第二条命令里，它是否从计划中消失（只作为信息展示）。

## 7. 清理计划演练（不删任何东西）

```bash
./bin/orbit clean --ssl --accounts=main --dry-run
```

检查：

1. 候选列表是否**只包含你自己上传的证书**（腾讯云免费证书必须出现在「已跳过」里，原因：非用户上传）？
2. 每个候选是否都满足：同域名有更优证书（`Superseded by` 有值）、超过 7 天、关联查询为 0？
3. 仍在被引用的证书，是否显示「仍被云资源引用」而不是出现在候选里？
4. 把 `--older-than` 改小（例如 `1d`）应该只会让候选变多，不会让在用的证书进入候选。

> ⚠️ 按你的 B3 决策，v1 的清理范围是**全部 `upload` 证书**，没有别名/标签/域名白名单这层兜底。所以请特别留意第 1 条：手工上传且无引用的证书**会**被列为候选。正式删除前建议先用 `protect_patterns` 兜一层（见 README）。

## 8. 写入验证（可选，风险自担；只动一个域名）

挑一个**测试用**的 CDN 域名或 EdgeOne 域名（不要用生产域名）：

```bash
# CDN：走 ModifyDomainConfig(Route="Https.CertInfo.CertId")
./bin/orbit update --ssl --accounts=main --domains=test.example.com --yes

# EdgeOne：走 ModifyHostsCertificate(Mode="sslcert")
./bin/orbit update --ssl --accounts=main --domains=test.example.com --yes
```

执行时应看到 `[ok]` 且 `Verified`（回读确认）。随后立刻去控制台看该域名的证书是否已变更为目标证书。再跑一次同一条命令，应该输出「已最新」。

## 需要你回报的 5 个事实（M1 的参数级实测项）

| # | 要确认的事 | 怎么确认 |
| --- | --- | --- |
| V1 | 写入所需**服务角色的确切名称** | 第一次写入若报 `RoleNotFoundAuthorization`，控制台授权页会显示角色名；或直接看 `orbit doctor` 的警告文案是否与实际一致 |
| V2 | 部署状态从提交到生效的**耗时**（决定轮询超时） | 第 8 步执行时留意耗时；若超过 5 分钟被判定为未确认，请把耗时告诉我 |
| V3 | TEO 客户端 `Region` 传 `ap-guangzhou` 是否被接受 | 第 4 步能返回 EdgeOne 数据即通过（当前实现固定传 `ap-guangzhou`） |
| V4 | EdgeOne 免费证书是否也出现在 SSL 证书列表里 | 对比第 4 步的 `certificates.total` 与控制台 SSL 列表数量 |
| V5 | `ModifyDomainConfig` 的 Route 更新是否真的生效 | 第 8 步 CDN 分支执行后，控制台该域名的证书是否已切换 |

## M1 已知边界（不是 bug）

- **TUI 未实现**：`orbit status` 目前是文本/JSON 输出，多 Tab TUI 在 M5。
- **`--strategy=deploy` 对 CDN 不可用**：`InstanceIdList` 的 CDN 形式需要「计费开关」，而 `DescribeDomainsConfig` 不返回它；为避免猜错影响计费，CDN 的 `InstanceKey` 留空，该策略会明确报 `cloud.err.deploy_failed` 并建议改用产品策略。CDN 默认走 `ModifyDomainConfig`，不受影响。
- **审计日志与证书材料备份未完成**（M4）：`clean --yes` 目前只写元数据备份。
- **`status` 缓存未启用**：每次都全量拉取，慢但准确。
- **翻页上限**：证书每页 100（上限 1000）、CDN 每页 100（硬上限）、EdgeOne 站点每页 100。

## 排错

| 现象 | 含义 / 处理 |
| --- | --- |
| 退出码 1 + `cloud.err.auth_failed` | SecretId/SecretKey 无效；`orbit account add --alias main --force` 重录 |
| 退出码 1 + `cloud.err.no_permission` | 子账号缺权限；对照第 0 节的策略补齐 |
| 退出码 4 + 账号歧义 | 多个账号时需 `--accounts=main` 或 `--all-accounts` |
| 退出码 2 + `cloud.err.role_missing` | 服务角色未授权；去控制台授权后重试（删除类操作会因此被保留，这是设计行为） |
| 退出码 2 + `cloud.err.delete_kept_bound` | 该证书仍被资源引用，Orbit 主动保留 |
| 退出码 3 | 计划模式下有变更（仅 `--fail-on-changes` 时出现），给 cron 用 |
| `[unverified]` 结果行 | 写入已提交但回读未确认；用 `orbit status` 再确认一次，不要盲目重跑 |
