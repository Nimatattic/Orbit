# Orbit 运维手册

面向"装到服务器上、由 cron 或人手定期运行"的场景。设计文档见
[IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md)，真机验证步骤见 [VERIFY_M1.md](VERIFY_M1.md)。

---

## 1. 安装

Orbit 是**静态单文件二进制**，不需要运行时依赖。

```bash
# 在开发机交叉编译（工作区自带 Go 工具链）
cd /path/to/orbit
source ../.toolchains/env.sh
make release
# -> dist/orbit-linux-amd64, dist/orbit-linux-arm64, dist/orbit-darwin-arm64

# 拷到服务器
scp dist/orbit-linux-amd64 root@server:/usr/local/bin/orbit
ssh root@server 'chmod 755 /usr/local/bin/orbit && orbit version'
```

## 2. 目录布局（全部在 `~/.orbit/` 下）

| 路径 | 内容 | 权限 |
| --- | --- | --- |
| `config.yaml` | 全局配置（账号、产品、宽限期等） | 0644 |
| `credentials.yaml` | 凭证（**不读 tccli**） | **0600**，权限过宽会拒绝启动 |
| `audit/audit.jsonl` | 审计日志，每次执行追加 | 0600 |
| `backups/<账号>/<证书ID>-<时间戳>/` | 删除前备份（`cert.pem` / `key.pem` / `chain.pem` / `meta.json`） | 目录 0700，`key.pem` **0600** |
| `cache/` | 预留给 `status` 的本地缓存 | — |

`ORBIT_HOME` 可整体改路径（见 §5 的 cron 示例）。

## 3. 权限：三档 CAM 策略

`docs/cam/` 下有三份可直接粘贴到 CAM 控制台的策略，按用途递增：

| 文件 | 动作数 | 用途 |
| --- | --- | --- |
| [`cam/readonly.json`](cam/readonly.json) | 9 | `status` / `doctor` / `account test`，以及一切 `--dry-run` |
| [`cam/execute.json`](cam/execute.json) | 11 | 只读 + `orbit update --ssl --yes`（`cdn:ModifyDomainConfig`、`teo:ModifyHostsCertificate`） |
| [`cam/delete.json`](cam/delete.json) | 14 | 执行 + `orbit clean --ssl --yes`（`ssl:DeleteCertificates` 等） |

**这三份文件由代码生成，不要手改。** 需要调整时改
[`internal/cloud/tencent/permissions.go`](../internal/cloud/tencent/permissions.go)，然后：

```bash
go test ./internal/cloud/tencent/ -run TestPolicyFilesMatch -update   # 重新生成
go test ./internal/cloud/tencent/ -run TestPermissionTableCoversEverySDKCall   # 防漂移校验
```

不确定要开哪些权限时，用向导按需生成（还能看到每个权限的用途）：

```bash
orbit account guide                      # TUI：云厂商 × 产品范围 × 使用范围
orbit account guide --scope=read --products=cdn
orbit account guide --scope=full --policy > my-policy.json
```

**推荐两套子账号**：

- **巡检号**（`readonly.json`）→ 放在只读机器上跑 `status` 与演练；
- **执行号**（`execute.json` 或 `delete.json`）→ 只放在执行机上，密钥单独管理。

另外需在控制台一次性授权服务角色 `SSL_QCSLinkedRoleInReplaceLoadCertificate`（部署证书与"带关联检查的删除"的前置条件）。`orbit doctor` 会把它标为警告（只读探测不到），首次写入时才会真正校验。

## 4. 首次运行检查清单

```bash
orbit account add --alias main      # 交互录入，落 ~/.orbit/credentials.yaml（0600）
orbit doctor                        # 配置/凭证/权限/语言包逐项自检
orbit account test --accounts=main  # 逐产品探测 SSL / CDN / EdgeOne，并显示凭证归属
orbit                               # 终端下进入总览 TUI（非终端输出文本）
orbit dash                          # 交互式总控台：选账号 → 应用/清理/总览/自检
```

## 5. cron 用法

**先演练、再有变更时告警、最后执行**，是最稳的组合：

```bash
# /etc/cron.d/orbit
SHELL=/bin/bash
ORBIT_HOME=/root/.orbit
PATH=/usr/local/bin:/usr/bin:/bin

# 周一 03:30 演练：有变更则退出码 3（配合监控告警），不写云端
30 3 * * 1 root orbit update --ssl --all-accounts --dry-run --fail-on-changes --json --no-progress >> /var/log/orbit.log 2>&1

# 周一 04:00 真正执行
0 4 * * 1 root orbit update --ssl --all-accounts --yes --json --no-progress >> /var/log/orbit.log 2>&1

# 周一 04:30 清理（自动备份到 ~/.orbit/backups）
30 4 * * 1 root orbit clean --ssl --all-accounts --yes --older-than=7d --json --no-progress >> /var/log/orbit.log 2>&1
```

要点：

- **`--yes` 才写云端**；非终端环境下没有 `--yes` 只会出计划（退出码 0），加 `--fail-on-changes` 则待办时返回 3。
- `--no-progress` 让日志干净；`--json` 便于采集。
- 多账号要么 `--all-accounts`，要么 `--accounts=main,hk`——批处理模式不会替你猜。

## 6. systemd 用法（比 cron 更好控环境变量）

```ini
# /etc/systemd/system/orbit-update.service
[Unit]
Description=Orbit SSL certificate update
After=network-online.target

[Service]
Type=oneshot
User=root
EnvironmentFile=/etc/orbit/env        # 内含 ORBIT_TENCENT_MAIN_SECRET_ID / _SECRET_KEY
ExecStart=/usr/local/bin/orbit update --ssl --all-accounts --yes --json --no-progress
StandardOutput=append:/var/log/orbit.log
StandardError=append:/var/log/orbit.log
```

```ini
# /etc/systemd/system/orbit-update.timer
[Unit]
Description=Weekly Orbit certificate update

[Timer]
OnCalendar=Mon 04:00
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
systemctl enable --now orbit-update.timer
systemctl list-timers orbit-update.timer
```

`/etc/orbit/env` 只有变量名与值（0600 权限），配置文件里用 `credential.source: env` 引用变量名即可：

```
ORBIT_TENCENT_MAIN_SECRET_ID=AKIDxxxxxxxx
ORBIT_TENCENT_MAIN_SECRET_KEY=xxxxxxxx
```

## 7. 日常查看

```bash
# 今天失败过哪些变更
jq -c 'select(.kind=="change" and .result=="failed")' ~/.orbit/audit/audit.jsonl | tail -20

# 每次执行的汇总（成功 / 未确认 / 失败）
jq -c 'select(.kind=="summary")' ~/.orbit/audit/audit.jsonl | tail -10

# "未确认"的条目（写入已提交但回读没确认，用 status 复核即可，别盲目重跑）
jq -c 'select(.kind=="change" and .result=="unverified")' ~/.orbit/audit/audit.jsonl

# 备份里有哪些证书、哪些导出了私钥
jq -c '{id:.certificate_id, key:.material.private_key_exported}' ~/.orbit/backups/*/*/meta.json
```

TUI 里也能直接看：`orbit`（总览）→ `u` 应用证书、`c` 清理证书。

## 8. 回滚

| 场景 | 做法 |
| --- | --- |
| 换错了证书（`update`） | 重新指向旧证书：`orbit update --ssl --domains=<域名> --yes`（旧证书仍在池里时），或直接在控制台改回 |
| 删错了证书（`clean`） | 从备份取回材料：`~/.orbit/backups/<账号>/<证书ID>-*/{cert,key,chain}.pem`，用腾讯云控制台或你原有的 tccli 脚本重新上传，再 `orbit update` |
| 需要完全停止 | 禁用 timer/cron 即可；Orbit 不在后台常驻，没有守护进程 |

> 备份是"最后的底稿"：`meta.json` 一定会有，`key.pem` 只在云端允许导出时才存在（托管证书通常不可导出）。**证书的权威源头仍是 1Panel**，所以最稳的回滚是"从 1Panel 重新推送"。

## 9. 升级与回滚版本

```bash
# 升级：直接替换二进制（配置与凭证格式向后兼容）
scp dist/orbit-linux-amd64 root@server:/usr/local/bin/orbit.new
ssh root@server 'mv /usr/local/bin/orbit /usr/local/bin/orbit.bak && mv /usr/local/bin/orbit.new /usr/local/bin/orbit && orbit version'

# 回滚：换回备份二进制
ssh root@server 'mv /usr/local/bin/orbit.bak /usr/local/bin/orbit'
```

`orbit version` 输出形如 `v0.1.0 (abc1234, 2026-10-11T00:00:00Z)`；审计日志每条都带版本号，便于对照"某次变更是哪个版本做的"。

## 10. 排错速查

| 现象 | 处理 |
| --- | --- |
| 退出码 1 + `cloud.err.auth_failed` | 密钥无效：`orbit account add --alias main --force` 重录 |
| 退出码 1 + `cloud.err.no_permission` | 先看 `orbit account test` 是哪个产品；多半是该产品未开通或策略未覆盖 |
| 扫描告警 `teo — UnauthorizedOperation` | EdgeOne 未开通/未授权；不用 EdgeOne 就从范围里去掉（`--products=cdn` 或 `defaults.products: [cdn]`），否则 `clean` 会因证据不完整而拒绝执行 |
| 告警 `产品 — UnauthorizedOperation (站点名)` | 站点类型是 full/partial 却被拒 = 真权限问题；EdgeOne Pages 类型站点会被静默跳过（进度里有一行说明） |
| `cloud.err.cdn_domain_disabled` | 该域名的 CDN 加速服务已关闭：先去"域名管理"启动域名 |
| `[unverified]` 结果行 | 写入已提交但 2 分钟内回读未确认；用 `orbit status` 复核，不要盲目重跑 |
| 退出码 2 | 部分失败：看 `jq` 查审计里的 `result=="failed"` 明细 |
| 退出码 3 | 演练发现待执行变更（仅 `--fail-on-changes`） |
| 退出码 4 | 安全闸门拒绝：证据不完整、账号歧义、服务角色未授权、实验性策略未开启 |
| 关联查询很慢 | 首次是云端异步任务（数十秒到数分钟，上限 5 分钟）；`status` 30 分钟内复用缓存，`update` 不做关联查询 |
