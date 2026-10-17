# Orbit 项目实现方案与技术栈选型说明

> 文档状态：**已审阅通过（v1.0，可开工）** ｜ 项目代号 Orbit ｜ 命令 `orbit` ｜ 首批平台：腾讯云
> A1–A9 与 B1–B7 均已确认，决策见 [DECISIONS.md](../DECISIONS.md)；本文档自 v1.0 起作为实现依据，后续变更走 DECISIONS 变更日志。
> 相关文档：《[Tencent API 事实清单](TENCENT_API_NOTES.md)》（逐条事实 + 来源 + 待实测项）、[DECISIONS.md](../DECISIONS.md)（决策记录）
> **v0.4 变更**（文档调研回填）：① CDN 默认写路径改为 `ModifyDomainConfig(Route="Https.CertInfo.CertId")` **单点更新**，整对象回写风险消除；② EdgeOne 默认写路径 `ModifyHostsCertificate(Mode="sslcert")`，`Mode` 枚举已确认；③ EdgeOne"双证书体系"从**风险**降级为**已明确的语义**（按 `Mode` 判定），仅剩一个业务选择（B2b）；④ `Https.SslStatus` 取值确认（closed/deploying/deployed/failed）；⑤ teo 的 Domain 定位方式确认（全局唯一、无需 ZoneId）；⑥ 待实测清单从 3 项 P0 降为 5 项参数级打磨，工期 20 → 19.5 人日。
> **v0.3 变更**：① 按 A1–A9 收窄范围到 **CDN + EdgeOne**（CLB 等挪到后续批次）；② 凭证改为**用户手动录入 + Orbit 自管存储**，禁止读取 `~/.tccli`；③ 全局配置目录 `~/.orbit`，新增多语言（i18n）；④ `update` 候选池 = 全部证书，`clean` 只作用于 `FilterSource=upload`；⑤ CDN/TEO 字段用 SDK 源码核实。

---

## 0. 一页速读（TL;DR）

- **它是什么**：装在服务器或运维机上的单文件 CLI/TUI 工具，把「腾讯云 SSL 证书池」与「**CDN 加速域名 / EdgeOne 站点域名**」对齐，自动完成 **证书应用（`update`）**、**全景巡检（`status`）**、**无用证书清理（`clean`）**。
- **范围**：首批只支持 **CDN（`cdn`）与 EdgeOne（`teo`）**；多账号从第一天就按 N 个账号建模（你现有 2 个）。
- **它不做什么**：不申请/续签证书（1Panel 负责）、不上传证书到腾讯云（你现有 tccli 定时任务负责）、不创建资源、不做 HTTPS 解绑、不读 `~/.tccli` 凭证。
- **技术栈**：Go 1.27 + Cobra + Bubble Tea/Bubbles/Lip Gloss + 官方 `tencentcloud-sdk-go`；module `github.com/Crequency/orbit`；静态单文件二进制。
- **四条决定性事实**（塑造了整个设计）：
  1. **证书是全局资源**（SSL 证书接口不需要 Region），而 v1 的两个产品（CDN、EdgeOne）**部署也都不需要 Region** → **v1 完全没有地域扇出**，引擎比预想简单得多（能力位仍保留，供后续 CLB 等产品使用）。
  2. **同一（证书 × 资源类型）同时只能有一个部署任务**，部署是异步的，`DeployStatus=0` 表示"已有进行中任务"而非"排队成功" → 按该维度串行 + 轮询到终态 + **回读复核**。
  3. **批量删除证书的 `IsSync` 默认「不检查云资源关联」** —— 官方默认值与安全预期相反，Orbit 必须恒传 `true`（代码 + 单测锁死）。
  4. **EdgeOne 的证书语义由 `Certificate.Mode` 决定**：`sslcert`（用腾讯云 SSL 托管证书）/ `eofreecert*`（EdgeOne 自带免费证书，`CertId` 形如 `teo-xxx`、不在 SSL 池中）/ `disable` → 判定"是否需要更新"**必须先读 `Mode`**，否则会把免费证书域名误判成"无证书"。详见 §8.3。
- **安全**：默认演练（dry-run 是独立开关，`--yes` 才写）；所有变更走"计划 → 预览 → 门禁 → 执行 → 回读复核"；`clean` 的"未被使用"证据来自**腾讯云自己的证书—资源关联查询**。
- **待你拍板**：§12 的 B1–B7（都是"确认默认值"级别，逐条给一句话即可）。

---

## 1. 需求复述与范围确认

### 1.1 你现在的工作流

```
① 1Panel 申请 SSL 证书 → 落到服务器本地目录
      ↓
② 定时任务：tccli 把证书上传到腾讯云 SSL 证书服务
      ↓
③ 【手工·痛点1】逐个 CDN 域名 / EdgeOne 域名点选并应用证书
      ↓
④ 【手工·痛点2】翻证书列表，人工判断哪些证书没有资源在用，逐个删除
```

### 1.2 痛点本质

| 痛点 | 本质 | 人工为什么累 |
| --- | --- | --- |
| 应用证书 | **域名 × 证书 × 资源**三方匹配，资源分散在两个产品、多个站点/域名 | 控制台没有"以证书为中心"的视图；EdgeOne 还要先进站点再进域名 |
| 删除无用证书 | 需要先知道"哪些资源引用了它"，而这个信息不在证书列表页 | 只能靠记忆与肉眼比对；删错等于线上 HTTPS 挂掉 |
| 续期搬运 | 新旧证书同域并存，要判断"哪个域名还在用旧证书" | 每次人工巡检，容易漏 |

### 1.3 目标（可验收口径）

1. `orbit update --ssl --platforms=tencent` 执行后，**所有在管辖范围内、域名匹配得上、且当前不是最优证书的 CDN/EdgeOne 域名**，其证书都被更新为最优证书；重复执行结果不变（幂等）。
2. `orbit status` 以多 Tab TUI 展示"账号 → 产品 → 站点/域名"全景，并告诉你是哪些域名需要更新、哪些证书可清理。
3. `orbit clean --ssl --platforms=tencent` 能识别并删除"无任何云资源使用"的证书，且**在误删可能性不为零时不删**。
4. 三个命令都能在无人值守（cron / 1Panel 定时任务）下运行：无 TTY 时自动降级为纯文本/JSON + 明确退出码。
5. **多账号**：两个账号各自独立执行、独立展示；账号选择支持"命令行指定"与"TUI 多选后批量执行"。

### 1.4 非目标（第一批）

- ❌ 证书申请、DNS 验证、自动续签（1Panel 负责）。
- ❌ 证书上传/导入到腾讯云 SSL（你的 tccli 定时任务负责；`orbit push` 只**预留接口**，见 A7）。
- ❌ 创建/接入资源：不新建 CDN 域名、不在 EdgeOne 添加站点或加速域名。
- ❌ **HTTPS 解绑 / 停用**：SSL 部署接口没有解绑语义（唯一例外是云直播），解绑必须走产品 API → 不在 v1。
- ❌ CLB / WAF / API 网关 / COS / 直播 / 点播 / TKE（后续批次，架构已预留）。
- ❌ 多用户/多租户服务端形态：它是**单机 CLI**。

### 1.5 你的答复已编码进设计（A1–A9）

| # | 你的决策 | 落地方式 |
| --- | --- | --- |
| A1 | 两个账号，但按多账号编码；**禁止读 `~/.tccli/*.credential`**，用户手动录入、Orbit 自行存储 | `orbit account add` 交互录入（隐藏输入）→ `~/.orbit/credentials.yaml`（0600）；凭证支持 env 覆盖；不实现 tccli profile 读取（B1 待你选存储形态） |
| A2 | 应用/匹配管理**全部证书**；删除只作用于 `FilterSource=upload` | `update` 候选池 = 全量；`clean` 候选池 = `FilterSource=upload` ∩ 额外范围限定（B3） |
| A3 | 首批只支持 **CDN + EdgeOne** | Provider 的 `Products = {cdn, teo}`；其余返回"未实现"并在 TUI 标注 |
| A4 | 「未被引用 且 同域名更优证书已存在 且 超 N 天」，删前备份，免费证书不清理 | §2.3 的 G1–G4 + 备份步骤 + `include_free=false` 默认 |
| A5 | TUI + CLI 双模式；`--yes` 直接执行，`--dry-run` 演练 | §3.5 行为矩阵：默认（TTY）出计划并交互确认；非 TTY 无 `--yes` 时**不写云端**（B6 确认退出码） |
| A6 | 首批无 CLB → 地域问题不适用 | v1 无地域扇出；能力位 `DeployNeedsRegion` 保留 |
| A7 | `orbit push` 预留接口，第一批不做 | Provider 接口预留在 §4.2（`UploadCertificate` 能力位），CLI 不暴露命令 |
| A8 | `github.com/Crequency/orbit`，仓库在 `tools/orbit/` | go.mod module 路径已定 |
| A9 | 选型通过；多语言；`orbit config --global language="en-US"`；全局配置 `~/.orbit` | §3.7 i18n + §6 目录布局 |

---

## 2. 核心难题拆解

### 2.1 难题一：域名 × 证书的匹配与"最优证书"选优

**候选池**：按 A2 = **全部证书**（不限定来源），但做状态过滤。

**匹配规则（`Cover(domain, cert)`）**

| 规则 | 说明 |
| --- | --- |
| 归一化 | 小写、去尾部 `.`、IDN 转 punycode（`golang.org/x/net/idna`）后再比较 |
| 精确匹配 | `domain ∈ {cert.Domain} ∪ cert.SubjectAltName ∪ cert.CertSANs`（**三处取并集**：详情接口的 SAN 不含主域名，列表接口的含） |
| 泛域名匹配 | 形如 `*.example.com`：按 RFC 6125 **只匹配一级**（匹配 `a.example.com`；**不匹配** `example.com`，也**不匹配** `a.b.example.com`） |
| 状态过滤 | 排除 `Status ∈ {0审核中,2审核失败,3已过期,9吊销中,10已吊销,11重颁发中,14已退款,15迁移中}`、`Deployable=false`、以及 `HostingStatus` 处于托管中的证书 |
| 过期容忍 | 默认只选有效期内的证书；`--allow-expired` 才放宽（排障用） |

**选优规则（对同一域名有多个候选时，按优先级依次比较）**

| 优先级 | 比较项 | 方向 | 理由 |
| --- | --- | --- | --- |
| 1 | 有效期 | 有效 > 无效 | 不能把过期证书推上去 |
| 2 | 匹配精确度 | 精确 CN > 精确 SAN > 泛域名 | 越精确越不易被审核拒绝 |
| 3 | 泛域名后缀长度 | 更长者优先 | `*.a.example.com` 比 `*.example.com` 精确 |
| 4 | `CertEndTime` | **更晚者优先** | ← 续期场景的核心：新证书自动胜出 |
| 5 | `InsertTime` | 更晚者优先 | 同到期时间的平局 |
| 6 | 证书来源 | `From=upload` > 腾讯云签发（可配置，见 B2） | 你上传的证书最可控 |
| 7 | `CertificateId` | 字典序（仅为结果稳定可复现） | 保证计划可复现、diff 不抖动 |

**为什么第 4 条是关键**：1Panel 签发的新证书上传后，与旧证书的域名完全相同 → "选到期更晚的那张"就等价于"选新证书"，**不需要任何本地台账**。

**EdgeOne 的特殊性**：EdgeOne 站点域名的"当前证书"可能是 **EdgeOne 自己签发的免费证书**（`DescribeDefaultCertificates` → `DefaultServerCertInfo`），其 `CertId` 未必是腾讯云 SSL 证书 ID。因此：
- **候选池只取腾讯云 SSL 证书**，EdgeOne 默认证书**不参与选优**；
- EdgeOne 域名的证书状态由 `Certificate.Mode` 决定（`sslcert` / `eofreecert*` / `disable`）：`Mode != "sslcert"` 时其 `CertId` 不在 SSL 证书池中，标记为 `ForeignCert`（外部证书）并在 TUI 与计划中显式展示；是否把它替换成你上传的证书由 `edgeone.replace_free_cert` 决定（默认值见 B2b）。

**到期时间的权威来源**：统一用 SSL 证书池的 `CertEndTime`。CDN 侧 `ServerCert.ExpireTime` 与 EdgeOne 侧 `ServerCertInfo.ExpireTime` 都存在，但仅作**交叉校验**（两者不一致时告警，提示证书池与资源侧漂移）。

### 2.2 难题二：应用动作的异构、异步与"是否真的生效"

好消息是：**两个产品各有"原生"写路径，且都能被回读验证**；SSL 侧的 `DeployCertificateInstance` 是第三条统一通道。文档核对后，v1 的策略**按产品分开**（不再用一条路径硬套两个产品）：

| 策略 | 接口 | 适用 | 状态 |
| --- | --- | --- | --- |
| **S-CDN（CDN 默认）** | `ModifyDomainConfig{Domain, Route="Https.CertInfo.CertId", Value="{\"update\":\"<CertId>\"}"}` | CDN 换证书 | ✅ **文档已确认**：官方示例 4 就是"更新 HTTPS 服务器证书 ID"；**点分路径单点更新，非叶子节点配置保持不变** → 彻底避开整对象回写风险 |
| **S-TEO（EdgeOne 默认）** | `ModifyHostsCertificate{ZoneId, Hosts[], Mode="sslcert", ServerCertInfo=[{CertId}]}` | EdgeOne 换证书 | ✅ **文档已确认**：`Mode` 枚举为 `disable` / `eofreecert` / `eofreecert_manual` / `sslcert`，`ServerCertInfo` 仅 `Mode=sslcert` 生效且传 SSL 证书 ID |
| **S-UNI 统一部署**（备用/批量） | `DeployCertificateInstance(certId, ResourceType=cdn\|teo, InstanceIdList)` | 一次把某证书批量打到一批域名 | ✅ 逐字段已核实；`cdn` 用 `Domain\|on/off`，`teo` 用 `Domain`；都不需要 Region。注意它会占用"同证书同资源类型"的任务名额 |
| **S-REPLACE 旧证换新证**（可选优化） | `UpdateCertificateInstance(OldCertificateId → CertificateId)` | 能配对出"旧→新"时 | ⚠️ 入参结构未实测，默认关闭 |

**执行层的三条硬约束（官方原文）**

1. **同一「证书 ID × 资源类型」同时只能存在一个部署任务** → Orbit 按该维度**串行化**，否则报 `FailedOperation.CertificateDeployHasPendingRecord`（或拿到 `DeployStatus=0`）。
2. **部署是异步的**：`DeployCertificateInstance` 返回 `DeployRecordId` + `DeployStatus`，其中 **`DeployStatus=0` 表示"当前已有部署中的任务、本次未创建新任务"** → 必须查 `DescribeHostDeployRecord` / `...Detail` 到终态（`0`未开始 / `1`成功 / `2`失败）。而 `ModifyDomainConfig` 与 `ModifyHostsCertificate` **响应里根本没有 task id** → 只能靠回读。
3. **回读复核（read-back verification）**：
   - **CDN**：`DescribeDomainsConfig` → `Https.SslStatus ∈ {closed, deploying, deployed, failed}` 轮询到 `deployed`，并比对 `Https.CertInfo.CertId`
   - **EdgeOne**：`DescribeAccelerationDomains` → `Certificate.Mode` 与 `Certificate.List[].Status ∈ {deployed, processing, applying, failed, issued}` 轮询到 `deployed`，并比对 `List[].CertId`
   - 统一交叉校验：`DescribeHostCdnInstanceList` / `DescribeHostTeoInstanceList`（SSL 侧的"证书 → 实例"视图）
   不一致则重试或标记失败，**不以接口返回成功为准**。

> **写路径闸门（已收窄）**：原先担心的"EdgeOne 写语义未验证"已由文档解决，**常规换证书不再需要实验性开关**。保留的闸门只针对两件事：① 未实测的 `UpdateCertificateInstance`（S-REPLACE）；② 把 EdgeOne 域名从 **EdgeOne 免费证书**（`Mode=eofreecert*`）切换到 SSL 托管证书 —— 这是行为变更（免费证书 90 天自动续期 → 改用你上传的证书），默认**列入计划并明确标注**，可用 `edgeone.replace_free_cert=false` 改成"只报告不动"（见 B2b）。

### 2.3 难题三：删除的安全边界（最重要）

```
G1 范围过滤（此处按 A2/A4）
   clean 候选池 = FilterSource=upload（用户上传）∩ 额外范围限定（B3：域名白名单/别名前缀/标签）
   → CreateCertificateBindResourceSyncTask(CertificateIds=[...≤100], IsCache=0)
   → 轮询 DescribeCertificateBindResourceTaskResult 至 Status=1
   → 证据 = 所有 (ResourceType, Region) 的 TotalCount=0 且 Error 为空
   → 交叉验证：CDN / EdgeOne 枚举结果里也没有域名引用该 CertId（双重保险）
   → G3 取代判定 + G4 宽限/保护 → 备份 → DeleteCertificates(IsSync=true) → 轮询结果 → 分类收尾
```

⚠️ 证书列表里的 `BoundResource` 字段**文档明文标注"暂不可用"** → 绝对不能拿它做判断。

**四道闸门（全部满足才允许删除）**

| 闸门 | 内容 | 失败的后果 |
| --- | --- | --- |
| **G1 范围** | v1：证书必须 `FilterSource=upload`（**全量，不再叠加别名/标签/域名白名单**——按 B3，范围层下一版再做）；腾讯云签发与免费证书绝不进入删除流程 | 只删你自己推送上去的证书；但手工上传且无引用的证书也会进入候选 → 由备份 + 确认 + G2/G3/G4 + `protect_patterns` 兜底（见 DECISIONS §3.1 已接受风险） |
| **G2 证据完整** | 关联查询 `Status=1`、无任何 `Error`、非"查询超时"；`IsCache=0` 实时结果；CDN/TEO 枚举无分页截断；只扫子集时必须显式 `--permit-partial-scan` | 杜绝"缓存过期/查询异常"导致的误判 |
| **G3 已被取代** | 证书覆盖的**每个域名**都存在另一张更优证书（规则同 §2.1） | 只删"续期后的旧证书"，不删"尚未启用的新证书" |
| **G4 宽限与保护** | 年龄 > `clean.older_than`（默认 7 天）；不匹配 `protect_patterns`；`include_free=false`（默认）；`HostingStatus` 托管中不删 | 留出后悔窗口，避开免费证书 1 小时规则与托管续期链 |

**删除是异步的，且要读懂任务状态**：`DeleteCertificates(IsSync=true)` → `CertTaskIds` → `DescribeDeleteCertificatesTaskResult` 的 `Status`：`0`进行中 / `1`成功 / `2`失败 / **`3`未授权服务角色** / **`4`有未解绑的云资源** / **`5`查询关联云资源超时**。其中 `4`/`5` 一律视为"保留"，**绝不重试硬删**。

---

## 3. 产品形态与命令面

### 3.1 命令总览

```
orbit                                  # 无参数 = TUI 总览（等价 orbit status；无 TTY 时打印摘要）
orbit status                           # 只读：多 Tab TUI（账号 → 产品 → 站点/域名）
      --platforms=tencent              #   平台（alibaba 为占位）
      --accounts=main,hk | --all-accounts
      --products=cdn,teo               #   默认取配置
      --watch=30s
      --format=table|json|ndjson

orbit update --ssl                     # 核心：匹配 + 应用证书
      --platforms=tencent
      --accounts=main,hk | --all-accounts
      --products=cdn,teo
      --domains=www.example.com,*.a.com
      --strategy=auto|deploy|replace   #   见 §2.2（默认 auto = S2）
      --dry-run                        #   只出计划，不写云端
      --yes                            #   跳过交互确认，直接执行
      --enable-experimental-write      #   允许尚未实测确认的策略（当前仅 S-REPLACE 旧证换新证）
      --fail-on-changes                #   有变更则退出码 3（给 cron 用）

orbit clean --ssl                      # 清理无资源使用的证书
      --platforms=tencent
      --older-than=7d
      --include-free                   #   默认排除免费证书
      --permit-partial-scan            #   危险：放宽 G2
      --dry-run / --yes
      --backup-dir=~/.orbit/backups    #   默认即此目录

orbit account list|add|remove|test     # 账号增删查 + 连通性/权限/服务角色自检
orbit config show|path|set             # 例：orbit config --global language="en-US"
orbit doctor                           # 环境自检：配置、凭证、服务角色、权限、限流、语言包
orbit version
```

全局参数：`--config <path>`、`--global` / `--local`（配置作用域）、`--lang=zh-CN|en-US`、`--no-tui`、`--color=auto|always|never`、`--log-level`、`--log-file`、`--json-errors`。
环境变量：`ORBIT_CONFIG`、`ORBIT_HOME`（默认 `~/.orbit`）、`ORBIT_LANG`、`ORBIT_NO_TUI`、`NO_COLOR`，以及凭证注入变量 `ORBIT_TENCENT_<ACCOUNT>_SECRET_ID` / `_SECRET_KEY`。

### 3.2 `orbit update` 交互流程

```
orbit update --ssl --platforms=tencent
  ├─ 解析配置 → (平台 → 账号列表)
  ├─ 账号数 > 1 且是 TTY？
  │     ├─ 是 → TUI 多选页（↑/↓ 移动，空格勾选，a 全选，回车确认）→「本次将对 2 个账号执行，是否继续？」
  │     └─ 否 → 必须给 --accounts 或 --all-accounts，否则报错退出（退出码 4）
  ├─ [前置自检] 凭证可用？服务角色已授权？API 权限？语言包？→ 失败即退出（附 CAM 策略片段）
  ├─ [只读阶段] 每账号并发拉取：证书池 + CDN 域名配置 + EdgeOne 站点/域名 + 证书↔资源映射
  ├─ [计算阶段] 纯函数匹配 → 执行计划 Change[]
  ├─ [预览阶段] 计划表：账号 | 产品 | 站点/资源 | 域名 | 当前证书 → 目标证书 | 策略 | 风险
  │     └─ --dry-run：打印后退出（不写云端）
  │     └─ 无 TTY 且无 --yes：打印后退出，不写云端
  ├─ [执行阶段] 账号串行 → 按（证书 × 资源类型）串行 → 异步任务轮询 → 回读复核
  └─ [总结] 成功/跳过/失败计数 + 失败明细（错误码 + 可行动建议）+ 退出码
```

### 3.3 `orbit status` TUI 布局

```
┌ Orbit ────────────────────────────────────────────────────────────────────┐
│ [1 腾讯云·main*] [2 腾讯云·hk] [3 阿里云·(未实现)]            ? 帮助  L:中文 │
├───────────────────────────────────────────────────────────────────────────┤
│ 账号 main · 凭证 OK · 服务角色 OK · 产品 cdn+teo · 12s 前同步               │
│                                                                           │
│  证书 42 张 │ 已部署 31 │ 未部署 9 │ 30 天内到期 4 │ 已过期 1              │
│                                                                           │
│  ⚠ 待处理（5）                                          按 u 生成应用计划   │
│  ┌──────────┬──────────────────────┬──────────────┬──────────────┬──────┐│
│  │ 产品/资源 │ 域名                  │ 当前证书      │ 目标证书      │说明  ││
│  ├──────────┼──────────────────────┼──────────────┼──────────────┼──────┤│
│  │ CDN      │ www.example.com       │ c-1001(已过期)│ c-1002(新)   │      ││
│  │ EdgeOne  │ cdn.example.com       │ EO 默认证书 ★ │ c-1002       │外部  ││
│  │ EdgeOne  │ api.example.com       │ 未绑定        │ c-1003       │      ││
│  └──────────┴──────────────────────┴──────────────┴──────────────┴──────┘│
│                                                                           │
│  🗑 可清理（3）                                          按 c 生成清理计划   │
│  ┌────────────────┬──────────────────────┬──────────────┬───────────────┐ │
│  │ 证书 ID         │ 覆盖域名              │ 到期          │ 取代者        │ │
│  └────────────────┴──────────────────────┴──────────────┴───────────────┘ │
├───────────────────────────────────────────────────────────────────────────┤
│ 1-9 切页 tab 切页 r 刷新 u 应用 c 清理 / 过滤 L 切换语言 ? 帮助 q 退出      │
└───────────────────────────────────────────────────────────────────────────┘
```

要点：
- **一个 Tab = 一个（平台，账号）**，页签带健康标记（`*` = 凭证/服务角色/限流异常）。
- 中部两块**行动导向**列表（待处理 / 可清理）；`★ 外部证书` 表示该域名的证书不在腾讯云 SSL 池中（EdgeOne 默认证书等），这是 EdgeOne 特有的语义提示。
- `u` / `c` **不直接执行**，跳转到与 CLI 完全相同的"计划预览 → 确认"流程 —— TUI 只是引擎的一个视图。

### 3.4 `orbit clean` 交互流程

```
orbit clean --ssl --platforms=tencent
  ├─ [前置自检] 服务角色 SSL_QCSLinkedRoleInReplaceLoadCertificate 已授权？（未授权 → 引导链接 + 退出码 4）
  ├─ 证书池 → G1 过滤（FilterSource=upload ∩ 额外范围限定）
  ├─ 建关联查询任务（IsCache=0）→ 拿权威"被引用情况"
  ├─ G2 证据完整性校验 → G3 取代关系判定 → G4 宽限/保护/免费/托管排除
  ├─ 交叉验证：CDN 域名配置 + EdgeOne 站点域名里都没有引用这些 CertId
  ├─ 展示候选表（含证据列：查询时间 / 覆盖产品与地域 / TotalCount 全 0 / 取代者）
  ├─ 二次确认（TUI 输入 y 或 ctrl+d；CLI 需 --yes）
  ├─ 备份到 --backup-dir（可下载则导出材料，否则至少导出元数据）
  ├─ DeleteCertificates(IsSync=true) → 轮询 DescribeDeleteCertificatesTaskResult
  └─ 分类收尾：已删除 / 因"有未解绑资源(4)"保留 / 因"查询超时(5)"保留 / 失败(错误码)
```

**"宁可少删"**：任何一条候选只要证据链不完整，就不删，以 `SKIPPED(incomplete-evidence)` 报出并给补齐建议。

### 3.5 非交互 / 无人值守行为矩阵（按 A5）

| 场景 | 行为 |
| --- | --- |
| TTY，无 `--yes` 无 `--dry-run` | 出计划 → 交互确认（默认 Enter=否）→ 确认后执行 |
| `--dry-run` | 出计划，**绝不写云端**；退出码 0（`--fail-on-changes` 时为 3） |
| `--yes` | 出计划 → 直接执行（非交互） |
| **非 TTY，无 `--yes`** | 出计划，**不写云端**，退出码 0（`--fail-on-changes` 时为 3）—— 见 B6 待确认 |
| 账号歧义且无 TTY | 报错退出（退出码 4），提示 `--accounts` 或 `--all-accounts` |
| 未实测策略（S-REPLACE 旧证换新证）未加 `--enable-experimental-write` | 该计划条目标记 `BLOCKED(experimental)`，不执行（退出码 2 或 4） |

**退出码契约**

| 码 | 含义 |
| --- | --- |
| 0 | 成功（含"无变更"、含"演练完成"） |
| 1 | 致命错误（配置/凭证/网络/参数） |
| 2 | 部分失败（计划内条目执行失败，明细在输出与审计） |
| 3 | 计划模式存在待执行变更（仅 `--fail-on-changes`） |
| 4 | 安全闸门拒绝执行（证据不完整、账号歧义、服务角色未授权、实验性写被阻断） |

### 3.6 多账号模型（按 A1）

- `~/.orbit/credentials.yaml` 存多个账号；`~/.orbit/config.yaml` 为每个账号配置产品、范围、地域（v1 无地域，字段保留）。
- **账号是一等公民**：计划、执行、审计、结果表都带 `account` 维度；账号之间默认串行（日志清晰），可 `--parallel-accounts`。
- TUI 中账号是 Tab；CLI 中账号是过滤条件；两者共用同一份 Provider 调用路径。
- 凭证解析优先级：命令/环境变量注入 > `~/.orbit/credentials.yaml` > 报错（**不读 `~/.tccli`**）。

### 3.7 国际化（按 A9）

- 语言：内置 **zh-CN** 与 **en-US** 两套文案（`internal/i18n`，用 `go:embed` 内嵌 JSON 语言包 + `golang.org/x/text/language` 做 locale 匹配/回退）。
- 设置方式：`orbit config --global language="en-US"`、`orbit config set language en-US`、`--lang=en-US`、`ORBIT_LANG=en-US`；优先级 flag > env > 配置 > `$LANG` > 默认 zh-CN。
- 覆盖范围：TUI 全部文案、CLI 的提示/警告/错误建议、表格表头、`--help` 文本。
- **不国际化**：JSON 输出的字段名与**错误码**（保持机器可读、与腾讯云错误码对齐）；审计日志字段名。
- 语言包缺失 key 时回退到 zh-CN 并在 TUI 底部显示一次提示（开发期便于发现漏翻）。

---

## 4. 架构设计

### 4.1 分层

```
        ┌─────────────────────────── 表现层（只渲染与按键） ─────────────────────────────┐
        │  render/tui (Bubble Tea)     render/plain (表格文本)     render/json (结构化)    │
        │  i18n: 语言包 + 消息键（表现层负责翻译，引擎只抛消息键与参数）                     │
        └───────────────────────────────────┬───────────────────────────────────────────┘
                                            │  读：Report / Plan    写：Plan → 确认 → Engine.Run
        ┌───────────────────────────────────▼───────────────────────────────────────────┐
        │                              核心引擎 core                                     │
        │  model  match(纯函数)  plan(计划/策略/风险)  engine(执行/串行/重试/回读复核)      │
        │  safety(G1–G4)  audit(jsonl)  i18n(消息键)                                     │
        └───────────────────────────────────┬───────────────────────────────────────────┘
                                            │  Provider 接口（按能力建模）
        ┌───────────────────────────────────▼───────────────────────────────────────────┐
        │  cloud/tencent（v1: cdn + teo）    cloud/alibaba（占位）    cloud/fake（测试）    │
        └───────────────────────────────────┬───────────────────────────────────────────┘
                                            │
                         腾讯云 SDK（限流、重试、分页、Zone 扇出、异步任务轮询）
```

**铁律**：表现层不含业务判断；引擎不 import TUI 包；`match` 为纯函数（无 IO，时间注入）→ 可穷举单测；**引擎只抛消息键 + 参数，翻译全部在表现层**（否则 i18n 会渗透进核心逻辑）。

### 4.2 Provider 抽象

```go
type Provider interface {
    Name() string
    Accounts(ctx) ([]AccountRef, error)
    Capabilities() Capabilities
    ListCertificates(ctx, AccountRef, ScanScope) ([]Certificate, error)
    ListResources(ctx, AccountRef, ScanScope) ([]Resource, error)      // CDN 域名 / EdgeOne 站点域名
    ListBindings(ctx, AccountRef, []CertID) ([]Binding, error)          // 证书 ↔ 资源（权威）
    Deploy(ctx, AccountRef, []Deployment) ([]DeployResult, error)
    DeleteCertificates(ctx, AccountRef, []CertID, DeleteOptions) ([]DeleteResult, error)
    Preflight(ctx, AccountRef) ([]Check, error)
    // 为 A7 预留（第一批返回 ErrNotImplemented）：
    UploadCertificate(ctx, AccountRef, LocalCert) (CertID, error)
}

type Capabilities struct {
    Products            map[Product]ProductCapability
    CertGlobal          bool            // 腾讯云 = true
    DeployNeedsRegion   map[Product]bool // v1: cdn=false, teo=false
    AsyncDeploy         bool            // true
    DeploySerialPerPair bool            // true：(证书 × 资源类型) 串行
    SupportsUnbind      bool            // false（v1 不做解绑）
    VerifyAfterDeploy   bool            // true（回读复核）
    ExtraScopes         []string        // teo 需要 Zone 扇出
}

type ProductCapability struct {
    CanEnumerate, CanReadBoundCert, CanApplyByCertID, CanVerifyAfterDeploy bool
    Granularity  string   // cdn=domain, teo=domain-in-zone
    OwnCertField string   // cdn: Https.CertInfo.CertId; teo: AccelerationDomain.Certificate.List[].CertId
    Experimental bool     // 策略是否尚未实测确认（当前仅 S-REPLACE=true；cdn/teo 的常规换证书均已文档确认）
}
```

阿里云接入时只需实现同一接口（其证书服务 + 部署任务模型不同，但能力位可表达），核心引擎与 TUI 零改动。

### 4.3 目录结构

```
orbit/
├── go.mod / Makefile            # module github.com/Crequency/orbit
├── cmd/orbit/main.go            # 仅装配 + cli.Execute()
├── internal/
│   ├── cli/                     # cobra 命令、flag、TTY 探测、退出码映射
│   ├── config/                  # ~/.orbit 与本地配置、作用域合并、0600 校验
│   ├── credential/              # 录入/存储/解析（含可选加密）、脱敏
│   ├── i18n/                    # 语言包（go:embed）+ 消息键 + locale 协商
│   ├── core/
│   │   ├── model.go  match.go   # ★ 匹配与选优（纯函数）
│   │   ├── plan.go              # 计划、策略选择、风险分级、幂等判定
│   │   ├── engine.go            # 串行化、异步轮询、回读复核、重试
│   │   ├── safety.go            # G1–G4
│   │   └── audit.go
│   ├── cloud/{provider.go,tencent/{client,ssl,cdn,teo}.go,alibaba,fake}
│   ├── render/{tui,plain,json}/ # tui: app.go tabbar.go accountpicker.go plantable.go progress.go
│   └── version/
├── locales/{zh-CN.json,en-US.json}
├── docs/                        # 本文档、TENCENT_API_NOTES.md、VERIFY.md
├── testdata/                    # 匹配用例、API 样本、golden
└── scripts/                     # 打包、cron/systemd 示例
```

### 4.4 领域模型（要点）

```go
type Certificate struct {
    ID, Alias, From string          // From: upload / trustasia / wosign / sheca
    Domain string; SANs []string
    NotBefore, NotAfter time.Time   // CertBeginTime / CertEndTime
    Status uint64; PackageType string  // null=上传, 83=免费, 其它=付费
    Deployable bool; HostingStatus int64; AllowDownload bool
    Tags map[string]string
    Region string                   // 腾讯云恒空（为阿里云预留）
}

type Resource struct {              // CDN 域名 或 EdgeOne 站点域名
    Product Product                 // cdn | teo
    ZoneID, ZoneName    string      // teo 专用
    ID, Name, Domain    string
    BoundCertID         string      // 空 = 未绑定
    BoundCertIsForeign  bool        // true：绑定的证书不在 SSL 池中（EdgeOne 默认证书等）
    BoundCertNotAfter   time.Time
    DomainStatus        string      // CDN Status / EdgeOne DomainStatus
    InstanceKey         string      // 已按产品格式拼好的 InstanceIdList 元素
    Granularity         string      // domain
}

type Change struct {
    Account string; Product Product; ZoneID, ResourceID, ResourceName, Domain string
    Kind ChangeKind  // ApplyCert | UpToDate | NoCertFound | NeedsManual | DeleteCert | SkipWithReason | Blocked
    Strategy string  // S2 deploy / S1 replace / S3 cdn-route / S4 teo-product
    From, To *CertRef
    Risk Risk        // Low | Medium | High
    Evidence []string
}
```

### 4.5 幂等、审计与缓存

- **云端是唯一事实来源**，不维护本地台账。
- **幂等**：计划期比对"当前绑定 == 目标证书"则跳过（`UpToDate`）；重跑安全。
  - 顺带好处：腾讯云对"部署与当前绑定相同的证书"会直接报错，而我们根本不会发出这种调用。
- **审计**：`~/.orbit/audit/audit.jsonl` 追加写（时间、账号、动作、目标、变更前后、策略、部署记录 ID、结果、错误码、耗时）；凭证脱敏。
- **缓存**：仅 `status` 使用（`~/.orbit/cache/<account>.json`，TTL 60s，`--no-cache` 可关）；`update`/`clean` **永不用缓存**，`clean` 强制 `IsCache=0`。

### 4.6 并发、限流与错误处理

| 维度 | 默认 | 依据 |
| --- | --- | --- |
| 账号 | 串行（`--parallel-accounts` 可开） | 日志清晰、便于中断 |
| 产品/Zone | 并行 4 | EdgeOne 每个 Zone 单独枚举；CDN 分页拉取 |
| 变更操作 | **按（证书 × 资源类型）串行**，整体并发 2 | 官方硬约束 |
| 令牌桶 | SSL 10/s（`upload-update` 20/s）、CDN 20/s | 官方限流表；**维度含子账号**，多账号可并行 |
| 重试 | 仅幂等读 + `LimitExceeded.RateLimitExceeded` + 5xx | 指数退避 + 抖动；参数/权限类立即失败 |
| 超时 | API 15s / 部署轮询 5m | 可配 |

**错误码 → 可行动提示**（消息键由表现层翻译）

| 错误码 | 处理与提示 |
| --- | --- |
| `FailedOperation.RoleNotFoundAuthorization` | 阻断并引导：需授权服务角色 `SSL_QCSLinkedRoleInReplaceLoadCertificate`（附直达链接） |
| `FailedOperation.CertificateDeployHasPendingRecord` | 等待该（证书 × 资源类型）任务完成；提示"控制台可能有进行中任务" |
| `FailedOperation.CertificateMismatch` / CDN `InvalidParameter.SslCertMatchError` | "证书与域名不匹配"，打印证书 SAN 与目标域名的差异 |
| CDN `ResourceUnavailable.CdnHostNoIcp` | 域名未备案（同步约 2 小时），跳过并提示 |
| CDN `InvalidParameter.AccessPortOpenedHttps` | 启用 HTTPS 需访问端口配置中 443 已开启，提示去控制台开启 |
| `InvalidParameter.RegionNotEmpty` | 内部约束兜底（v1 不应触发；打印产品名以便修 bug） |
| `LimitExceeded.RateLimitExceeded` | 自动退避重试；连续失败建议降低并发 |
| `FailedOperation.CertificateHostResourceInstanceHugeLimit` | 实例过多，建议按产品或 Zone 分批（或后续支持分片扫描） |

---

## 5. 安全模型（一页读懂）

| 层级 | 行为 | 触发方式 |
| --- | --- | --- |
| L0 只读 | `status`、`account test`、`doctor` | 永远只读 |
| L1 计划 | `update`/`clean` 打印将发生什么（diff + 风险 + 证据） | 默认（TTY 下）与 `--dry-run` |
| L2 执行 | 真正下发变更 | `--yes` 或 TUI 确认 |
| L3 销毁 | 删除证书 | `--yes` + G1–G4 全通过 + 备份成功 |
| L4 实验性 | 尚未实测确认的写路径 | 额外需要 `--enable-experimental-write` |

补充原则：

1. **前置自检**：凭证 → 服务角色 → API 权限 → 限流 → 语言包。失败即阻断（只读命令除外），并给出**可复制的 CAM 策略片段**与授权链接。
2. **凭证自管**：不读 `~/.tccli`；`~/.orbit/credentials.yaml` 强制 0600（否则启动报错）；支持环境变量注入以适配 cron；日志与审计全量脱敏。
3. **删除不可逆**：删前必须备份成功，失败即中止。
4. **TUI 中危险操作高亮**，执行摘要里"删除"单独成段。
5. **可观测**：每次运行都有审计记录与 `--format=json` 摘要。

---

## 6. 配置与凭证

### 6.1 目录布局（按 A9）

```
~/.orbit/
├── config.yaml          # 全局配置（含 language、账号、产品、范围）
├── credentials.yaml     # 凭证（0600；SecretId/SecretKey，可选用主密码加密）
├── cache/               # status 缓存（TTL 60s）
├── audit/audit.jsonl    # 审计
└── backups/             # clean 删除前备份（<account>/<certid>-<time>/）
```

可选**项目级覆盖**：`./.orbit.yaml`（`--local` 作用域），用于不同机器/环境的差异；`--config <path>` 直接指定。

### 6.2 优先级

`命令行 flag` > `环境变量 ORBIT_*` > `--config 指定文件` > `./.orbit.yaml`（本地） > `~/.orbit/config.yaml`（全局） > 内置默认

### 6.3 配置示例

```yaml
version: 1
language: zh-CN                 # 也可 en-US；可被 --lang / ORBIT_LANG 覆盖
defaults:
  platforms: [tencent]
  products: [cdn, teo]
  strategy: auto                # auto(=S2) | deploy | replace
  concurrency: { products: 4, changes: 2 }
  timeouts: { api: 15s, deploy_poll: 5m }
  cert_preference: [upload, buy]
  domain_match: { wildcard: true, allow_expired: false }
  experimental_write: false     # 未实测策略（S-REPLACE）默认关闭
  edgeone:
    replace_free_cert: true     # EdgeOne 免费证书域名是否默认替换为 SSL 托管证书（见 B2b）
  clean:
    older_than: 7d
    require_full_evidence: true
    include_free: false
    protect_patterns: []         # 默认空；可按需填 "*prod*" 之类兜底（B3 已接受风险见 DECISIONS §3.1）
    # 以下范围层为下一版预留，v1 不生效（留空 = 全部 upload 证书都在清理范围内）
    scope:
      cert_alias_prefixes: []
      cert_tags: {}
      domains: []
platforms:
  tencent:
    accounts:
      - alias: main              # Tab / --accounts 用
        credential: { source: store }        # store | env
        products: [cdn, teo]
        scope:
          domains: ["*.example.com"]         # G1 额外范围限定（域名白名单）
          cert_alias_prefixes: ["1panel-"]
          cert_tags: { orbit-managed: "true" }
      - alias: hk
        credential: { source: env, secret_id_env: ORBIT_TENCENT_HK_SECRET_ID, secret_key_env: ORBIT_TENCENT_HK_SECRET_KEY }
        products: [cdn, teo]
        scope: { domains: ["*.example.hk"] }
```

### 6.4 凭证录入与存储（按 A1）

- `orbit account add`：交互式，隐藏输入 SecretId / SecretKey（可选二次确认），生成别名，写入 `~/.orbit/credentials.yaml`（0600），并立即调用 `Preflight` 校验（凭证有效性 + 服务角色 + 权限）。
- 非交互：`orbit account add --alias main --secret-id-env ...`；或 `--from-stdin`（从管道读取，不落 shell history）。
- 环境变量注入（cron 推荐）：`source /etc/orbit/env` 后 `credential: {source: env}`，配置文件里只有变量名。
- **加密可选**（B1）：若你要求，则用 `ORBIT_MASTER_PASSPHRASE` + Argon2id → AES-GCM 加密 `credentials.yaml`；代价是 cron 也需要注入口令。

---

## 7. 技术栈选型说明

### 7.1 选型结论

**Go 1.27 + Cobra + Bubble Tea 生态 + 官方腾讯云 Go SDK + 轻量自研 i18n，产出静态单文件二进制。**

| 关注点 | 选择 | 版本（本地实测） |
| --- | --- | --- |
| 语言 | Go | **1.27.1**（`source .toolchains/env.sh`） |
| CLI | `spf13/cobra` | v1.10.2 |
| TUI | `charmbracelet/bubbletea` + `bubbles`(table/viewport/list/spinner/progress/help) + `lipgloss` | v1.3.10 / v1.0.0 / v1.1.0 |
| 云 SDK | `tencentcloud-sdk-go/tencentcloud/common` + `.../ssl/v20191205` + `.../cdn/v20180606` + `.../teo/v20220901` | common v1.3.191；ssl v1.3.147；cdn v1.3.173；teo v1.3.186 |
| 配置 | `gopkg.in/yaml.v3` | v3.0.1 |
| i18n | `golang.org/x/text/language` + 自研 `go:embed` JSON 语言包 | x/text 随 x/net 依赖链（近乎零成本），不引入重型 i18n 框架 |
| 日志 | 标准库 `log/slog` | — |
| IDN | `golang.org/x/net/idna` | 随依赖 |
| 测试 | 标准库 `testing` + 表驱动 + golden | — |

**SDK 使用要点（实测得出，容易踩）**

1. 腾讯云 Go SDK 是**按产品拆分的多模块**：父模块 `tencentcloud-sdk-go` 只是版本标记；产品包路径形如 `.../tencentcloud/ssl/v20191205`（**module 与 import 路径都带 `/v20191205`**）。
2. **各产品模块版本号不同步**（ssl v1.3.147 / cdn v1.3.173 / teo v1.3.186 / common v1.3.191）→ `go.mod` 逐一 pin，升级一起升。
3. `ssl.NewClientWithSecretId` 已 Deprecated → 用 `NewClient(credential, region, clientProfile)`。
4. **bubbles v1.0.0 没有官方 tabs 组件**（已确认其组件清单）→ 页签栏自研（约 100 行），不引额外 UI 依赖。

**依赖策略**：直接依赖约 8–10 个（cobra、bubbletea、bubbles、lipgloss、yaml.v3、x/text、x/net、SDK 的 common+ssl+cdn+teo），其余走标准库。

### 7.2 横向对比（为什么不是别的）

| 方案 | 优势 | 劣势 | 结论 |
| --- | --- | --- | --- |
| **Go + Cobra + Bubble Tea** | 静态单文件、交叉编译；官方 SDK 一等公民；Elm 架构适配"引擎/视图分离"；你本人主力栈 | TUI 表达力略逊 Rust | ✅ |
| Rust + Ratatui | 性能与类型系统强 | 腾讯云 SDK 生态弱、开发慢、复用成本高 | ❌ |
| Python + Textual | 迭代最快 | 服务器需解释器/venv，cron 脆弱，无静态二进制 | ❌ |
| Node + Ink | 本机有 Node 24 | 运行时重、云 SDK 体验差、运维 CLI 部署劣势明显 | ❌ |
| Shell + tccli 脚本 | 零新增技术栈 | 域名匹配/异步复核/误删防护全要手写，不可测不可审计 —— 正是当前痛点的成因 | ❌ |

### 7.3 构建与发布

```bash
source .toolchains/env.sh
go build -ldflags "-X github.com/Crequency/orbit/internal/version.Version=$(git describe --tags --always)" -o bin/orbit ./cmd/orbit
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o orbit-linux-amd64 ./cmd/orbit   # 部署到服务器
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o orbit-darwin-arm64 ./cmd/orbit  # 本机 TUI 使用
```

- Makefile：`build / test / lint / vet / fmt / e2e-dryrun / verify-api`（后者跑待实测清单）。
- CI：`go vet` + `golangci-lint` + `go test ./...`（全部用例不触网，靠 `cloud/fake`）。

---

## 8. 腾讯云落地映射（v1 范围：CDN + EdgeOne）

### 8.0 先纠正 v0.1 中三处基于推测的 API 名

| v0.1 的写法 | 事实 | 影响 |
| --- | --- | --- |
| `DeployCertificate` | **不存在**；应为 **`DeployCertificateInstance`** | 应用路径 |
| `DescribeCertificateDeployRecords` | **不存在**；应为 **`DescribeHostDeployRecord`** + `...Detail` | 异步任务轮询 |
| 用 `Certificates.BoundResource` 判断"是否被使用" | 该字段文档明文**"暂不可用"** | 清理证据必须走关联查询任务 |

### 8.1 四组核心接口（覆盖全部动作）

| 阶段 | 接口 | 关键点 |
| --- | --- | --- |
| 发现 | `DescribeCertificates` | 分页 `Offset`/`Limit`（最大 1000）；过滤器含 `FilterSource=upload\|buy`、`Tags`、`CertIds`、`Status`、`FilterExpiring`、`Deployable`、`Hostable` |
| 关联 | `CreateCertificateBindResourceSyncTask` → `DescribeCertificateBindResourceTaskResult` / `...TaskDetail` | 首批 ≤100 张；`IsCache` 必填（1=半小时缓存）；结果为 `ResourceType → Region → TotalCount/Error` + `Status(0/1/2)` |
| 部署 | `DeployCertificateInstance` | 一次一张证书；`ResourceType`（不传默认 **clb**！）+ `InstanceIdList`；异步（`DeployRecordId`/`DeployStatus`） |
| 删除 | `DeleteCertificates`（≤100）→ `DescribeDeleteCertificatesTaskResult` | **`IsSync` 默认不检查关联，必须传 `true`**；任务 `Status` 3/4/5 都是"别删"信号 |

辅助：`DescribeHostCdnInstanceList` / `DescribeHostTeoInstanceList` —— **SSL 侧的"证书 → 实例"统一视图**，v1 的绑定关系读取主路径。

### 8.2 v1 产品矩阵

| 产品 | 枚举 | 当前证书读取 | 应用（默认） | 复核 | Region |
| --- | --- | --- | --- | --- | --- |
| **CDN** | `DescribeDomainsConfig`（`Limit` ≤100，20/s） | `Https.CertInfo`（`ServerCert{CertId, CertName, Certificate, PrivateKey, ExpireTime, DeployTime, From}`）+ `Https.SslStatus` | **S-CDN**：`ModifyDomainConfig(Route="Https.CertInfo.CertId")` | `Https.SslStatus → deployed` + `CertId` 比对 + `DescribeHostCdnInstanceList` | ❌ |
| **EdgeOne** | `DescribeZones`（`Limit` ≤100）→ 逐 Zone `DescribeAccelerationDomains`（`Limit` ≤200） | `AccelerationDomain.Certificate{Mode, List[]CertificateInfo{CertId, Alias, Type, ExpireTime, DeployTime, SignAlgo, CommonName, Status}}` | **S-TEO**：`ModifyHostsCertificate(Mode="sslcert", ServerCertInfo=[{CertId}])` | `Certificate.List[].Status → deployed` + `CertId` 比对 + `DescribeHostTeoInstanceList` | ❌ |

> ⚠️ 复核时**不要混用状态枚举**：CDN 的 `Https.SslStatus`（`closed`/`deploying`/`deployed`/`failed`）、EdgeOne 的 `CertificateInfo.Status`（`deployed`/`processing`/`applying`/`failed`/`issued`）是两套；而 `DescribeHostCdnInstanceList` 返回的 `CdnInstanceDetail.Status` 是**域名状态**（`rejected`/`processing`/`online`/`offline`），与证书状态无关 → 引擎里必须分成独立类型建模（见 §4.4）。

### 8.3 EdgeOne 的证书语义（已明确，不再算未知风险）

EdgeOne 域名的证书配置由 `Certificate.Mode` 决定，共三种：

| `Mode` | 含义 | `List[].CertId` 来源 |
| --- | --- | --- |
| `sslcert` | 使用腾讯云 SSL 托管证书 | **腾讯云 SSL 证书 ID**（可在证书池中比对） |
| `eofreecert` / `eofreecert_manual` | EdgeOne 自带免费证书（TrustAsia / Let's Encrypt，90 天、到期前 15 天自动续期、不支持下载） | **EdgeOne 自有 ID**（形如 `teo-285inl1otbtd`、`Type=default`、`Alias="EdgeOne default"`），**不在 SSL 证书池中** |
| `disable` | 未配置证书 | — |

由此确定三条设计：

1. **"当前是否已是你上传的证书"只看 `Mode=="sslcert"` 且 `List[].CertId` 在证书池内**；`Mode=eofreecert*` 的域名如实展示为"当前使用 EdgeOne 免费证书"，**不判定为"无证书"**。
2. **是否把免费证书切换成你的证书是业务选择** → 配置项 `edgeone.replace_free_cert`（默认 `true`，即按你"以 1Panel 为准"的工作流执行；设 `false` 则只在计划里报告、不写）。**默认值见 B2b 待你确认。**
3. 读取域名证书**必须用 `DescribeAccelerationDomains`**：官方在 `DescribeDefaultCertificates` 页明确"如需查询域名配置的证书，请使用 `DescribeAccelerationDomains`"，旧接口 `DescribeHostsSetting` / `DescribeDefaultCertificates` 不用于此目的。
4. 顺带提醒**第三套**易混淆的东西：SSL 服务自己也能签发免费证书（`ApplyCertificate`，400/41678），它会出现在 SSL 证书列表里，与 EdgeOne 免费证书无关。

### 8.4 实例标识（`InstanceIdList`，官方原文）

| ResourceType | 格式 | 需 Region |
| --- | --- | --- |
| `cdn` | `Domain\|计费开关(on/off)` | ❌ |
| `teo` | `Domain` | ❌ |

> `ResourceType` **不传默认 `clb`** → Orbit 永远显式传参（v1 只会传 `cdn` 或 `teo`）。
> `teo` 只传 `Domain` 即可唯一定位：teo 分支的 schema **没有 ZoneId 位**，且域名在 EdgeOne 内**全局唯一**（重复接入报 `ResourceUnavailable.DomainAlreadyExists`）；`DescribeHostTeoInstanceList` 会回带 `ZoneId`，可用于展示与映射。

### 8.5 清理路径（权威证据版）

```
G1（FilterSource=upload ∩ 域名白名单/别名前缀/标签）
  → CreateCertificateBindResourceSyncTask(IsCache=0, ≤100/批) → 轮询至 Status=1
  → 证据 = 所有 (ResourceType, Region) TotalCount=0 且 Error 为空
  → 交叉验证：CDN 域名配置 + EdgeOne 站点域名均无引用
  → G3 取代判定 + G4 宽限/保护/免费/托管
  → 备份 → DeleteCertificates(IsSync=true) → 轮询任务结果 → 分类收尾
```

### 8.6 待实测（已从"决定架构"降级为"打磨参数"）

原来的 P0 三项（`ModifyDomainConfig` 的 Route 语义、EdgeOne `Mode` 取值与 ID 来源、teo 的 Domain 定位方式）**已由官方文档解决**；`Https.SslStatus` 取值也已确认。剩余实测项都不影响架构：

| 项 | 影响 | 验证方式 |
| --- | --- | --- |
| SSL 部署到 CDN/EdgeOne 所需**服务角色的确切名称** | 首次使用的引导文案 | 控制台"服务角色授权"页对照（删除侧已知 `SSL_QCSLinkedRoleInReplaceLoadCertificate`） |
| `SslStatus` / `CertificateInfo.Status` 从 deploying→deployed 的**耗时上限** | 轮询超时与退避参数 | 单次实测记录耗时分布 |
| TEO `Region` 传空字符串是否被接受 | 客户端构造细节 | 实测（低风险：传 `ap-guangzhou` 亦可） |
| EdgeOne 免费证书是否也出现在 SSL `DescribeCertificates` | 展示口径 | 账号对照 |
| `ModifyDomainConfig` 与 `UpdateDomainConfig` 的底层一致性 | 备选路径可用性 | 实测 |

完整清单（含后置批次项）见《[Tencent API 事实清单](TENCENT_API_NOTES.md)》§5。

### 8.7 后续批次预留（不在 v1）

CLB（`DescribeListeners` + SNI `Rules` + `ModifyDomainAttributes`）、WAF、API 网关（`DomainSet.CertificateId`）、COS 自定义域名、直播（`ModifyLiveDomainCertBindings` + `CloudCertId`）、点播、TKE（写入 TLS Secret）、DDoS/轻量等。这些产品的 Action、格式与 Region 要求已在事实清单中记录，接入时只需新增 Provider 产品实现。

---

## 9. 里程碑与工期（按 v1 范围重估）

| 里程碑 | 内容 | 交付物 | 估算 | 状态 |
| --- | --- | --- | --- | --- |
| M0 骨架 | go.mod/Makefile、CLI 骨架、`~/.orbit` 配置、凭证录入与存储、TTY 探测、退出码、`cloud/fake`、i18n 骨架 | `orbit config`、`orbit account add`、`orbit version` 可用 | 2 人日 | ✅ **已完成**（另含匹配引擎与清理门禁，见下） |
| M1 只读采集 + 参数实测 | 腾讯云 client（限流/分页/Zone 扇出/重试）、证书池、CDN 域名、EdgeOne 站点/域名、绑定关系；完成 §8.6 的参数级实测 | `orbit status --format=json` 有真实数据；服务角色名称与轮询参数确定 | 3 人日 | ✅ **代码完成**（回放测试覆盖）；参数实测待你用真实账号按 [VERIFY_M1](VERIFY_M1.md) 回报 |
| M2 匹配引擎 | `match` 纯函数 + 选优 + 计划 + 策略/风险 + `ForeignCert` 判定 | `orbit update --dry-run` 输出计划 + 单测 | 2 人日 | ◐ 已随 M0 完成（`internal/core`，含表驱动单测） |
| M3 应用执行 | S-CDN（Route 局部更新）+ S-TEO（`Mode=sslcert`）、S-UNI 备用、异步轮询、回读复核、错误码处置、审计、幂等 | `orbit update --ssl --yes` 端到端可用 | 3.5 人日 | ✅ **完成** |
| M4 清理 | 关联证据、G1–G4、服务角色检查、备份、二次确认、删除任务状态分类 | `orbit clean --ssl` 安全可用 | 2.5 人日 | ✅ **完成**（服务角色确切名称待真机确认，V1） |
| M5 TUI + i18n + 多账号 | 多 Tab、账号多选、计划预览/确认、进度与结果页、zh-CN/en-US 语言包 | `orbit` / `orbit status` TUI 可用 | 4.5 人日 | ✅ **完成**（总览多 Tab、账号多选、计划确认、执行进度与动态等待、`u`/`c` 直达、`orbit dash` 总控台） |
| M6 交付 | 打包、cron/systemd 示例、README、`doctor`、CAM 模板、真机验收 | 可上线版本 + 文档 | 2 人日 | ✅ 交付完成（`make release` 三平台静态二进制、[OPERATIONS.md](OPERATIONS.md) 运维手册、`docs/cam/` 三档策略、`scripts/` 示例）；**真机验收待用户执行** |
| 合计 |  |  | **≈ 19.5 人日** |  |

> **M0 实际交付**（略超出原定范围）：`internal/core` 的匹配选优、计划与清理四道闸门已实现并通过单测，因此 M2 与 M4 的核心逻辑提前完成；M0–M2 的"只读可验"目标已达成，可直接进入 M1 接入真实 SDK。
> 关键路径 M1 → M3。**建议节奏**：M1 完成后先用只读命令与真实账号核对数据，再开 M3 写操作。

---

## 10. 测试与验收标准

**单测（不触网，走 `cloud/fake`）**
- `match` 表驱动：精确/泛域名/多级子域/IDN/大小写/尾部点/过期证书/同域多证书选优/平局稳定/SAN 与主域名并集/`ForeignCert` 不参与选优。
- `plan` 幂等：连续两次生成计划，第二次全 `UpToDate`。
- `safety`：G1–G4 阻断与放行边界；**证据不完整必须拒绝删除**。
- `engine`：**（证书 × 资源类型）串行化**、`DeployStatus=0` 处理、回读失败重试、删除任务 `Status=3/4/5` 分类、实验性写闸门。
- `config/credential`：优先级合并、0600 校验、脱敏（日志里不得出现 SecretKey）。
- `i18n`：zh-CN/en-US key 集合一致性测试（防止漏翻导致运行期显示 key）。
- golden：`status --format=table`、`update --dry-run` 输出快照（两种语言各一份）。

**联调（需你授权的最小权限子账号）**
- `orbit doctor` 四项自检全绿；`orbit status` 与控制台逐项核对（证书数、CDN 域名数、EdgeOne 站点与域名数、当前证书）。

**端到端验收清单**
1. 测试账号制造场景：同域名一张旧证书（已绑 CDN）+ 一张新证书（未绑定）→ `update --dry-run` 恰好 1 条 `ApplyCert` → `--yes` 执行后 CDN 域名证书变为新证书 → **再跑一次全部 `UpToDate`**。
2. EdgeOne 场景：一个使用 EdgeOne 免费证书（`Mode=eofreecert`）的域名 → 计划里必须如实标注"当前为 EdgeOne 免费证书"；`edgeone.replace_free_cert=false` 时**不产生任何写操作**，为 `true` 时产生一条切到 `Mode=sslcert` 的变更。
3. 清理场景：制造"在用旧证书"与"完全无引用证书"各一张 → `clean --dry-run` 只把后者列为候选；前者必须出现在 `SKIPPED` 明细并给出原因。
4. 故障注入：错误凭证 / 权限不足 / 服务角色未授权 / 限流 / 关联查询超时 → 退出码与提示符合契约，且**任何情况下都不得删除证据不完整的证书**。

---

## 11. 风险与对策

| # | 风险 | 影响 | 对策 |
| --- | --- | --- | --- |
| R1 | CDN 配置项误改 | 误伤用户其它 CDN 配置 | 默认走 `ModifyDomainConfig(Route="Https.CertInfo.CertId")` **单点更新**（官方示例即此用法，非叶子节点保持不变）；`UpdateDomainConfig` 仅作备选，且必须"读全量 → 只改证书字段 → 回写" + 单测锁死 |
| R2 | EdgeOne 免费证书被误判或被无意替换 | 把免费证书域名误判成"无证书"，或不加确认就替换掉 90 天自动续期的免费证书 | 以 `Certificate.Mode` 为准判定（§8.3）；免费证书域名在计划中单独列出；`edgeone.replace_free_cert` 开关控制是否替换（默认值见 B2b） |
| R3 | 异步部署静默失败 | 以为成功实际没生效 | 强制回读复核（CDN 读 `Https`、EdgeOne 读 `Certificate`、SSL 侧 `DescribeHost*InstanceList` 交叉验证） |
| R4 | 证据不全导致误删在用证书 | 线上 HTTPS 中断 | 云端关联查询 + G1 范围 + G3 取代 + G4 宽限 + 备份 + 默认不写；五重防护 |
| R5 | `DeleteCertificates.IsSync` 默认不检查关联 | 误删在用证书 | 代码层硬编码 `IsSync=true` + 单测锁死 |
| R6 | 服务角色未授权 | 部署/删除失败 | `doctor` 与执行类命令前置检查，输出引导而非报错堆栈 |
| R7 | （证书 × 资源类型）并发约束被违反 | 部署任务创建失败 | 引擎按该维度串行 + 识别 `CertificateDeployHasPendingRecord` 并等待 |
| R8 | 免费证书 1 小时内不可删、托管证书不宜动 | 误判为"删除失败" | `include_free=false` 默认、排除托管中证书；文档写明规则 |
| R9 | 凭证明文落盘 | 泄露 | 0600 强校验 + 全链路脱敏 + 支持 env 注入；B1 若选加密则 Argon2id+AES-GCM |
| R10 | EdgeOne 需要逐 Zone 扇出，站点多时慢 | 巡检耗时 | Zone 级并发 4 + 分页正确性单测；`status` 走缓存 |
| R11 | 限流（SSL 10/s，维度含子账号） | 巡检慢/失败 | 令牌桶 + 并发上限 + 退避；`clean` 才用 `IsCache=0` |
| R12 | 后续接入 CLB/阿里云时模型不兼容 | 返工 | Provider 能力位建模（含 `DeployNeedsRegion`、`ExtraScopes`），核心引擎零改动 |

---

## 12. 已确认的实现约束（B1–B7）

> 全部答复已落盘到 [DECISIONS.md](../DECISIONS.md) §3。以下为实现时必须遵守的硬约束。

| # | 约束 |
| --- | --- |
| **B1** | 凭证明文存储 + 强制 0600，路径 `~/.orbit/credentials.yaml`；配置文件 `~/.orbit/config.yaml`。同时支持环境变量注入（cron）与 `orbit account add --from-stdin`（非交互）。启动时若凭证文件权限不是 0600 → 报错并给出 `chmod 600` 提示。 |
| **B2a** | 选优顺序：**`CertEndTime` 更晚者优先；完全相同则 `From=upload` 优先**；再平局按 `CertificateId` 字典序（稳定可复现）。这条对所有域名（含 CDN 与 EdgeOne）统一适用。 |
| **B2b** | EdgeOne `Mode=eofreecert*` 的域名**默认参与替换**，目标同样是 B2a 选出的最优证书（即"所有证书中有效期最长的那张"）；`edgeone.replace_free_cert=false` 可改为只报告。 |
| **B3** | v1 `clean` 范围 = **全部 `FilterSource=upload` 证书**，不叠加别名/标签/域名白名单；范围层配置字段保留但默认空、不生效。已接受风险与四层缓解见 DECISIONS §3.1。 |
| **B4** | 配置分层：`~/.orbit/config.yaml`（全局基线）+ `./.orbit.yaml`（项目级覆盖，`--local`）+ `--config <path>`。合并规则：**按字段深合并，本地覆盖全局**，`--config` 指定的文件取代全局基线。 |
| **B5** | 语言：`zh-CN` + `en-US`；优先级 `--lang` > `ORBIT_LANG` > 配置 `language` > `$LANG` > `zh-CN`；**`--json` 输出与错误码不做翻译**。 |
| **B6** | 非 TTY 且无 `--yes`：出计划、**不写云端**、退出码 0（`--fail-on-changes` 时为 3）。`--yes` 才写；`--dry-run` 永不写。 |
| **B7** | EdgeOne 只处理**站点域名证书**；`UpstreamCertInfo`（回源）与 `ClientCertInfo`（mTLS）**不读不改**；`AliasDomain` 不处理。实现上：构造 `ModifyHostsCertificate` 请求时**绝不设置**这两个字段。 |

---

## 附录 A：CAM 最小权限策略模板（v1 范围）

```json
{
  "version": "2.0",
  "statement": [
    {
      "sid": "orbit-read-ssl",
      "effect": "allow",
      "action": [
        "ssl:DescribeCertificates",
        "ssl:DescribeCertificate",
        "ssl:DescribeCertificateDetail",
        "ssl:DescribeHostCdnInstanceList",
        "ssl:DescribeHostTeoInstanceList",
        "ssl:DescribeDeployedResources"
      ],
      "resource": ["*"]
    },
    {
      "sid": "orbit-bind-ssl",
      "effect": "allow",
      "action": [
        "ssl:CreateCertificateBindResourceSyncTask",
        "ssl:DescribeCertificateBindResourceTaskResult",
        "ssl:DescribeCertificateBindResourceTaskDetail"
      ],
      "resource": ["*"]
    },
    {
      "sid": "orbit-write-ssl",
      "effect": "allow",
      "action": [
        "ssl:DeployCertificateInstance",
        "ssl:DescribeHostDeployRecord",
        "ssl:DescribeHostDeployRecordDetail",
        "ssl:DeployCertificateRecordRetry"
      ],
      "resource": ["*"]
    },
    {
      "sid": "orbit-delete-ssl",
      "effect": "allow",
      "action": [
        "ssl:DeleteCertificate",
        "ssl:DeleteCertificates",
        "ssl:DescribeDeleteCertificatesTaskResult"
      ],
      "resource": ["*"]
    },
    {
      "sid": "orbit-read-products",
      "effect": "allow",
      "action": [
        "cdn:DescribeDomains",
        "cdn:DescribeDomainsConfig",
        "cdn:DescribeCertDomains",
        "teo:DescribeZones",
        "teo:DescribeAccelerationDomains",
        "teo:DescribeDefaultCertificates"
      ],
      "resource": ["*"]
    },
    {
      "sid": "orbit-write-products",
      "effect": "allow",
      "action": [
        "cdn:UpdateDomainConfig",
        "cdn:ModifyDomainConfig",
        "teo:ModifyHostsCertificate"
      ],
      "resource": ["*"]
    }
  ]
}
```

> 建议拆两套子账号：**巡检号**（只含 `orbit-read-*` 与 `orbit-bind-ssl`）用于 `status`；**执行号**（再加 write/delete）只部署在执行机上。
> 另需在控制台一次性授权服务角色 `SSL_QCSLinkedRoleInReplaceLoadCertificate`（部署与"带检查删除"的前置条件）。

## 附录 B：cron / systemd 用法示例

```bash
# 每周一 03:30 演练（有变更则退出码 3，可触发告警）
30 3 * * 1 /usr/local/bin/orbit update --ssl --accounts=main --dry-run --fail-on-changes --format=json >> /var/log/orbit.log 2>&1
# 04:00 真正执行
0  4 * * 1 /usr/local/bin/orbit update --ssl --accounts=main --yes --format=json >> /var/log/orbit.log 2>&1
# 04:30 清理超期无用证书（自动备份到 ~/.orbit/backups）
30 4 * * 1 /usr/local/bin/orbit clean --ssl --accounts=main --yes --older-than=7d >> /var/log/orbit.log 2>&1
```

```ini
# /etc/systemd/system/orbit-weekly.service
[Unit]
Description=Orbit SSL 巡检与应用
[Service]
Type=oneshot
EnvironmentFile=/etc/orbit/env      # 内含 ORBIT_TENCENT_MAIN_SECRET_ID / _SECRET_KEY
ExecStart=/usr/local/bin/orbit update --ssl --accounts=main --yes --format=json
StandardOutput=append:/var/log/orbit.log
StandardError=append:/var/log/orbit.log
```

---

*本文件为待审阅草案（v0.3）。B1–B7 确认后写入 [DECISIONS.md](../DECISIONS.md)；§8.6 的实测结论回填《[Tencent API 事实清单](TENCENT_API_NOTES.md)》。*
