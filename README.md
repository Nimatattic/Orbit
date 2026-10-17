# Orbit

> 把「腾讯云 SSL 证书池」与「CDN 加速域名 / EdgeOne 站点域名」对齐：自动匹配并应用最优证书，巡检全景状态，清理再没有任何云资源使用的证书。

命令：`orbit` ｜ 首批平台：腾讯云 ｜ 首批产品：**CDN + EdgeOne** ｜ module：`github.com/Crequency/orbit`

设计与事实依据：

- 方案与选型：[docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md)
- 真机验证步骤（M1）：[docs/VERIFY_M1.md](docs/VERIFY_M1.md)
- **运维手册（安装 / cron / systemd / 权限 / 回滚 / 排错）**：[docs/OPERATIONS.md](docs/OPERATIONS.md)
- CAM 最小权限策略（可直接粘贴）：[只读](docs/cam/readonly.json) · [执行](docs/cam/execute.json) · [含删除](docs/cam/delete.json)
- 腾讯云 API 事实清单（逐条来源 + 待实测项）：[docs/TENCENT_API_NOTES.md](docs/TENCENT_API_NOTES.md)
- 已确认决策与已接受风险：[DECISIONS.md](DECISIONS.md)

---

## 当前进度（M0 已完成）

| 里程碑 | 状态 |
| --- | --- |
| M0 骨架：CLI、配置分层、凭证存储、i18n、退出码、匹配引擎、清理门禁、fake provider | ✅ 完成 |
| M1 只读采集：腾讯云 SDK 接入（证书池 / CDN 域名 / EdgeOne 站点域名 / 关联查询） | ✅ 完成（回放测试覆盖；待你用真实账号按 [VERIFY_M1](docs/VERIFY_M1.md) 核对） |
| M2 匹配引擎打磨（计划渲染、风险分级已可用） | 部分可用 |
| M3 应用执行（`ModifyDomainConfig` / `ModifyHostsCertificate`） | ✅ 写入、异步轮询、回读复核、错误码提示、审计日志、分组并发 |
| M4 清理执行（删除 + 证书材料备份 + 任务状态分类） | ⏳ 未开始 |
| M5 TUI（多 Tab、账号多选、计划预览） | ✅ 完成（状态多 Tab 总览、账号多选、计划确认、执行进度与动态等待、`u`/`c` 直达） |
| M6 交付（打包、cron/systemd、CAM 模板） | ⏳ 未开始 |

**现在能做什么**：真实腾讯云账号的只读采集（证书池 / CDN 域名 / EdgeOne 站点与域名 / 证书↔资源关联查询）、匹配与计划、清理候选；写入路径（CDN `ModifyDomainConfig`、EdgeOne `ModifyHostsCertificate`、SSL `DeployCertificateInstance`）已实现并带**回读复核**；另外还有配置与凭证管理、环境自检、多语言、以及完全不触网的 `--demo` 演示链路。
**还不能做什么**：多 Tab TUI（M5）、审计日志与证书材料备份（M4）、`--strategy=deploy` 对 CDN（见 [VERIFY_M1](docs/VERIFY_M1.md) 已知边界）。

---

## 构建与运行

工作区自带 Go 工具链（1.27.1）：

```bash
source ../.toolchains/env.sh
make build            # -> bin/orbit
make test             # 单元测试（全部离线，走 cloud/fake）
make vet              # go vet
make release          # linux/amd64, linux/arm64, darwin/arm64 静态二进制
```

## 运行模式（由参数决定）

| 你输入 | 行为 |
| --- | --- |
| `orbit dash` | **TUI 总控台**：登记多个账号时先**单选**一个账号，然后在同一界面里进入「应用证书 / 清理证书 / 状态总览 / 环境自检」，每次功能执行完自动回到总控台 |
| `orbit`（无参数） | **状态总览 TUI**：每个（平台·账号）一个 Tab，展示证书/资源/待处理/可清理；`u`/`c` 直接跳到应用/清理 |
| `orbit status`（终端下） | 同上；非终端时输出纯文本，逐账号容错（一个账号失败不影响其它） |
| `orbit update --ssl`（终端下） | 登记多个账号时先出现**账号多选器**（空格勾选 / a 全选 / 回车确认）；选定后每个账号**进入 TUI**：扫描 → 变更确认页 → 执行页（逐条状态与动态等待）→ 结果页；`y` 执行、`n`/`esc` 取消 |
| `orbit update --ssl --dry-run` | 只出计划，纯文本，**绝不写云端** |
| `orbit update --ssl --yes` | CLI 批处理模式（cron / 脚本用），逐行进度 |
| 非终端环境（无 TTY） | 自动降级为纯文本：无 `--yes` 时只出计划 |
| `--no-tui` / `--json` | 强制纯文本 / 结构化输出 |

TUI 只接管屏幕，日志与结果仍会落回终端（退出后打印文本结果，便于留痕）。`orbit clean` 的交互流程与 `update` 一致。

界面采用全屏面板布局（圆角边框 + 标题嵌在顶边），底栏是按宽度自适应的快捷键提示；等待云端回报的行显示**转动的指示器 + 已等待秒数**，执行阶段顶部还有**进度条**。

### 总控台（`orbit dash`）

面向"人坐在终端前操作"的场景：一个账号、一块屏幕、所有功能。

```
╭─ Orbit · 总控台 ─────────────────────────────────────────────╮
│账号 demo · 产品 cdn, teo  更新于 12:33:53                    │
╰──────────────────────────────────────────────────────────────╯
╭─ 概览 ───────────────────────────────────────────────────────╮
│证书 7（上传 6 / 在用 4 / 未用 2） · 资源 4（未绑定 2）         │
│待处理 4 · 可清理 1 · 告警 0                                  │
╰──────────────────────────────────────────────────────────────╯
╭─ 功能 ───────────────────────────────────────────────────────╮
│▸ 应用证书  4 条待处理                                        │
│  清理证书  1 条候选                                          │
│  状态总览（全部账号）                                        │
│  环境自检                                                    │
╰──────────────────────────────────────────────────────────────╯
↑/↓ 移动  ·  enter 执行  ·  d 自检  ·  r 重扫  ·  q 退出
```

- **多账号时先单选**：`orbit dash` 会先弹单选列表（`↑/↓` + 回车）确定要管理哪个账号；用 `--accounts=<别名>` 可跳过。
- `enter` 执行当前条目（也可按 `1`–`4` 直接选），执行完**回到总控台**，概览会重新扫描。
- `d` 展开/收起环境自检（凭证归属、SSL/CDN/EdgeOne 可达性、服务角色授权）；自检结果会被缓存，`r` 才会重测。
- 账号不可用（凭证缺失等）时**仍然进得去**：失败会显示在「概览」和「环境自检」里，此时只允许进入只读的「状态总览」。
- 需要交互式终端；非 TTY 下会明确报错（脚本环境请用 `orbit status` / `update` / `clean`）。

### 总览页的多选操作

`orbit` / `orbit status` 的待处理与可清理列表支持逐条勾选后再执行：

| 按键 | 作用 |
| --- | --- |
| `↑`/`↓`（或 `j`/`k`） | 在当前列表内移动选择框 |
| `space` | 勾选 / 取消勾选当前行 |
| `a` | 全选当前列表 |
| `n` | 取消全选 |
| `s` | 在「待处理」与「可清理」两个列表间切换焦点 |
| `u` / `enter` | 对**勾选的**待处理条目生成应用计划（未勾选任何条目 = 全部） |
| `c` | 对**勾选的**可清理条目执行清理（未勾选 = 全部） |
| `tab` / `1-9` | 切换账号 |
| `r` | 重新扫描当前账号 |
| `q` / `esc` | 退出 |

## 快速上手（不触网，用演示数据）

```bash
# 环境自检
./bin/orbit --demo doctor

# 查看演示账号的证书/资源/待办概览
./bin/orbit --demo status
./bin/orbit --demo status --json

# 生成「应用证书」计划（默认只出计划，绝不写云端）
./bin/orbit --demo update --ssl

# 真正执行（对演示数据是内存写，不会碰腾讯云）
./bin/orbit --demo update --ssl --yes

# 清理计划：只列候选，并给出每一张被跳过的原因
./bin/orbit --demo clean --ssl
```

演示数据里刻意包含了这些场景，便于核对判定逻辑：

| 场景 | 期望行为 |
| --- | --- |
| `www.example.com` 绑定了一张已过期证书 | 计划中标记为**高风险**应用，目标为新证书 |
| `static.example.com` 未绑定证书 | 由泛域名证书 `*.example.com` 匹配，**低风险**首次绑定 |
| EdgeOne `cdn.example.com` 使用 EdgeOne 免费证书（`Mode=eofreecert`） | 默认替换为最优证书；`--dry-run` 或 `edgeone.replace_free_cert=false` 时只报告 |
| `test.example.com` 新旧两张证书、旧的无任何引用 | 旧证书成为清理候选；新的因"未被取代"被保留 |
| 腾讯云签发的免费证书（`From=trustasia`） | **永不进入清理候选**（G1 只允许 upload） |
| 托管中的证书（`HostingStatus=0`） | 保留 |

## 接入真实账号（M1 之后可用）

```bash
# 交互式录入（SecretKey 不回显），凭证写入 ~/.orbit/credentials.yaml（0600）
./bin/orbit account add --alias main

# 非交互（CI / 手工准备）
printf 'AKIDxxx\nSECRETxxx\n' | ./bin/orbit account add --alias hk --from-stdin

./bin/orbit account list
./bin/orbit account test --accounts=main             # 按 SSL/CDN/EdgeOne 逐产品探测，并显示凭证归属
./bin/orbit account test --accounts=main --products=teo   # 只复查某一个产品
./bin/orbit doctor
```

**Orbit 不会读取 `~/.tccli/*.credential`**（决策 A1）。凭证只来自：环境变量注入、`~/.orbit/credentials.yaml`（强制 0600，权限过宽直接拒绝启动）。

cron 推荐用环境变量注入，配置文件里只写变量名：

```yaml
platforms:
  tencent:
    accounts:
      - alias: main
        credential:
          source: env
          secret_id_env: ORBIT_TENCENT_MAIN_SECRET_ID
          secret_key_env: ORBIT_TENCENT_MAIN_SECRET_KEY
        products: [cdn, teo]
```

## 配置

分层（高优先级在前）：命令行 flag → `ORBIT_*` → `--config <path>` → `./.orbit.yaml` → `~/.orbit/config.yaml` → 内置默认值。

```bash
./bin/orbit config --global language=en-US     # 切换语言（zh-CN / en-US）
./bin/orbit config --local  defaults.clean.older_than=3d
./bin/orbit config show
./bin/orbit config path
```

`~/.orbit/` 布局：`config.yaml`、`credentials.yaml`(0600)、`cache/`、`audit/`、`backups/`。

### 审计日志

每次执行（`update --yes`、`clean --yes`，以及 TUI 流程）都会向 `~/.orbit/audit/audit.jsonl` 追加记录：一条变更一行（时间、run_id、命令、账号、产品、域名、策略、变更前后证书 ID、结果 `ok/unverified/failed`、错误码），末尾一条汇总（成功/未确认/失败计数）。文件权限 0600，**从不记录凭证**。用 `jq` 就能查：

```bash
jq -c 'select(.kind=="change" and .result=="failed")' ~/.orbit/audit/audit.jsonl
jq -c 'select(.kind=="summary")' ~/.orbit/audit/audit.jsonl | tail -5
```

### 删除前备份

`clean` 在删除任何证书之前，会在 `~/.orbit/backups/<账号>/<证书ID>-<时间戳>/` 下写入：

| 文件 | 权限 | 内容 |
| --- | --- | --- |
| `cert.pem` | 0644 | 服务器证书（如云端允许导出） |
| `chain.pem` | 0644 | 根证书链（如可得） |
| `key.pem` | **0600** | 私钥（如云端允许导出；托管证书通常不可导出） |
| `meta.json` | 0600 | 证书 ID、别名、域名、SAN、到期时间、来源、被谁取代、G2 证据、材料是否导出、run_id |

材料无法导出时**仍会写元数据**并把证书 ID 列出来提示你（design 见 DECISIONS：1Panel 仍是证书源头，元数据是可接受的兜底）。

语言优先级：`--lang` → `ORBIT_LANG` → 配置 `language` → `$LANG` → `zh-CN`。`--json` 输出与错误码**不做翻译**。

## 退出码契约

| 码 | 含义 |
| --- | --- |
| 0 | 成功（含"无变更"、"演练完成"） |
| 1 | 致命错误（配置 / 凭证 / 参数 / 未实现的 provider） |
| 2 | 部分失败（有计划内条目执行失败） |
| 3 | 计划模式存在待执行变更（仅 `--fail-on-changes`） |
| 4 | 安全闸门拒绝执行（证据不完整、账号歧义、服务角色未授权） |

## 安全模型

- `status` / `doctor` / `account test` 永远只读。
- `update` / `clean` 默认**只出计划**；`--yes` 才写云端；`--dry-run` 永不写。
- 非 TTY 且未给 `--yes` 时**不写云端**，退出码 0（`--fail-on-changes` 时为 3）。
- 清理四道闸门（全部通过才允许删除）：① 只处理 `FilterSource=upload`；② 关联查询证据必须完整且为 0（`--permit-partial-scan` 可放宽，会打印警告）；③ 每个覆盖域名都必须已有更优证书；④ 超过宽限期（默认 7 天）、不在保护名单、非免费、非托管。
- 删除前强制写备份（M0 只备份元数据，M4 补齐证书材料）。
- v1 的清理范围是**全部 upload 证书**，不含别名/标签/域名白名单；由此带来的已接受风险见 [DECISIONS.md §3.1](DECISIONS.md)。

## 目录结构

```
cmd/orbit/          入口：装配 + os.Exit
internal/cli/       命令树、flag、TTY 判定、退出码映射、错误消息键翻译
internal/config/    ~/.orbit 与 ./.orbit.yaml 的分层合并、校验
internal/credential/凭证存储（0600 强制）与环境变量解析
internal/core/      领域模型、匹配选优、计划、清理门禁（纯函数，重点测试）
internal/cloud/     Provider 接口 + tencent(M1) + fake(演示/测试)
internal/render/    纯文本渲染（JSON 由 cli 直接编码）
internal/i18n/      语言协商与消息渲染
locales/            嵌入的语言包（zh-CN、en-US）
docs/               方案与腾讯云事实清单
```
