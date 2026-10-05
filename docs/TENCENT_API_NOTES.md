# 腾讯云 API 事实清单（Orbit 用）

> **这份文档只放事实，不放设计。** 设计见《[项目实现方案](IMPLEMENTATION_PLAN.md)》。
>
> 核对方式：官方文档页 + **官方 Go SDK 源码本地实测**（`go get` 到 `ssl@v1.3.147`、`clb@v1.3.187`、`cdn@v1.3.173`、`waf@v1.3.186`、`apigateway@v1.3.142`、`live@v1.3.191`、`vod@v1.3.186`、`teo@v1.3.186`、`tke@v1.3.180` 后逐字段提取 struct 与 action 名单）。
> 核对时间：2026-10（版本号为当次抓取结果）。
> 标记 **「未验证」** 的条目在实现前必须用测试账号实测，**不得当作事实使用**。

---

## 1. 全局约定

| 事实 | 内容 |
| --- | --- |
| 接入点 | `ssl.tencentcloudapi.com`，Version `2019-12-05`，签名为 TC3-HMAC-SHA256（service=`ssl`） |
| Region（证书级接口） | **不需要传**。官方原文："本接口不需要传递此参数"，公共参数页补充"即使传递该参数也不会生效" → **证书是全局资源，不按地域分区** |
| Region（部署接口） | `DeployCertificateInstance` 在 `clb` / `waf` / `apigateway` / `cos` / `lighthouse` / `tke` / `tse` / `tcb` / `mqtt` 上**必传**（否则 `InvalidParameter.RegionNotEmpty`）；`cdn` / `live` / `vod` / `teo` / `ddos` 不需要 |
| 限流 | SSL 接口默认 **10 次/秒**；仅 6 个接口为 **20 次/秒**（`UploadUpdateCertificateInstance`、`DescribeHostUploadUpdateRecord`、`DescribeHostUploadUpdateRecordDetail`、`UploadUpdateCertificateRecordRetry`、`UploadUpdateCertificateRecordRollback`、`CheckCertificateChain`）。**限流维度 = API + 接入地域 + 子账号**（官方原文），超限 `LimitExceeded.RateLimitExceeded` |
| 其他产品限流 | CLB `DescribeListeners`/`CreateListener` = 20/s；CDN `DescribeDomainsConfig`/`UpdateDomainConfig` = 20/s；apigateway `DescribeServiceSubDomains` = 20/s；live `ModifyLiveDomainCertBindings` = 20/s |
| 免费证书配额 | 50 张/账号；吊销或正常到期后 **24 小时内**释放额度；免费证书不支持泛域名/IP/多域名 |
| 服务角色 | 部署证书到云资源、以及"带关联检查的删除"都需要服务角色 **`SSL_QCSLinkedRoleInReplaceLoadCertificate`** 已授权，否则 `FailedOperation.RoleNotFoundAuthorization` |

---

## 2. SSL 证书服务（product 400）

### 2.1 证书池：`DescribeCertificates`（[41671](https://cloud.tencent.com/document/api/400/41671)）

- 分页：`Offset`（默认 0）、`Limit`（默认 10，**最大 1000**，超 1000 按 1000 处理）；返回 `TotalCount` + `Certificates[]`。
- **没有通用 `Filters.N` 数组**，过滤器全是命名顶层参数：
  `SearchKey`（模糊匹配 证书ID/备注名/域名）、`CertIds.N`、`FilterSource`（**`upload`=上传证书 / `buy`=腾讯云证书 / 不传=全部**）、`Upload`、`Renew`、`CertificateStatus.N`、`Deployable`、`Hostable`、`ProjectId`、`ExpirationSort`、`CertificateType`（`CA`=客户端 / `SVR`=服务器）、`IsSM`、`IsPendingIssue`、`FilterExpiring`（1=即将过期）、`Tags.N`、`ServiceId`。
- 与本任务相关的输出字段（结构 `Certificates`）：

| 字段 | 说明 |
| --- | --- |
| `CertificateId` / `Domain` / `SubjectAltName[]` / `CertSANs[]` | 证书 ID、主域名、SAN（**列表页的 `SubjectAltName` 含主域名**；详情页的 `SubjectAltName` **不含**主域名，需与 `Domain` 取并集） |
| `CertBeginTime` / `CertEndTime` / `InsertTime` / `ValidityPeriod` | 生效、过期、插入时间（GMT+8）与有效期月数 |
| `Status`（Integer）/ `StatusName` / `StatusMsg` | 状态：`0`审核中 `1`已通过 `2`审核失败 `3`已过期 `4`已添加DNS记录 `5`企业证书待提交 `6`订单取消中 `7`已取消 `8`已提交资料待上传确认函 `9`吊销中 `10`已吊销 `11`重颁发中 `12`待上传吊销确认函 `13`免费证书待提交资料 `14`已退款 `15`证书迁移中。`StatusMsg` 形如 `PRE-REVIEWING`/`PENDING-DCV`/`WAIT-ISSUE` |
| `From` | 证书来源：`upload`（用户上传）/ `trustasia` / `wosign` / `sheca` |
| `PackageType` / `PackageTypeName` | **`null` = 用户上传证书；`83` = TrustAsia C1 DV Free（免费）；其余为付费套餐编号** |
| `Alias` / `Tags[]` / `ProjectId` | 备注名、标签、项目 —— 可用于界定"Orbit 管辖范围" |
| `IsWildcard` / `IsDv` / `IsVip` / `IsSM` / `EncryptAlgorithm` | 泛域名、DV、付费、国密、加密算法 |
| `HostingStatus` | 托管状态：`0`托管中 / `5`资源替换中 / `10`托管完成 / `-1`未托管 |
| `Deployable` | 是否可部署 |
| `AutoRenewFlag` / `RenewAble` / `HostingRenewCertId` | 自动续期与续期链信息 |
| `AllowDownload` / `SupportDownloadType` | 是否允许下载（决定删除前备份的可行路径） |
| `BoundResource` | ⚠️ **文档明文标注"暂不可用"** → **绝对不能**用它判断"证书是否被资源使用" |

- 详情接口 `DescribeCertificateDetail`（[41673](https://cloud.tencent.com/document/api/400/41673)）额外提供：`CertificatePublicKey`、`CertificatePrivateKey`、`RootCert`、`CertFingerprint`、`CertChainInfo[].Issuer`、`CertType`(DV/OV/EV)、`DomainType`、`DvAuthDetail`、`CertificateExtra`（含 `OriginCertificateId` 续费原证书、`ReplacedBy`/`ReplacedFor` 重颁发、`SMCert`、`CompanyType`）。

### 2.2 「这张证书被哪些资源使用」——两代接口

| | 旧接口 | 新接口（推荐） |
| --- | --- | --- |
| Action | `DescribeDeployedResources`（[62674](https://cloud.tencent.com/document/api/400/62674)） | `CreateCertificateBindResourceSyncTask`（[97759](https://cloud.tencent.com/document/api/400/97759)）→ `DescribeCertificateBindResourceTaskResult`（[97757](https://cloud.tencent.com/document/api/400/97757)）/ `...TaskDetail`（[97758](https://cloud.tencent.com/document/api/400/97758)） |
| 入参 | `CertificateIds.N`（**≤50**）、`ResourceType`（**必填、单值**：`clb,cdn,live,vod,waf,antiddos,teo`） | 创建：`CertificateIds.N`（**≤100**）、`IsCache`（**必填**，1=用缓存(默认，半小时内结果复用) / 0=实时）。查询：`TaskIds.N`（≤100）；详情：`TaskId`（单个）+ `Offset`/`Limit`（String，Limit 最大 100）+ `ResourceTypes.N` + `Regions.N`（仅 clb/tke/waf/apigateway/tcb/cos/tse 支持按地域） |
| 出参 | `DeployedResources[]{CertificateId, ResourceIds[], Resources[], Type, Count}` | 结果（仅总数）：`SyncTaskBindResourceResult[]{TaskId, Status(0查询中/1成功/2异常), Error, CacheTime, BindResourceResult[]{ResourceType, BindResourceRegionResult[]{Region, TotalCount, Error}}}`；详情按产品分列（`CLB/CDN/WAF/...` 各含实例明细） |
| 状态 | 同步 | 异步两步 |
| 弃用提示 | 文档原文："最新查询接口请使用 `CreateCertificateBindResourceSyncTask`" | — |

> 支持关联查询的产品（结果接口正文列 11 种）：`clb`、`cdn`、`waf`、`live`、`vod`、`ddos`、`tke`、`apigateway`、`tcb`、`teo`、`cos`；详情接口正文另列 `tdmq`、`mqtt`、`gaap`、`scf` → **两处不一致，以实际返回为准**。
> `CreateCertificateBindResourceSyncTask`：**若该证书已有查询任务，直接返回该任务 ID**。

### 2.3 「这张证书部署到了哪些实例」——`DescribeHostXxxInstanceList` 家族（11 个 Action）

均吃 `CertificateId`（+`IsCache`、部分支持 `Filters`），是**统一跨产品的"证书 → 实例"查询**：

| Action | 文档号 | 产品 |
| --- | --- | --- |
| `DescribeHostClbInstanceList` | 91662 | CLB |
| `DescribeHostCdnInstanceList` | 91663 | CDN |
| `DescribeHostApiGatewayInstanceList` | 91664 | API 网关 |
| `DescribeHostCosInstanceList` | 91661 | COS |
| `DescribeHostDdosInstanceList` | 91660 | DDoS |
| `DescribeHostLiveInstanceList` | 91656 | 直播 |
| `DescribeHostTeoInstanceList` | 91655 | EdgeOne |
| `DescribeHostTkeInstanceList` | 91654 | TKE |
| `DescribeHostVodInstanceList` | 91651 | 点播 |
| `DescribeHostWafInstanceList` | 91650 | WAF |
| `DescribeHostLighthouseInstanceList` | 91657 | 轻量服务器 |

CLB 的返回结构（文档示例，字段名已核对）：
`ClbInstanceDetail{LoadBalancerId, LoadBalancerName, Forward(0传统/1应用), Listeners[]{ListenerId, ListenerName, SniSwitch(1开/0关), Protocol(HTTPS|TCP_SSL), Certificate{CertId, DnsNames[], CertCaId, SSLMode}, Rules[]{LocationId, Domain, IsMatch, Certificate, NoMatchDomains, Url}, NoMatchDomains}}`
其 `Filters.N` **FilterKey 仅 `domainMatch`**（FilterValue `1`=匹配 / `0`=不匹配，默认查匹配），另有 `AsyncCache`、`OldCertificateId`。
降级错误：`CertificateHostResourceInstanceHugeLimit`（账号下实例过多）。

### 2.4 部署：`DeployCertificateInstance`（[91667](https://cloud.tencent.com/document/api/400/91667)）

| 参数 | 必选 | 说明 |
| --- | --- | --- |
| `CertificateId` | 是 | 待部署证书 ID（**一次一张**） |
| `InstanceIdList.N` | 是 | 实例标识数组，按资源类型拼接（见下表） |
| `ResourceType` | 否 | **不传默认 `clb`** |
| `Status` | 否 | 仅云直播：`-1`未关联证书 / `1`https 已开启 / `0`https 已关闭 |
| `IsCache` | 否 | `1` 用缓存（默认，半小时）/ `0` 实时 |

`InstanceIdList` 拼接格式（官方原文）：

| ResourceType | 格式 | 需 Region |
| --- | --- | --- |
| `clb` | 开 SNI：`LoadBalancerId\|ListenerId\|Domain`；未开 SNI 或四层：`LoadBalancerId\|ListenerId` | ✅ |
| `cdn` | `Domain\|计费开关(on/off)` | ❌ |
| `ddos` | `InsId\|Domain\|VirtualPort` | ❌ |
| `live` / `vod` / `waf` / `teo` | `Domain` | ❌（waf 为 ✅） |
| `apigateway` | `ServiceId\|Domain` | ✅ |
| `cos` | `Region\|Bucket\|Domain` | ✅ |
| `tke` | `ClusterId\|NameSpace\|SecretName` | ✅ |
| `lighthouse` | `Region\|InstanceId\|Domain` | ✅ |
| `tse` | `GatewayId\|CertificateId` | ✅ |
| `tcb` | `Type\|Region\|EnvId\|Domain` | ✅ |
| `mqtt` | `InstanceId\|CertId` | ✅ |
| `gaap` | 出现在 `InstanceIdList` 说明里（`InstanceId\|ListenerId`），但**不在 `ResourceType` 取值表中** → **文档自相矛盾，未验证** | ? |

**响应与异步语义**：返回 `DeployRecordId`（Integer）+ `DeployStatus`（`1`=创建成功；**`0`=当前存在部署中的任务、本次未创建新任务，此时 `DeployRecordId` 是那个进行中任务的 ID**）。任务查询：`DescribeHostDeployRecord`（[91659](https://cloud.tencent.com/document/api/400/91659)，`DeployRecordInfo{Id, CertId, ResourceType, Region, Status(0未开始/1成功/2失败), CreateTime, UpdateTime}`）+ `DescribeHostDeployRecordDetail`（[91658](https://cloud.tencent.com/document/api/400/91658)，明细含 `InstanceId`/`ListenerId`/`Domains[]`/`ErrorMsg`/`SniSwitch`/`Bucket`/`Namespace`/`SecretName`/`InstanceStatus`/`ListenerStatus` 等）。重试 `DeployCertificateRecordRetry`（91666）、回滚 `DeployCertificateRecordRollback`（91665）。

**硬约束（官方原文）**：

1. **"一个证书ID，相同的资源类型，只能创建一个部署任务，必须等部署任务执行完成，才能创建新的部署任务。"** → 违反报 `FailedOperation.CertificateDeployHasPendingRecord`。
2. 创建任务前的匹配校验（不通过直接失败）：证书与实例当前绑定证书**相同**；证书与传入域名不匹配；CLB 七层监听器下无规则；CLB 七层未开 SNI 且监听器下存在不匹配域名；CLB 规则为正则表达式。
3. 部署**没有"解绑"语义**（除云直播的 `Status`）→ 解绑 HTTPS 必须走产品侧 API。

相关错误码：`CertificateDeployInstanceFormatError`、`CertificateHostResourceTypeInvalid`、`CertificateNotDeployInstance`、`CertificateNotAvailable`、`CertificateHostDeployCanNotAllow`、`FailedOperation.RoleNotFoundAuthorization`、`InvalidParameter.RegionNotEmpty`。

### 2.5 另两条部署/更新路径

| Action | 用途 | 关键点 |
| --- | --- | --- |
| `UpdateCertificateInstance`（[91649](https://cloud.tencent.com/document/api/400/91649)「一键更新旧证书资源」） | 按 `OldCertificateId` 自动找出全部绑定资源并替换为新证书 | 入参：`OldCertificateId`、`ResourceTypes.N`（含 `scf,tdmq,gaap` 等更广列表）、`CertificateId` 或公钥+私钥、`ResourceTypesRegions.N`、`Repeatable`、`ExpiringNotificationSwitch` 等；出参 `DeployRecordId`（**为 0 表示进行中，需重复请求直到 >0**）。**字段结构以文档为准，需实测确认** |
| `UploadUpdateCertificateInstance`（[119791](https://cloud.tencent.com/document/api/119791)） | **证书 ID 不变、只替换证书内容**（资源侧零变更） | QPS **20**；另有记录查询 `DescribeHostUploadUpdateRecord`(120057) / 重试(120055) / 回滚(120054) |

### 2.6 删除

| Action | 入参 | 说明 |
| --- | --- | --- |
| `DeleteCertificate`（[41675](https://cloud.tencent.com/document/api/400/41675)） | `CertificateId`、`IsCheckResource`（**默认不检查**） | `IsCheckResource=true` 需服务角色授权，**删除变异步**并返回 `TaskId`；出参 `DeleteResult` + `TaskId` |
| `DeleteCertificates`（[107800](https://cloud.tencent.com/document/api/107800)） | `CertificateIds.N`（**≤100**）、`IsSync`（语义同 `IsCheckResource`） | 出参 `Success[]`、`Fail[]BatchDeleteFail{CertId, Msg}`、`CertTaskIds[]CertTaskId{CertId, TaskId}` |

> ⚠️ **批量删除的 `IsSync` 默认不检查云资源关联** —— 这与安全预期相反，是必须显式传 `true` 的地方。

任务结果 `DescribeDeleteCertificatesTaskResult`（[105464](https://cloud.tencent.com/document/api/105464)）：`DeleteTaskResult[]{TaskId, CertId, Status, Error, CacheTime, Domains[]}`，**Status：`0`进行中 / `1`成功 / `2`失败 / `3`未授权服务角色 / `4`有未解绑的云资源 / `5`查询关联云资源超时**。

拒绝删除的错误码（判定"不能删"的锚点）：`FailedOperation.BoundResources`（有未解绑云资源）、`FailedOperation.DeleteResourceFailed`、`FailedOperation.CannotBeDeletedIssued`、`FailedOperation.CannotBeDeletedWithinHour` / `DeleteFailedTimeNotUp`（**免费证书申请 1 小时内不允许删除**）、`RenewNotIssued`、`InvalidCertificateStatusCode`、`OrderAlreadyReplaced`、`RoleNotFoundAuthorization`。

**删除后的生效时间与不可逆性：官方未作任何说明 → 未验证。**

---

## 3. 各产品侧事实（枚举资源、读当前证书、绑定）

### 3.1 总览

| 产品 | 枚举接口 | 当前证书字段名 | Region | 到期时间 |
| --- | --- | --- | --- | --- |
| CLB（[214](https://cloud.tencent.com/document/api/214/30686)，`2018-03-17`） | `DescribeListeners`（**不分页**，20/s）、`DescribeLoadBalancers`、`DescribeLoadBalancerListByCertId`（按证书反查 LB） | `Listener.Certificate.CertId`（结构 `CertificateOutput`，另有 `CertCaId`、`ExtCertIds[]`）、SNI 时看 `Listener.Rules[]` | 必传 | **不返回** |
| CDN（[228](https://cloud.tencent.com/document/api/228/41117)，`2018-06-06`） | `DescribeDomainsConfig`（分页 Offset/Limit，**Limit 最大 100**，20/s）、`DescribeDomains`、`DescribeCertDomains` | **已核实**：`Https.SslStatus` + `Https.CertInfo`（结构 `ServerCert`） | 不需传 | ✅ `ServerCert.ExpireTime` / `DeployTime` |
| WAF（`2018-01-25`） | `DescribeHosts`、`DescribeHost`、**`DescribeCertificatedDomain`（按 SSL 证书 ID 查可绑定域名）** | 未验证（WAF 存在自带免费证书体系 `ApplyFreeCertificate`/`DescribeFreeCertificates`） | 未验证 | 未验证 |
| API 网关（[628](https://cloud.tencent.com/document/api/628/45185)，`2018-08-08`） | `DescribeServiceSubDomains`（分页 Offset/Limit，**Limit 最大 100**，20/s） | `DomainSet.CertificateId`（还有 `DomainName`/`Protocol`/`IsForcedHttps`/`IsDefaultMapping`/`Status`） | 必传 | **不返回** |
| COS（非 tencentcloud-sdk-go 产品模块） | 无标准枚举；`PutBucketDomain` 字段未验证 | 未验证 | 必传（部署时） | 未验证 |
| 直播（[267](https://cloud.tencent.com/document/api/267/78655)，`2018-08-01`） | `DescribeLiveDomains`（字段未验证） | `CloudCertId`（`ModifyLiveDomainCertBindings` 入参） | 不需传 | 未验证 |
| 点播（`2018-07-17`） | `DescribeVodDomains` | 未验证 | 不需传（部署时） | 未验证 |
| TKE（`2018-05-25`） | 无证书枚举接口 | 证书存在 K8s TLS Secret 中 | 必传（部署时） | 不适用 |
| EdgeOne / TEO（[1552](https://cloud.tencent.com/document/api/1552/80764)，`2022-09-01`） | **已核实**：`DescribeZones`（分页 Offset/Limit）→ 逐个 Zone 调 `DescribeAccelerationDomains`（`ZoneId` 必填，分页 Offset/Limit） | **已核实**：`AccelerationDomain.Certificate`（结构 `AccelerationDomainCertificate`） | 不需传（部署时） | ✅ `ServerCertInfo.ExpireTime`（在 `Certificate.List[]` 内） |
| DNSPod | 无证书概念 | — | — | — |

### 3.2 各产品的正文级细节

**CLB**
- 证书引用方式：**两者都支持** —— `CertificateInput{SSLMode, SSLVerifyClient, CertId, CertCaId, CertName, CertKey, CertContent, CertCaName, CertCaContent}`（`CertId` 不填则必须给 `CertContent`+`CertKey`+`CertName`）；`CertInfo{CertId, CertName, CertContent, CertKey}`。
- 创建/修改：`CreateListener`（20/s，**异步**，需 `DescribeTaskStatus` 查，支持 `Ports.N` 批量建监听器；`Certificate` 与 `MultiCertInfo` 至少传一个、不能同传）、`ModifyListener`（入参未验证）、`ModifyDomainAttributes`（**SNI 多证书场景改单域名证书的正确入口**，URL 与入参未验证）、`ReplaceCertForLoadBalancers`（名称由数据结构引用列表确认，URL 未验证）。
- SNI 域名的证书映射在 `Listener.Rules[]` 中，**规则内字段名未验证**（推测 `Domain`/`Certificate`）。
- 解绑：`DeleteListener`（Action 名未验证）；TCP_SSL 可用 `CreateListener.SslCloseSwitch=true` 降级为明文 TCP（仅 CreateListener 文档确认）。
- `DescribeLoadBalancers` 分页参数未验证。

**CDN**（字段与语义均已核对：SDK 源码 + 官方文档正文）

- **读**：`DescribeDomainsConfig`（[228/41117](https://cloud.tencent.com/document/api/228/41117)，20/s；`Limit` 默认 100、**最大 100**）→ `Domains[]`（`DetailDomain`）→ `Https`：
  `Https{Switch(必填 on/off), Http2, OcspStapling, VerifyClient, CertInfo *ServerCert, ClientCertInfo *ClientCert, Spdy, SslStatus, Hsts, TlsVersion}`
  `ServerCert{CertId, CertName, Certificate, PrivateKey, ExpireTime, DeployTime, Message, From}`
  `ClientCert{Certificate, CertName, ExpireTime, DeployTime}`（**没有** `PrivateKey`/`CertId`；是否启用由 `Https.VerifyClient` 控制）
  - `Https.SslStatus` 枚举：**`closed` 已关闭 / `deploying` 部署中 / `deployed` 部署成功 / `failed` 部署失败** → **部署生效判定就靠它**
  - `Https.CertInfo == null` = "未配置证书"，与 `Switch=off`（HTTPS 加速开关）是两件事
  - 托管 vs 自有：入参按"填哪个"表达 —— `CertId` → 托管；`Certificate`+`PrivateKey` → 自有上传。**`ServerCert` 没有 `Type` 字段**（与 TEO 不同），出参可用 `From`（示例 `upload`）区分
  - ⚠️ `ServerCert.DeployTime` 的语义是**证书颁发时间**，不是"部署时间"
- **写路径 A（推荐）**：`ModifyDomainConfig`（[228/94258](https://cloud.tencent.com/document/api/228/94258)，2023-06 新增，20/s）入参 `{Domain, Route, Value}`：`Route` 为点分路径（**最后一段是叶子节点，非叶子节点配置保持不变**），`Value` 为 JSON 字符串且固定以 `update` 为 key。**官方示例 4 正是"更新 HTTPS 服务器证书 ID"**：`Route="Https.CertInfo.CertId"`、`Value="{\"update\":\"0VpOXiPz\"}"` → **换证书用它，单点更新不会误伤同级字段**。
- **写路径 B**：`UpdateDomainConfig`（[228/41116](https://cloud.tencent.com/document/api/228/41116)，20/s）入参 `Domain`（**必填、单域名、不支持批量**）+ `Https`。官方原文警告：**"必须传递整个对象的所有属性，未传递的属性将使用默认值"** → 风险高，仅在需要一次改多个配置项时使用。**两条路径并存，官方均未标注废弃**。
- **写路径 C**：SSL 侧 `DeployCertificateInstance(ResourceType=cdn, "Domain|计费开关(on/off)")` → 完全不必调 CDN 配置接口，由 SSL 侧下发；适合"把某证书批量打到一批域名"。
- 响应**只有 `RequestId`（无 task id）** → 轮询 `Https.SslStatus` 至 `deployed`/`failed` 复核（耗时上限文档未给 → 指数退避 + 超时上限）。
- `DescribeCertDomains`（[228/42491](https://cloud.tencent.com/document/api/228/42491)，20/s）入参 `Cert`（PEM base64）或 `CertId`（不能同时为空；都给以 `CertId` 为准）+ `Product`(cdn|ecdn)；出参 `Domains[]`（该证书覆盖且本账号已接入 CDN 的域名）、`CertifiedDomains[]`（其中已配过证书的）→ **可用于清理阶段的交叉验证**。
- 状态相关错误码：`InvalidParameter.CDNStatusInvalidDomain`、`CdnHostInvalidStatus`、`ResourceUnavailable.CdnHostNoIcp`（未备案，备案同步周期 2 小时）、`ResourceInUse.CdnOpInProgress`、`ResourceUnavailable.CdnHostIsLocked`、`InvalidParameter.AccessPortOpenedHttps`（**启用 HTTPS 要求访问端口配置里 443 为开启**）。
- 证书错误码：`FailedOperation.SslCertNotFound`（走 CertId 路径）、`InvalidParameter.SslCertMatchError` / `SslCertParseError` / `SslCertChainError`（走 PEM 路径）。

**EdgeOne / TEO**（字段与语义均已核对：SDK 源码 + 官方文档正文；SDK 共 422 个 Action）

- **站点枚举**：`DescribeZones`（[1552/80713](https://cloud.tencent.com/document/api/1552/80713)，20/s）`{Offset(默认0), Limit(默认20、最大100), Filters, Order, Direction}` → `TotalCount` + `Zones[]{ZoneId, ZoneName, AliasZoneName, Area(global|mainland|overseas), Type, Status, CnameStatus, ActiveStatus, LockStatus, Paused, ...}`。不填 Filters 返回当前账号有权限的全部站点。**站点是账号级资源，与 Region 无关**。
- **域名枚举**：`DescribeAccelerationDomains`（[1552/86336](https://cloud.tencent.com/document/api/1552/86336)，20/s）**`ZoneId` 必填**，`Limit` 默认 20、最大 200 → `TotalCount` + `AccelerationDomains[]{ZoneId, DomainName(**注意不叫 Host**), DomainStatus(online|process|offline|init), Cname, IdentificationStatus, OriginDetail, OriginProtocol, HttpOriginPort, HttpsOriginPort, Certificate, CreatedOn, ModifiedOn}`。
- **证书结构与状态**：`AccelerationDomainCertificate{Mode, List[]CertificateInfo, ClientCertInfo(MutualTLS), UpstreamCertInfo}`；`CertificateInfo{CertId, Alias, Type(default|upload|managed), ExpireTime, DeployTime, SignAlgo, CommonName, Status}`；`Status ∈ deployed|processing|applying|failed|issued`。
- **写**：`ModifyHostsCertificate`（[1552/80764](https://cloud.tencent.com/document/api/1552/80764)，20/s）入参 `{ZoneId(必填), Hosts.N(必填，支持批量), Mode, ServerCertInfo.N(仅 Mode=sslcert 生效，传 CertId), ClientCertInfo, UpstreamCertInfo}`。
  **`Mode` 枚举（官方原文）**：`disable` 不配置 / `eofreecert` 自动验证并部署 EdgeOne 免费证书 / `eofreecert_manual` 部署手动验证的免费证书 / **`sslcert` 配置 SSL 托管证书**（`ServerCertInfo` 仅此模式生效，传 SSL 证书 ID）。
  响应**只有 `RequestId`（无 task id）** → 回读 `AccelerationDomain.Certificate.List[].Status` 至 `deployed` 复核。
  **批量一致性约束**：同一次调用的多个 Host 最终配置必须一致，否则 `OperationDenied.HostsCertificateInconsistency`（回源/双向认证各有对应错误码）。
- **「某域名当前用哪张证书」的判定（关键）**：TEO **没有** `DescribeHostsCertificates`，证书相关 Action 只有 3 个（`ModifyHostsCertificate` / `ApplyFreeCertificate` / `CheckFreeCertificateVerification`）。读取一律用 `DescribeAccelerationDomains` → `Certificate.Mode` + `Certificate.List[]`：
  - `Mode=sslcert` → `List[].CertId` 是**腾讯云 SSL 证书 ID**（可与证书池比对）
  - `Mode=eofreecert*` → **EdgeOne 免费证书**，`CertId` 形如 `teo-285inl1otbtd`（`Type=default`、`Alias="EdgeOne default"`），**不在腾讯云 SSL 证书池里**
  - `Mode=disable` → 未配置证书
  ⚠️ 官方在 `DescribeDefaultCertificates` 页明确：**"如需查询域名配置的证书，请使用 `DescribeAccelerationDomains`"** → 不要用旧接口 `DescribeHostsSetting` / `DescribeDefaultCertificates` 读域名证书。
- **EdgeOne 免费证书体系（与 SSL 证书服务是两套东西）**：`ApplyFreeCertificate{ZoneId, Domain, VerificationMethod(http_challenge|dns_challenge)}` → 配置验证内容 → `CheckFreeCertificateVerification{ZoneId, Domain}`；由 TrustAsia / Let's Encrypt 颁发、**有效期 90 天、到期前 15 天自动续期、不支持下载**；仅 CNAME 接入模式需要调用（NS/DNSPod 托管走自动验证）。与 SSL 的区别：申请/查询是 TEO 的 Action、`CertId` 前缀 `teo-`、不可下载。
  ⚠️ 还有**第三套**容易混淆的东西：**SSL 服务自己也能签发免费证书**（`ApplyCertificate`，400/41678），它会出现在 SSL 证书列表里。
- **回源证书**：与站点证书是不同配置项，但在同一个 `ModifyHostsCertificate` 入参里 —— `UpstreamCertInfo{UpstreamMutualTLS(MutualTLS), UpstreamCertificateVerify(OriginCertificateVerify{VerificationMode: disable|custom_ca, CustomCACerts[]})}`；其中**回源双向认证当前为白名单内测**。CDN 侧对应 `Origin.AdvanceHttps{VerifyOriginType(off|oneWay|twoWay), CertInfo, OriginCertInfo}`。
- 其他相关 Action：`ModifyAccelerationDomain`、`CreateAccelerationDomain`、`DeleteAccelerationDomains`、`ModifyAccelerationDomainStatuses`、`DescribeAliasDomains` / `CreateAliasDomain`（别名域名）、`IdentifyZone`、`CreateZone`、`DescribeZoneSetting`。
- **Region**：TEO 文档中 `Region` 一律标注"否/可选"（不像 CDN 会写"不需要传递"）；站点是账号级、部署证书不需要 Region 定位资源，官方推荐就近接入 `teo.tencentcloudapi.com`。SDK `NewClient` 仍需一个 region 字符串（传 `ap-guangzhou` 即可；**传空串是否可接受未验证**）。
- 关键错误码：`InvalidParameter.CertNotMatchDomain`（证书与域名不匹配）、`FailedOperation.CertificateNotFound`、`ResourceUnavailable.CertNotFound`、`FailedOperation.CertificateHasExpired`、`InvalidParameter.CertToExpire`、`ResourceUnavailable.HostNotFound`（域名不存在**或未开启代理**）、`ResourceUnavailable.ZoneNotFound`、`OperationDenied.HostsCertificateInconsistency`、`OperationDenied.DomainNoICP`、`InvalidParameter.ActionInProgress`、`LimitExceeded.RateLimitExceeded`。**TEO 没有"Zone 未实名"错误码**（实名类错误在 SSL 侧：`FailedOperation.NoRealNameAuth`）。

**WAF**
- 证据：存在 `DescribeCertificatedDomain`（官方说明"通过 ssl 证书 id 获取可以绑定的 waf 中的域名"）→ **WAF 用 SSL 证书 ID 关联域名**；另有 `CreateHost`（负载均衡型）、`AddSpartaProtection`（SaaS 型）、`DeleteHost` / `DeleteSpartaProtection`、`DescribeCertificateVerifyResult`、`DescribeUserClbWafRegions`。
- `ModifyHost` 存在于 SDK action 名单中，但入参、分页、字段名、Region 要求均**未验证**。

**API 网关**：`DomainSet` 字段已确认（`DomainName`、`CertificateId`、`Protocol`、`IsForcedHttps`、`IsDefaultMapping`、`NetType`、`Status`、`RegistrationStatus`）；绑定/解绑 Action 名（`BindSubDomain`? `ModifySubDomain`? `UnBindSubDomain`?）**未验证** —— SDK 名单中确认存在 `ModifySubDomain`。

**COS**：**开启 CDN 加速的自定义域名，证书在 CDN 侧配置，COS 不持有证书**；关闭 CDN 加速的自定义源站域名，在 COS 控制台绑域名与证书。→ 工程上**优先走 SSL `DeployCertificateInstance(ResourceType=cos)`**，不要依赖 `PutBucketDomain` 传证书（其 XML 字段全部未验证）。
**COS 不在 tencentcloud-sdk-go 产品模块内**（用独立的 `cos-go-sdk-v5`）。

**直播**：`ModifyLiveDomainCertBindings`（20/s）入参 `DomainInfos.N`（`LiveCertDomainInfo{DomainName, Status}`，**支持批量**）+ `CloudCertId`（腾讯云 SSL 证书 ID）与"公钥+私钥"**二选一**；传 `CloudCertId` 时忽略公钥私钥。返回 `MismatchedDomainNames[]`（**与证书不匹配被跳过的域名**）+ `Errors[]`。错误码已覆盖三个关键场景：域名不在 SAN 内（`InvalidParameter.CrtDomainNotFound` / `ResourceNotFound.DomainNotExist`）、**证书过期**（`InvalidParameter.CrtDateOverdue`）、证书与私钥不匹配（`FailedOperation.CertificateMismatch` / `InvalidParameter.CrtKeyNotMatch`）。

**TKE**：SSL 部署到 TKE 的资源标识是 `ClusterId|NameSpace|SecretName` → 本质是**把证书写进集群某命名空间的 TLS Secret**，不是改 Ingress 对象。TKE 侧是否有"引用 CertId 的 Ingress 证书接口"**未验证**（未找到）。

---

## 4. 凭证与签名

| 事项 | 事实 |
| --- | --- |
| Module 路径 | 公共包 `github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common`；SSL 包 `github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205`（**module 路径自带 `/v20191205`**，import 时也要带上） |
| 版本（本地实测） | `ssl` module：**v1.3.147**（已 `go get` 成功并逐字段提取）；`common` module：**v1.3.191**。**两个 module 版本号不同步，须各自 pin**；官方建议产品包与 common 包保持同版本 |
| 父模块 | `github.com/tencentcloud/tencentcloud-sdk-go` 本身只是版本标记，**不含任何产品包**（v1.3.191 里只有 README/CHANGELOG） |
| 客户端构造 | `ssl.NewClient(credential, region, clientProfile)`；`NewClientWithSecretId` 已标注 **Deprecated** |
| 环境变量 | `TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY`；临时密钥加 `TENCENTCLOUD_TOKEN`；角色扮演加 `TENCENTCLOUD_ROLE_ARN` / `TENCENTCLOUD_ROLE_SESSION_NAME` |
| tccli profile | `~/.tccli/<name>.credential`，JSON 字段为**小写驼峰**：`{"secretId": "...", "secretKey": "..."}`（多账号 = 多文件） |
| 凭证优先级（tccli 原文） | 实例角色 > 角色 > TKE OIDC > 临时密钥 > 永久密钥；命令行参数 > 环境变量 |
| 跨账号角色扮演 | STS「申请扮演角色临时访问凭证」；SDK `common` **v1.3.181 起提供 `RoleArnProvider`**（`DefaultRoleArnProviderWithCredential` / `NewRoleArnProviderWithCredential`） |
| 限流维度 | API + 接入地域 + **子账号** |

---

## 5. 明确「未验证」清单（实现前用测试账号实测）

| # | 未验证项 | 验证方式 | 状态 |
| --- | --- | --- | --- |
| 1 | CDN `Https` / `ServerCert` / `ClientCert` 的字段名 | — | ✅ **已解决**（SDK 源码） |
| 2 | CDN `ModifyDomainConfig` 的 `Route`/`Value` 语义；与 `UpdateDomainConfig` 的关系 | — | ✅ **已解决**：官方示例 4 就是改证书 ID —— `Route="Https.CertInfo.CertId"`、`Value="{\"update\":\"<CertId>\"}"`；两者并存、均未废弃，换证书优先用前者 |
| 3 | CLB `ModifyDomainAttributes` / `DeleteListener` / `ReplaceCertForLoadBalancers` 入参；`Listener.Rules[]` 字段名 | 调 `DescribeListeners` 看真实返回 | 后置（本批次不做 CLB） |
| 4 | WAF `DescribeHosts` / `ModifyHost` 的分页、证书字段名、Region | 实测 | 后置 |
| 5 | apigateway 绑定/解绑自定义域名的 Action 名与入参 | 读 SDK 名单 + 实测 | 后置 |
| 6 | COS `PutBucketDomain` 的 XML 字段与是否支持 CertId | 实测（优先走 SSL 部署） | 后置 |
| 7 | live `DescribeLiveDomains` 证书字段；"解绑域名证书" Action 名 | 实测 | 后置 |
| 8 | VOD 域名证书接口全貌 | 实测 | 后置 |
| 9 | TKE 是否有 Ingress 证书接口 | 实测 | 后置 |
| 10 | `UpdateCertificateInstance` / `UploadUpdateCertificateInstance` 入参结构 | 实测 | P2（不作为主路径） |
| 11 | `gaap` 能否用于 `DeployCertificateInstance` | — | 跳过（文档自相矛盾，本批次用不到） |
| 12 | 删除证书的不可逆性与生效时间；付费证书配额上限；部署任务并发上限 | 测试账号 + 保守设计 | P1 |
| 13 | ~~tccli profile 的临时密钥/角色键名~~ | — | ❌ **不适用**（A1 禁止读取 tccli 凭证） |
| 14 | EdgeOne `ModifyHostsCertificate` 的 `Mode` 取值与 `ServerCertInfo.CertId` 来源 | — | ✅ **已解决**：`Mode ∈ disable\|eofreecert\|eofreecert_manual\|sslcert`；`ServerCertInfo` 仅 `Mode=sslcert` 生效并传 SSL 证书 ID |
| 15 | EdgeOne 免费证书与腾讯云 SSL 是否同一 ID 空间 | 账号对照（低优先） | ✅ **基本解决**：`Mode=eofreecert*` 即 EdgeOne 免费证书，其 `CertId` 形如 `teo-xxx`、`Type=default` → 判定不再依赖实测；仅"免费证书是否也出现在 SSL 列表"待对照 |
| 16 | CDN `Https.SslStatus` 的取值 | — | ✅ **已解决**：`closed` / `deploying` / `deployed` / `failed` |
| 17 | `DeployCertificateInstance(ResourceType=teo)` 只传 `Domain` 如何唯一定位 | — | ✅ **已解决**：teo 分支 schema 无 ZoneId 位；域名在 EdgeOne 内全局唯一（重复接入报 `ResourceUnavailable.DomainAlreadyExists`）；`DescribeHostTeoInstanceList` 会回带 `ZoneId` |
| 18 | TEO `Region` 传空字符串是否被接受 | 实测 | P3（低风险：传 `ap-guangzhou` 即可） |
| 19 | **SSL 部署到 CDN/EdgeOne 所需服务角色的确切名称** | 控制台授权页对照 | P1（删除侧已知 `SSL_QCSLinkedRoleInReplaceLoadCertificate`） |
| 20 | `SslStatus` / `CertificateInfo.Status` 从 `deploying`→`deployed` 的耗时上限 | 实测 | P2（决定轮询超时与退避参数） |
| 21 | EdgeOne 免费证书是否也出现在 SSL `DescribeCertificates` 列表 | 账号对照 | P3（不影响判定，仅影响展示口径） |
| 22 | `ModifyDomainConfig` 与 `UpdateDomainConfig` 的底层一致性/隐性废弃 | 实测 | P3 |

---

## 6. 来源

- SSL：[DescribeCertificates](https://cloud.tencent.com/document/api/400/41671) · [DescribeCertificateDetail](https://cloud.tencent.com/document/api/400/41673) · [数据结构](https://cloud.tencent.com/document/api/400/41679) · [DeployCertificateInstance](https://cloud.tencent.com/document/api/400/91667) · [部署记录](https://cloud.tencent.com/document/api/400/91659) · [部署记录详情](https://cloud.tencent.com/document/api/400/91658) · [重试](https://cloud.tencent.com/document/api/400/91666) · [回滚](https://cloud.tencent.com/document/api/400/91665)
- 关联：[DescribeDeployedResources](https://cloud.tencent.com/document/api/400/62674) · [CreateCertificateBindResourceSyncTask](https://cloud.tencent.com/document/api/400/97759) · [TaskResult](https://cloud.tencent.com/document/api/400/97757) · [TaskDetail](https://cloud.tencent.com/document/api/400/97758) · [DescribeHostClbInstanceList](https://cloud.tencent.com/document/api/400/91662)
- 更新/删除：[UpdateCertificateInstance](https://cloud.tencent.com/document/api/400/91649) · [DeleteCertificate](https://cloud.tencent.com/document/api/400/41675) · [DeleteCertificates](https://cloud.tencent.com/document/api/400/107800) · [DescribeDeleteCertificatesTaskResult](https://cloud.tencent.com/document/api/400/105464)
- 公共：[公共参数/地域](https://cloud.tencent.com/document/api/400/41660) · [API 概览与限流](https://cloud.tencent.com/document/api/400/41681) · [Action 索引](https://cloud.tencent.com/document/api/400/41664) · [免费证书配额](https://cloud.tencent.com/document/product/400/46849)
- 产品：[CLB DescribeListeners](https://cloud.tencent.com/document/api/214/30686) · [CLB 按证书反查](https://cloud.tencent.com/document/api/214/40953) · [CDN DescribeDomainsConfig](https://cloud.tencent.com/document/api/228/41117) · [CDN UpdateDomainConfig](https://cloud.tencent.com/document/api/228/41116) · [CDN ModifyDomainConfig](https://cloud.tencent.com/document/api/228/94258) · [CDN 数据结构](https://cloud.tencent.com/document/api/228/30987) · [EdgeOne 配置域名证书](https://cloud.tencent.com/document/api/1552/80764) · [apigateway DescribeServiceSubDomains](https://cloud.tencent.com/document/api/628/45185) · [live ModifyLiveDomainCertBindings](https://cloud.tencent.com/document/api/267/78655) · [COS 自定义域名 HTTPS](https://cloud.tencent.com/document/product/436/11142) · [WAF 操作审计清单](https://cloud.tencent.com/document/product/629/46736)
- 凭证与 SDK：[tccli 凭证管理](https://cloud.tencent.com/document/product/440/96291) · [Go SDK 安装](https://cloud.tencent.com/document/sdk/Go) · [ssl module 版本](https://pkg.go.dev/github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205?tab=versions)
- 源码：[ssl/client.go](https://raw.githubusercontent.com/TencentCloud/tencentcloud-sdk-go/master/tencentcloud/ssl/v20191205/client.go) · [ssl/models.go](https://raw.githubusercontent.com/TencentCloud/tencentcloud-sdk-go/master/tencentcloud/ssl/v20191205/models.go)
