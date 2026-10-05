# Orbit 决策记录（DECISIONS）

> 规则：**已确认的决策才写入本文件**；未确认的留在"待确认"区，确认后立即落盘并注明日期。
> 设计方案见《[项目实现方案](docs/IMPLEMENTATION_PLAN.md)》，API 事实见《[Tencent API 事实清单](docs/TENCENT_API_NOTES.md)》。

---

## 1. 技术事实驱动的决策

| # | 决策 | 依据 |
| --- | --- | --- |
| T1 | **CDN 换证书默认走 `ModifyDomainConfig(Route="Https.CertInfo.CertId")`** 单点更新；`UpdateDomainConfig`（整对象回写）仅作备选 | 官方示例 4 即此用法；"非叶子节点配置保持不变" |
| T2 | **EdgeOne 换证书默认走 `ModifyHostsCertificate(Mode="sslcert", ServerCertInfo=[{CertId}])`** | `Mode` 枚举已确认；`ServerCertInfo` 仅 `sslcert` 生效 |
| T3 | `DeployCertificateInstance` 作为备用/批量通道（`cdn`: `Domain\|on/off`；`teo`: `Domain`，均不需 Region）；`UpdateCertificateInstance`(S-REPLACE) 未实测前默认关闭 | 逐字段已核实；未实测策略需 `--enable-experimental-write` |
| T4 | `clean` 的"未被使用"证据以 `CreateCertificateBindResourceSyncTask` + `...TaskResult` 为准，并用 CDN/EdgeOne 枚举交叉验证 | 官方权威关联查询；`BoundResource` 文档明文"暂不可用" |
| T5 | `DeleteCertificates` 的 `IsSync` 恒传 `true`（代码层锁死 + 单测） | 官方默认"不检查云资源关联"，与安全预期相反 |
| T6 | **v1 无地域扇出**：证书全局，CDN 与 EdgeOne 部署都不需要 Region；能力位保留给后续产品 | 官方 Region 表 + SDK 核对 |
| T7 | 执行层按（证书 ID × 资源类型）串行化 | 官方硬约束 |
| T8 | 三套状态枚举**分开建模**：CDN `Https.SslStatus`(closed/deploying/deployed/failed)、EdgeOne `CertificateInfo.Status`(deployed/processing/applying/failed/issued)、`CdnInstanceDetail.Status`(域名状态) | 官方数据结构；混用会导致复核逻辑错误 |
| T9 | EdgeOne 证书语义以 `Certificate.Mode` 判定；`Mode != sslcert` 记为 `ForeignCert` | `Mode=eofreecert*` 时 `CertId` 形如 `teo-xxx`，不在 SSL 证书池中 |
| T10 | 到期时间以 SSL 证书池为权威；CDN/EdgeOne 侧的 `ExpireTime` 仅作交叉校验 | 选优口径统一 + 漂移检测 |
| T11 | 读"证书 ↔ 资源"绑定统一走 `DescribeHostCdnInstanceList` / `DescribeHostTeoInstanceList`；产品侧接口只枚举域名 | 已核实的返回结构 |
| T12 | Go 1.27 + Cobra + Bubble Tea + 官方 `tencentcloud-sdk-go`；i18n 用 `go:embed` 语言包 + `golang.org/x/text/language`，引擎只抛消息键 | 静态单文件；翻译与逻辑解耦 |

## 2. 已确认的用户决策（A1–A9）

| # | 决策 | 确认日期 |
| --- | --- | --- |
| A1 | **按多账号编码**（现有 2 个账号）；**禁止读取 `~/.tccli/*.credential`**，凭证手动录入、Orbit 自管存储 | 2026-10 |
| A2 | **匹配与应用的候选池 = 全部证书**；**删除语义只作用于 `FilterSource=upload`** | 2026-10 |
| A3 | **第一批只支持 CDN + EdgeOne（TEO）** | 2026-10 |
| A4 | 删除口径 =「未被任何云资源引用 **且** 同域名已有更优证书 **且** 超过 N 天（默认 7）」；删前必须备份；免费证书默认不清理 | 2026-10 |
| A5 | TUI 与 CLI 双模式；`--yes` 直接执行；`--dry-run` 演练；非 TTY 且无 `--yes` 时不得写云端 | 2026-10 |
| A6 | 首批不含 CLB → 地域问题不适用 | 2026-10 |
| A7 | 为 `orbit push` 预留接口，第一批不实现 | 2026-10 |
| A8 | module `github.com/Crequency/orbit`；仓库在 `tools/orbit/` | 2026-10 |
| A9 | TUI 支持多语言；`orbit config --global language="en-US"`；全局配置目录 `~/.orbit` | 2026-10 |

## 3. 已确认的用户决策（B1–B7，2026-10 全部答复）

| # | 决策 |
| --- | --- |
| **B1** | **凭证存明文 + 0600，落在 `~/.orbit/` 下**（`credentials.yaml`）。同时保留环境变量注入用于 cron。`orbit account add` 交互隐藏输入；另提供 `--from-stdin`（非交互/CI 用）。 |
| **B2a** | 选优规则：**到期更晚的优先；到期相同时优先 `upload` 证书**。 |
| **B2b** | EdgeOne 免费证书域名（`Mode=eofreecert*`）**默认替换**，替换目标是**所有候选中有效期最长的那张证书**（即按 B2a 规则选优，与其它域名同一套口径）。 |
| **B3** | **v1 的 `clean` 范围 = `FilterSource=upload` 全量**，不再叠加别名前缀/标签/域名白名单。额外范围层**下一版再做**；配置结构预留 `scope.*` 与 `protect_patterns`（默认空）。 |
| **B4** | **支持项目级覆盖** `./.orbit.yaml`（`config --local`），全局 `~/.orbit/config.yaml` 为基线。 |
| **B5** | 首批支持 **zh-CN + en-US**，**默认跟随 `$LANG`**（识别不到用 zh-CN）；`--json` 输出语言无关。 |
| **B6** | 非 TTY 且无 `--yes`：**只出计划、不写云端**，退出码 0（`--fail-on-changes` 时为 3）——按推荐方案。 |
| **B7** | EdgeOne **只处理站点域名证书**；回源证书（`UpstreamCertInfo`）、双向认证（mTLS）、别名域名（`AliasDomain`）**不动**。 |

### 3.1 因 B3 而明确接受的风险（必须记住）

v1 的删除范围是"**全部 `FilterSource=upload` 的证书**"，没有别名/标签/域名白名单这层兜底。因此：

- 你在控制台**手工上传**的、当前无任何资源引用的证书（客户的、测试用的、临时排障用的）**会被列入清理候选**。
- 现有缓解手段（不是消除）：① 删除前**强制备份**（A4）；② 默认不写云端、需 `--yes` 或 TUI 二次确认（A5/B6）；③ G2 证据完整性校验 + G3 必须有同域名更优证书 + G4 七天宽限期；④ `protect_patterns` 配置项已实现，可随时用它兜底（默认空）。
- 下一版加入 `scope.domains` / `scope.cert_alias_prefixes` / `scope.cert_tags` 后，此风险即被范围层收口。

## 4. 待实测（参数级，不阻塞架构）

| # | 待实测项 | 影响 |
| --- | --- | --- |
| V1 | SSL 部署到 CDN/EdgeOne 所需**服务角色的确切名称**（删除侧已知 `SSL_QCSLinkedRoleInReplaceLoadCertificate`） | 首次使用引导与 `doctor` 检查项 |
| V2 | `SslStatus` / `CertificateInfo.Status` 从 deploying→deployed 的**耗时上限** | 轮询超时与退避参数 |
| V3 | TEO `Region` 传空字符串是否被 SDK/网关接受 | 客户端构造细节（低风险，传 `ap-guangzhou` 亦可） |
| V4 | EdgeOne 免费证书是否也出现在 SSL `DescribeCertificates` | 展示口径 |
| V5 | `ModifyDomainConfig` 与 `UpdateDomainConfig` 的底层一致性 | 备选路径可用性 |
| V6 | 删除证书的不可逆性与生效时间、付费证书配额上限、部署任务并发上限 | 保守设计与文档措辞 |

## 5. 变更日志

| 日期 | 变更 |
| --- | --- |
| 2026-10 | 建立本文件；写入 T1–T9 技术决策 |
| 2026-10 | 落盘 A1–A9；范围收窄为 CDN + EdgeOne，改为"v1 无地域扇出"；新增 B1–B7 |
| 2026-10 | 文档调研回填：CDN/TEO 写路径确定（T1/T2）、状态枚举分开建模（T8）、EdgeOne 语义明确（T9）；待实测降为 V1–V6 |
| 2026-10 | **B1–B7 全部确认**；B3 决定 v1 不做额外范围层 → 记录 §3.1 已接受风险；B2b 明确为"按 B2a 选优后默认替换"；**开工 M0** |
| 2026-10 | **M0 完成**：CLI/配置分层/凭证(0600)/i18n/退出码/fake provider 落地；匹配选优、计划、清理四道闸门提前完成并通过单测。过程中修掉两个真实缺陷：① `HostingStatus` 零值与"托管中"撞车（改为指针，缺失≠未托管）；② 配置合并方向写反（本地未覆盖全局）。另修 `account add` 只写凭证不登记配置导致账号不可见 |
| 2026-10 | **M1 完成**：接入官方 SDK（ssl/cdn/teo），实现证书池、CDN 域名与 EdgeOne 站点/域名枚举、证书↔资源关联查询、三条写入路径（CDN `ModifyDomainConfig(Route=Https.CertInfo.CertId)`、EdgeOne `ModifyHostsCertificate(Mode=sslcert)`、SSL `DeployCertificateInstance`）与**回读复核**；`IsSync` 在 provider 边界强制为 true（即使调用方传 false）。限流按官方 QPS 分产品令牌桶，错误码映射为可行动提示。新增 20 项回放测试（不打真实 API）。真机验证步骤见 docs/VERIFY_M1.md |
| 2026-10 | **v1.1 诊断与部分扫描**：真实账号出现 `UnauthorizedOperation`，暴露两个问题并修复——① 云端错误没有标明失败的 API 与账号（现已在错误里带上 `action`/`account`，`account test` 按 SSL/CDN/EdgeOne 逐产品探测）；② 单个产品不可用会让整个 `status` 失败（现改为 `Scan` 返回 per-product warning，`status`/`update` 降级继续，**`clean` 因证据不完整而拒绝执行**，除非 `--permit-partial-scan`）。另修 `orbit account test` 不识别 `--accounts` 的缺陷 |
| 2026-10 | **v1.2 可见性与等待优化**：`status` 首次运行数分钟且全程无输出 → ① 新增分级扫描进度（stderr，默认终端下开启，`--progress`/`--no-progress`）；② `status` 改用云端 30 分钟关联缓存（`IsCache=1`），`update`/`clean` 仍强制实时；③ 关联查询改为"先建完所有任务、再统一轮询"，总等待从 批次数×超时 降为最慢任务；④ 关联查询超时由静默改为显式 `scan warning`，`clean` 因此拒绝执行；⑤ 首次查询耗时在开始时就提示 |
| 2026-10 | **v1.3 告警精简**：`UnauthorizedOperation`（及 `UnauthorizedOperation.*`）属于"该产品在本账号不可用"，扫描时不再打印提示文案与云端原始错误，只输出一行 `扫描告警：<产品> — <错误码>`；完整 API 名与 RequestId 保留在 `--json` 的 `warnings` 里。需要行动的其他告警（未备案、限流、角色缺失等）仍保留提示与原始错误 |
| 2026-10 | **v1.4 凭证归属自检**：teo 授权后仍报 `UnauthorizedOperation`，为此新增 `sts:GetCallerIdentity` 探针（`account test` 第一行显示 AccountId/Type/UserId，用于确认权限是否授给了同一身份）、`account test --products=<p>` 单产品复查、以及 `--no-cache`（`status` 默认复用云端 30 分钟关联缓存；该缓存只影响关联查询，不影响产品枚举） |
| 2026-10 | **v1.5 站点级容错**：真实账号出现"`account test` teo 通过、`status` 却报 teo UnauthorizedOperation"——定位为 `teo:DescribeZones` 成功而 `teo:DescribeAccelerationDomains` 在 **EdgeOne Pages 类型站点**（`default-pages-zone`）上被拒。修复：EdgeOne 改为**逐站点容错**（单站点失败不再拖垮整个产品，失败站点记入 `ScanWarning.Scope`），并对 `pages`/`ai`/`noDomainAccess` 类型给出"该站点不由证书部署接口管理"的专门说明（`ZoneNotApplicable`）；紧凑告警补充站点名并对重复项折叠为 `×N` |
| 2026-10 | **v1.6 告警分级**：把 `ScanWarning` 分为阻断性与信息性（字段 `Informational`，**零值即严格**）。`ZoneNotApplicable`（EdgeOne Pages / AI / 无域名接入站点）为信息性，不再阻断 `clean`，也不再打印云端原始错误；真正的 `UnauthorizedOperation`（full/partial 站点）仍阻断。`--json` 的 warnings 新增 `scope` 与 `blocking` 字段 |
| 2026-10 | **v1.7 计划提速与展示补全**：① `update` 不再执行"证书 ↔ 云资源"关联查询（`ScanScope.SkipEvidence`），因为出计划只需要证书池与资源清单——真实账号上从 74 秒降到约 2 秒；`status`（in_use 计数）与 `clean`（G2 证据）仍必须查。② 计划表的「当前证书」列现在会用证书池反查来源（`ID/upload 到期日`），池外的证书（EdgeOne 免费证书等）保持原样，便于人工核对 |
