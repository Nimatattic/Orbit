# Orbit 运维手册

面向"装到服务器上、由 cron 或人手定期运行"的场景。设计文档见
[IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md)，真机验证步骤见 [VERIFY_M1.md](VERIFY_M1.md)。

> **命令与用法的完整说明集中在 [`docs/usage/`](usage/README.md)**：命令与参数见
> [commands.md](usage/commands.md)，模式与退出码见 [modes.md](usage/modes.md)，
> 界面按键见 [tui.md](usage/tui.md)，DNS 见 [dns.md](usage/dns.md)，
> 权限与策略生成见 [permissions.md](usage/permissions.md)，配置见
> [configuration.md](usage/configuration.md)，排错见
> [troubleshooting.md](usage/troubleshooting.md)。本文只讲部署与运维。

---

## 1. 安装

Orbit 是**静态单文件二进制**（`CGO_ENABLED=0`），不需要运行时依赖。发布包共 9 个目标，
命名规则与包内结构见 [§11.3](#113-产物命名与包内结构)。

**方式一：下载已发布的包并校验**（推荐，无需 Go 工具链）

```bash
VERSION=v1.2.3                       # 换成你要的版本号
base="https://github.com/Nimatattic/Orbit/releases/download/${VERSION}"

curl -LO "${base}/orbit_${VERSION}_linux_amd64.tar.gz"   # 其它平台按 §11.3 换掉这一段
curl -LO "${base}/orbit_${VERSION}_checksums.txt"

sha256sum -c --ignore-missing "orbit_${VERSION}_checksums.txt"   # 只校验已下载的那一条
tar -xzf "orbit_${VERSION}_linux_amd64.tar.gz"                   # -> orbit / LICENSE / README.md

install -m 0755 orbit /usr/local/bin/orbit
orbit version
```

**方式二：在自己的开发机上交叉编译**（工作区自带 Go 工具链）

```bash
cd ~/path/to/orbit
source ../.toolchains/env.sh
make dist                                    # = bash scripts/build-dist.sh --all
ls dist/                                     # 9 个包 + orbit_<version>_checksums.txt
VERSION="$(git describe --tags --dirty 2>/dev/null || echo v0.0.0-dev)"
cd dist && sha256sum -c "orbit_${VERSION}_checksums.txt"   # 期望 9 行 OK
```

`make release` 仍可用，是 `make dist` 的**兼容别名**（同名产物、同样的 9 个目标），
只是名字是历史遗留的——它早已不再产出三个裸二进制。

**方式三：只打一个目标，拷到服务器解包安装**（运维最常用）

```bash
VERSION="$(git describe --tags)"
bash scripts/build-dist.sh linux amd64
scp "dist/orbit_${VERSION}_linux_amd64.tar.gz" root@server:/tmp/
ssh root@server "tar -xzf /tmp/orbit_${VERSION}_linux_amd64.tar.gz -C /usr/local/bin orbit \
  && chmod 755 /usr/local/bin/orbit && orbit version"
```

**方式四：按模块路径安装**（仓库发布 `v*` tag 之后可用，module 与仓库同名）

```bash
go install github.com/Nimatattic/Orbit/cmd/orbit@latest
```

> 版本号来自构建时的 `git describe --tags`；**工作树没有 tag 时兜底 `v0.0.0-dev`**，
> 产物名相应变成 `orbit_v0.0.0-dev_linux_amd64.tar.gz`（见 [§9](#9-升级与回滚版本)）。

## 2. 目录布局（全部在 `~/.orbit/` 下）

| 路径 | 内容 | 权限 |
| --- | --- | --- |
| `config.yaml` | 全局配置（账号、产品、宽限期等） | 0644 |
| `credentials.yaml` | 凭证（**不读 tccli**） | **0600**，权限过宽会拒绝启动 |
| `audit/audit.jsonl` | 审计日志，每次执行追加 | 0600 |
| `backups/<账号>/<证书ID>-<时间戳>/` | 删除前备份（`cert.pem` / `key.pem` / `chain.pem` / `meta.json`） | 目录 0700，`key.pem` **0600** |
| `cache/` | **不存在**：没有任何代码创建该目录（`status` 复用云端 30 分钟关联缓存，`--no-cache` 可关） | — |

`ORBIT_HOME` 可整体改路径（见 §5 的 cron 示例）。

## 3. 权限：三档 CAM 策略

三份策略的生成方式、CAM 控制台操作步骤与最小权限建议见
[用法文档 · 权限](usage/permissions.md)，这里只列部署时的取舍。

`docs/cam/` 下有三份可直接粘贴到 CAM 控制台的策略，按用途递增：

| 文件 | 动作数 | 用途 |
| --- | --- | --- |
| [`cam/readonly.json`](cam/readonly.json) | 10 | `status` / `doctor` / `account test`，以及一切 `--dry-run` |
| [`cam/execute.json`](cam/execute.json) | 17 | 只读 + 写入（`cdn:ModifyDomainConfig`、`teo:ModifyHostsCertificate`、deploy 路径的 `ssl:DeployCertificateInstance` / `ssl:DescribeHostDeployRecord`） |
| [`cam/delete.json`](cam/delete.json) | 21 | 执行 + `orbit clean --yes`（`ssl:DeleteCertificates` 等） |

> `deploy` 策略从实验路径转正时，execute/delete 各新增了 `ssl:DeployCertificateInstance`
> 与 `ssl:DescribeHostDeployRecord` 两个 action。**用 `--strategy=deploy` 的账号，其存量
> 自定义策略需要补上这两个 action**，否则写入会报 `UnauthorizedOperation`；readonly 不变。

**这三份文件由代码生成，不要手改。** 需要调整时改
[`internal/cloud/tencent/permissions.go`](../internal/cloud/tencent/permissions.go)，然后：

```bash
go test ./internal/cloud/tencent/ -run TestPolicyFilesMatch -update   # 重新生成
go test ./internal/cloud/tencent/ -run TestPermissionTableCoversEverySDKCall   # 防漂移校验
```

不确定要开哪些权限时，用向导按需生成（还能看到每个权限的用途），并把输出直接粘贴到
**访问管理控制台 → 策略 → 新建自定义策略 → 按策略语法创建**：

```bash
orbit account guide                                     # TUI：p 看策略 JSON，w 保存到文件
orbit account guide --scope=read --products=cdn
orbit account guide --scope=full --policy > my-policy.json
orbit account guide --scope=full --policy-out ./orbit-policy.json
```

生成的 JSON 遵循[官方策略语法](https://cloud.tencent.com/document/product/598/10604)：

```json
{
  "version": "2.0",
  "statement": [
    { "effect": "allow", "action": ["ssl:DescribeCertificates"], "resource": ["*"] }
  ]
}
```

statement 结构是 `{effect, action, resource, condition?}`，**没有 `sid`**（与 AWS 不同），
控制台会校验 JSON 格式，因此不要手加字段。

action 名取自官方接口列表：`ssl` → [98168](https://cloud.tencent.com/document/product/598/98168)、
`cdn` → [98110](https://cloud.tencent.com/document/product/598/98110)、
`teo` → [99327](https://cloud.tencent.com/document/product/598/99327)。
**`sts:GetCallerIdentity` 不会写进策略**：它无需授权，且控制台遇到未知 action 会拒绝整份策略。
`orbit account guide --json` 的 `no_grant_needed` 字段会列出这类接口。

> 也可以直接在控制台附加官方预设策略（策略 → 预设策略，按产品名搜索）。预设策略是一键的，
> 但粒度更粗：产品的全读写策略会连带授予该产品其它接口的权限。要最小权限就用生成的 JSON。

**推荐两套子账号**：

- **巡检号**（`readonly.json`）→ 放在只读机器上跑 `status` 与演练；
- **执行号**（`execute.json` 或 `delete.json`）→ 只放在执行机上，密钥单独管理。

另外需在控制台一次性授权服务角色 `SSL_QCSLinkedRoleInReplaceLoadCertificate`（部署证书与"带关联检查的删除"的前置条件）。`orbit doctor` 会把它标为警告（只读探测不到），首次写入时才会真正校验。

## 4. 首次运行检查清单

```bash
orbit account add --alias main      # 录入凭证（0600）
orbit doctor                        # 环境自检
orbit account test --accounts=main  # 逐产品探测 + 凭证归属
orbit                               # 总览（TUI 或文本）
```

完整步骤（含演示模式、`about`、无凭证试跑）见
[getting-started.md](usage/getting-started.md)。

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

## 6.5 DNS 记录

DNS 的完整用法（记录类型、校验规则、幂等语义、多账号行为、控制台按键）见
[dns.md](usage/dns.md)。运维上只需记住一点：`dns set` 幂等，可安全放进 cron。

```bash
# 证书 DNS-01 校验记录；重复执行不会产生重复记录
orbit dns set example.com _acme-challenge TXT "$TOKEN" --ttl 60 --yes --accounts=main
# 是否已生效（对比公网 DNS，未生效退出码 3）
orbit dns check example.com _acme-challenge TXT
```

每次写入都会回读确认并写审计（`action: dns.create|update|delete`）。

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
# 升级：解包后整体替换二进制（配置与凭证格式向后兼容）
VERSION=v1.2.3
tar -xzf "orbit_${VERSION}_linux_amd64.tar.gz" -C /tmp
ssh root@server 'install -m 0755 /usr/local/bin/orbit /usr/local/bin/orbit.bak'   # 先留底
scp /tmp/orbit root@server:/usr/local/bin/orbit.new
ssh root@server 'mv /usr/local/bin/orbit.new /usr/local/bin/orbit && orbit version'

# 回滚：换回备份二进制
ssh root@server 'mv /usr/local/bin/orbit.bak /usr/local/bin/orbit && orbit version'
```

升级前先对照发布页的 `orbit_${VERSION}_checksums.txt` 校验包（见 [§11.5](#115-本地校验步骤)），
再解包——包内是扁平的 `orbit` + `LICENSE` + `README.md`，没有子目录需要处理。

`orbit version` 输出形如 `v1.2.3 (abc1234, 2026-11-01T00:00:00Z)`；审计日志每条都带版本号，
便于对照"某次变更是哪个版本做的"。

版本来自 `git describe --tags`（构建时注入）。**工作树没有 tag 时兜底为 `v0.0.0-dev`**，
不会拿 commit hash 冒充版本号——commit 与构建时间另有字段：
`./bin/orbit version` 会打印 `v0.0.0-dev (abc1234, 2026-11-01T00:00:00Z)`。
发布二进制请从打了 tag 的提交构建，否则版本号就是 `v0.0.0-dev`。

## 10. 排错速查

命令层面的排错（退出码含义、`[unverified]`、CDN 加速未开启、域名归属、
凭证权限、语言与进度输出）集中在
[用法文档 · 排错](usage/troubleshooting.md)。

只属于运维现场的三件事：

| 现象 | 处理 |
| --- | --- |
| 想确认某次变更 | `jq -c 'select(.kind=="change" and .result=="failed")' ~/.orbit/audit/audit.jsonl`，或按 `run_id` 串起一次执行 |
| 需要回滚 | 见 [§8 回滚](#8-回滚)；证书权威源头仍是签发端（如 1Panel） |
| 感觉"卡住" | 首次关联查询是云端异步任务（上限 5 分钟），`status` 30 分钟内复用缓存；`--progress` 可把进度强制打到 stderr |

## 11. CI 与发布

发布链路只有一处事实源：[`scripts/build-dist.sh`](../scripts/build-dist.sh)。`make dist` 直接调它，
CI 的每个矩阵分片也调它——所以本地打出来的包和 Release 上的包走的是**同一份矩阵、打包与校验逻辑**，
不存在两套会各自漂移的规则。

> 注意措辞：是"同一份构建逻辑"，不是"可复现构建（bit-for-bit）"。脚本会把构建时间写进二进制
> （`-X .../internal/version.Date`），所以两台机器、两个时刻打同一个 tag 的包，sha256 是不同的。
> 用校验和比对时，请比对**同一次构建**产出的文件。

### 11.1 触发条件

仓库自带 [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)，只用官方 action，
不需要任何仓库 secret 或云端凭证（测试全离线）：

| 事件 | 跑什么 | 会发版吗 |
| --- | --- | --- |
| push 到 `main` | `test` → `build`（9 个目标全编） | **否** |
| pull request 到 `main` | `test` → `build`（9 个目标全编） | **否** |
| Actions 页手动 `workflow_dispatch` | `test` → `build`（9 个目标全编） | **否** |
| push 一个以 `v` 开头的 tag | `test` → `build` → `release` | 是（通过双重守卫才发） |

```bash
# 发版：打一个 tag 并 push（tag 与 GPG 签名由维护者手工完成，CI 不签名）
git tag -s v1.2.3 -m "Orbit v1.2.3"
git push origin v1.2.3
```

**tag 过滤用的是宽 glob `'v*'`，这是刻意的。** GitHub 的 tag 过滤是 minimatch glob，
不是正则——写成 `v[0-9]+` 会被当成"字符类里一堆字符"，可能永远不触发。所以 workflow
只拦"v 开头"，真正的严格判定放在 release job 里。

### 11.2 Job 结构与双重守卫

| Job | 依赖 | 内容 |
| --- | --- | --- |
| `test` | — | `gofmt -l .` 检查、`go mod tidy -diff` 检查、`go vet ./...`、`go test -count=1 ./...`；Go 版本取自 `go.mod` |
| `build` | `test` | 矩阵 `fail-fast: false`，9 个分片各跑一次 `bash scripts/build-dist.sh <goos> <goarch> [goarm]`，上传产物（保留 7 天） |
| `release` | `build` | 仅 `refs/tags/v*` 才进入；先过**双重守卫**，再汇总产物、重算校验和、发 GitHub Release |

`release` job 的两道守卫（任一不过就 job 失败、**什么都不发**）：

1. **严格正则**：`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$` —— 形如 `v1.2.3` 或 `v1.2.3-rc.1`；
2. **tag 指向当前提交**：`git describe --tags --exact-match` 的输出必须等于 `GITHUB_REF_NAME`。

守卫通过后依次做三件事：下载全部 9 个分片产物并合并到 `dist/` → 用 `bash scripts/build-dist.sh --checksums`
重新生成 `orbit_<version>_checksums.txt`（并断言 `dist/` 里恰好 9 个包，不是 9 个就失败）→
`sha256sum -c` 全量自检，最后才发布。9 个包的断言是硬闸门——防止某个平台静默缺席。

**prerelease 规则**：tag 名里含 `-`（例如 `v1.2.3-rc.1`、`v1.2.3-beta.2`）→ Release 自动标记
**prerelease**；不含 `-` 的正式版（`v1.2.3`）就是正式 Release。这条规则只看 tag 名，不看其它任何东西。

> `release` 的权限是 `contents: write`（全局默认 `contents: read`），是整条流水线里唯一的写者。
> 另外 tag 触发的 run 永不取消（`cancel-in-progress` 对 `refs/tags/` 为 false）——半发布的
> Release 比排队的 run 更糟。

### 11.3 产物命名与包内结构

命名规则：`dist/orbit_<version>_<goos>_<goarch>[v<goarm>].tar.gz`，Windows 用 `.zip`。
`<version>` 就是构建时的版本串（tag 上是 tag 名；无 tag 时是 `v0.0.0-dev`）。

| 目标 | 产物名 |
| --- | --- |
| linux / amd64 | `orbit_<version>_linux_amd64.tar.gz` |
| linux / arm64 | `orbit_<version>_linux_arm64.tar.gz` |
| linux / armv7 | `orbit_<version>_linux_armv7.tar.gz` |
| linux / 386 | `orbit_<version>_linux_386.tar.gz` |
| darwin / amd64 | `orbit_<version>_darwin_amd64.tar.gz` |
| darwin / arm64 | `orbit_<version>_darwin_arm64.tar.gz` |
| windows / amd64 | `orbit_<version>_windows_amd64.zip` |
| windows / arm64 | `orbit_<version>_windows_arm64.zip` |
| freebsd / amd64 | `orbit_<version>_freebsd_amd64.tar.gz` |

另外还有 **1 个校验和文件**：`dist/orbit_<version>_checksums.txt`，sha256，**9 条**，
一行一个包，按文件名排序（`cd dist && sha256sum -c orbit_<version>_checksums.txt` 可直接用）。

**包内是扁平结构**，没有子目录：

```text
orbit_<version>_linux_amd64.tar.gz
├── orbit          # 静态二进制；Windows 包里是 orbit.exe
├── LICENSE
└── README.md
```

所以 `tar -xzf ... -C /usr/local/bin orbit` 一步就能装好，不用处理路径前缀。

### 11.4 本地打全量包

```bash
source ../.toolchains/env.sh          # 工作区自带 Go 工具链；没有它脚本会报 go is not in PATH
make dist                             # = bash scripts/build-dist.sh --all
# 等价写法（可覆盖版本号）
VERSION=v9.9.9 bash scripts/build-dist.sh --all
```

- `make dist` 与 `bash scripts/build-dist.sh --all` 完全等价；`make release` 是 `make dist` 的
  **兼容别名**（历史名字，产物一模一样）。
- 用 `VERSION=...` 覆盖版本时，包名与校验和文件名都跟着变；不覆盖则取 `git describe --tags --dirty`，
  没有 tag 时兜底 `v0.0.0-dev`。
- 打单个目标：`bash scripts/build-dist.sh linux arm64`；只重算校验和：`bash scripts/build-dist.sh --checksums`。
- **Windows 包需要 `zip` 命令**；缺了脚本会直接报错退出（`apt-get install zip` / `brew install zip`）。

### 11.5 本地校验步骤

```bash
bash scripts/build-dist.sh --all
VERSION="$(git describe --tags --dirty 2>/dev/null || echo v0.0.0-dev)"   # 与脚本同一条命令
cd dist && sha256sum -c "orbit_${VERSION}_checksums.txt"
# 期望：9 行 "<包名>: OK"
```

用 `VERSION=v9.9.9` 打过包，就把上面的 `VERSION=` 换成 `v9.9.9`。

只想校验自己下载的那一个包时（其它 8 条必然报 missing）：

```bash
cd dist && sha256sum -c --ignore-missing "orbit_${VERSION}_checksums.txt"
```

`<version>` 就是 `VERSION` 的值（`v1.2.3` / `v0.0.0-dev` / `v9.9.9` …）。
校验和文件只覆盖**当前版本号**的包，`dist/` 里其它版本的残留文件不参与。

> GitHub 把每个包放进一个 zip 再打包，所以校验和必须对**解压后**的内容算——
> release job 先 `merge-multiple: true` 摊平到 `dist/` 再 `--checksums`，正是这个原因。

### 11.6 在分支上验证构建（不发布）

改完构建脚本或 CI 之后，不必先打 tag 再验证：

```bash
make check                            # = fmt-check + vet + test（CI 的 test job 另含 go mod tidy -diff）
bash scripts/build-dist.sh --all      # 与 CI 的 9 个矩阵分片等价
```

推到 `main` / 开 PR / 在 Actions 页手动 `workflow_dispatch` 时，CI 会把 9 个目标全编一遍，
产物以 `orbit-<name>` 命名的 artifact 上传（`orbit-linux-amd64`、`orbit-linux-armv7`、`orbit-darwin-arm64` …），
**保留 7 天，且不会发布任何 Release**。分支上的构建版本号固定是 `v0.0.0-dev`——
只有 tag 触发的 run 才把 tag 名写进二进制，这是刻意的，避免把开发构建误标成某个版本。

### 11.7 失败排查

| 现象 | 处理 |
| --- | --- |
| `gofmt is required on:` | `make fmt` 后重新提交 |
| `go mod tidy would rewrite go.mod/go.sum` | `make tidy` 后提交 `go.mod` / `go.sum` |
| `go is not in PATH` | `source ../.toolchains/env.sh`（本地跑脚本时最容易漏） |
| `'zip' is required to package Windows builds` | 装 `zip`：`apt-get install zip` / `brew install zip` |
| `expected 9 packages, found N` | 某个矩阵分片没成功或产物没合并全；先看 `build` job 里哪个 `build (...)` 红了 |
| `'<tag>' is not a v-semver release tag` | tag 名不匹配第一道守卫的正则；确认写法是 `v1.2.3` 形式（不要 `v1.2`、`release-1.2.3`） |
| `git describe --tags --exact-match returned ...` | checkout 没拿到 tag（`fetch-depth: 0` 已配）或 tag 指向了别的提交；重新 checkout 并确认 tag 落在该提交上 |
| `sha256sum -c` 报 `FAILED` | 下载被改写/截断，删掉重下；本地则重新 `--all` 打包 |
| 版本号变成 `v0.0.0-dev` | 当前提交没有 tag（`git describe` 取不到）；发版构建必须在 tag 的提交上 |

> 排发布失败时，**顺序**是：先看 `test`（格式化/tidy/vet/测试）→ 再看 `build` 的 9 个分片 →
> 最后才是 `release` 的守卫与校验和。守卫失败不会留下半成品 Release。

### 11.8 为什么不用 GoReleaser

选"自写脚本 + 官方 action"而不是 GoReleaser，是三条硬需求：

1. **零外部工具依赖**：仓库里只需要 Go、`tar`、`zip`、`sha256sum`；不引第三方二进制、
   不需要额外的 `.goreleaser.yaml` 与版本锁定。
2. **本地可离线完全复现同一套流程**：`bash scripts/build-dist.sh --all` 在无网机器上跑出的是
   和 CI 相同的 9 个包、相同的命名与相同的校验和格式，不需要"先在本地模拟一遍 CI"。
3. **产物可逐字节校验**（对同一次构建）：校验和由脚本自己生成并在发布前用 `sha256sum -c` 自检，
   Release job 只重算与校验，不另起一套打包逻辑；矩阵、打包、校验和都在同一个脚本里，
   本地与 CI 不会各自漂移。构建时间会写进二进制，因此不同次构建的哈希本就不同。

**tag 与 GPG 签名由维护者手工完成**，CI 不签名；release job 只发布已存在的 tag 产物。
没有 tag 的本地构建版本号兜底 `v0.0.0-dev`（见 [§9](#9-升级与回滚版本)）。

## 12. License

Orbit 以 **MIT** 授权，全文见仓库根 [`LICENSE`](../LICENSE)。可自由使用、修改与再分发
（含商用），保留版权声明与许可声明即可；软件按"现状"提供，不含任何明示或默示担保。
