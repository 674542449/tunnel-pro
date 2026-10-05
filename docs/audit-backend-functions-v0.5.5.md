# 后端功能逐项审计：v0.5.5 基线及本轮修复

审计日期：2026-10-03。范围为 `internal/control`、控制器启动入口和与其直接关联的部署/备份脚本。v0.5.5 是本次审查起点；本轮修复进入 v0.5.6 工作区。本文记录代码实际作用，不把“存在接口”“本地测试通过”“线上配置完成”“第三方真实结算完成”混为一谈。

本次检查了路由到事务落盘、配置参数到消费点、后台维护启动路径和现有验收记录。没有向真实客户发邮件、修改线上账户或发起真实支付。本地复现使用隔离文件存储。历史 PostgreSQL 验收引用已有报告，不能当作本轮新增审计功能的数据库验收。

状态说明：**已实现**表示存在可追踪的真实状态变化或外部调用；**依赖外部配置**表示代码已实现但仍需运营方提供服务；**有限实现**表示功能有明确边界；**缺口/已修复**分别指基线缺失及本轮落实的修补。接口没有对应产品承诺时，不把未开发的额外能力称为“假按钮”。

## 一、已复现并处理的问题

| 基线问题 | 复现/源码证据 | 本轮处理 |
|---|---|---|
| 新安装未自动建立备份任务 | `deploy/install-control.sh` 原本只安装控制器，没有安装备份脚本和 systemd timer；包中虽有备份文件，但没有执行链 | 主代理已把脚本、service、timer 安装与首份备份接入安装流程。旧线上部署曾另外安装任务，不能据此声称旧线上没有备份 |
| 备份失败/缺失/过期没有运维事件 | 原 `maintain()` 无备份分支；`readiness()` 只看成功时间，旧成功文件会掩盖新一次失败 | 新增 `backup-unavailable`，检查缺失、损坏、两小时过期、`failed_at>0`、未来时间；成功备份清除失败状态并生成恢复事件。商业模式关闭时不要求备份 |
| 必须 MFA 却允许缺失加密密钥的配置启动 | 隔离 HTTP 复现：`commercial.enabled=false`、`require_admin_mfa=true`、无 `security_key`，配置检查通过，后台 403，绑定 MFA 503 | 主代理已增强配置校验：商业模式、强制 MFA、test/smtp 邮件任一启用时必须有可用安全密钥 |
| 工单 `status` 参数被接受但不生效 | 隔离 HTTP：`POST /api/tickets` 带 `status:"closed"` 返回 200，持久化状态却是 `open` | 删除未消费字段，严格 JSON 解码返回 400；关闭工单明确走 `/tickets/{id}/close`，有回归测试 |
| 商业模式仍允许保存无效的非零试用小时数 | `accessSettings()` 会把商业模式试用强制投影为 0，原保存接口却可接受非零值 | 主代理已让保存接口明确拒绝不生效的非零配置，避免“保存成功但没有作用” |
| 审计日志只有四个原始字段 | 原 `Audit` 只有 time/actor/action/subject；大多数操作只能看到 ID，退款还把购买者记为操作者 | 新增可读操作者、对象、中文摘要和允许名单字段差异；管理员退款记录实际管理员，渠道回调明确记录系统/渠道来源。旧历史不被回填或改写 |

最初复现结果保存在私有目录 `.local/backend-audit-v0.5.5/probe-result.json`；其中配置锁定和工单参数结果描述的是**修复前**状态，不能作为修复后的当前行为。

## 二、功能与调用链矩阵

以下行号以本轮工作区为参考；后续增加代码时可能平移，函数名是稳定定位依据。

| 功能 | 入口及实际调用链 | 参数/状态实际作用 | 结论与验收证据 |
|---|---|---|---|
| 注册开关 | `api.go:138 login(register=true)` → `accessSettings` → 事务重新检查注册权限 | `registration` 控制真正的账户创建，非仅隐藏按钮 | 已实现；`TestSettingsPartialSaveAndRuntimeAccess`、用户旅程报告 |
| 注册与邮箱验证 | `api.go:138` → `challengeEmail`（`security.go:169`）→ Challenge/Outbox 持久化；`publicSecurity` 消耗一次性链接 | 验证前禁止购买/获取节点配置；链接仅存哈希，邮件正文单独加密 | 已实现，邮件投递依赖模式；`TestCommercialVerificationAndResetSingleUse` |
| 试运营邀请码 | `payments.go:385` 生成哈希邀请 → 注册事务检查有效期/消费 → `User.Beta` | `beta_invite_only` 约束注册，测试付款仅向合格账号开放 | 已实现；商业 UI 和用户旅程。邀请明文只在创建响应返回，不写审计 |
| 登录及会话 | `api.go:109 auth`、`api.go:138 login` → PBKDF2 验证 → Session 落盘 | 会话 24 小时；Cookie 写请求检查 Origin+CSRF；Bearer 使用持有者凭据 | 已实现；`TestCookieCSRFAndMalformedStore`、`TestRevokedSessionCannotCommitAndSelfPasswordReset` |
| 登录/MFA 防重放 | `security.go:111 verifyMFA` → 单调 TOTP step 或消耗恢复码 | 不是只检验 6 位字符串；已用恢复码不能重用 | 已实现；`TestCommercialMFAReplayRecoveryAndRoleGate` |
| 强制员工 MFA | 路由授权 → `mfaRequired`（`security.go:288`）→ MFA 设置/登录会话标记 | 管理员、财务、客服在策略要求下必须通过 MFA；设置入口仍可到达 | 已实现；MFA DOM 报告及 `TestAuditStaffCannotDisableRequiredMFA` |
| 改密/找回/管理员重置 | `security.go publicSecurity/security`、`api.go` password 分支 → 修改哈希、轮换隧道凭据、撤销会话和旧挑战 | 旧密码恢复链接、未完成 MFA 绑定在改密后失效；已正式启用 MFA 保留 | 已实现；`TestJourneyPasswordChangeInvalidatesOutstandingRecovery` 三条路径 |
| 角色与封禁 | `payments.go:22 adminPathAllowed`、角色接口；`api.go` user update → `commit` 再验身份 | 角色改变撤销会话；封禁影响后续鉴权；不能把用户页面当作授权边界 | 已实现；管理旅程与角色/MFA测试。无自定义权限矩阵、无角色自助申请 |
| 套餐价格/币种/天数/流量/设备数 | `api.go:620` → `financialPlan`+`validatePlan`+`validatePlanNodes` → Plans | 这些字段进入订单快照，进而成为实际金额与权益，不是展示字段 | 已实现；套餐 CRUD、币种、购物与配额回归 |
| 套餐节点范围 | Plan.NodeIDs → 订单快照 → `accountForNode`、`entitledToNode` → profile/lease | 选定节点限制实际节点访问；空数组表示符合账号测试/正式范围的全部节点 | 已实现；`TestAuditPlanNodeReferencesValidated`、`TestJourneyQueuedRefundQuotaAddonAndRestart` |
| 套餐下架/删除 | 保存 enabled=false 或删除 → 新订单只找 enabled 套餐 | 已生成的订单使用价格/节点等快照，不被当前套餐编辑追溯改价 | 已实现，既有订单按快照履约；节点引用另有删除保护 |
| 创建/取消订单 | `api.go:452` 创建快照；`cancelOrder`（`mutation.go`）事务检查所有者和状态 | 30 分钟待支付；已付款不能按取消处理；跨用户不允许取消 | 已实现；`TestOrderCancellationIsolationAndFulfilmentRace`、购物旅程 |
| 发起支付 | `payments.go:124 commerce` → 正式准备检查 → 快照可履约校验 → Payment → 网关适配器 | 已有付款也重新检查流量包条件/节点；方式选定后不可随意改链 | 已实现；`TestJourneyExistingCheckoutRevalidatesAddonBase`、网关测试 |
| 易支付支付宝/微信 | `payment_gateways.go:389 checkoutGateway` → V1 签名收银台；`epayWebhook:587` → 验签/商户/金额/状态校验 → `paidEvent` | 下单 URL 真正携带支付宝/微信类型；浏览器返回不会直接开通 | 已实现，依赖真实商户；历史模拟网关+真实 HTTP/PostgreSQL 验收通过，未做真实结算 |
| USDT 三网络 | `gatewayForMethod:355` → `usdt.trc20/usdt.bep20/usdt.polygon` → create-transaction | 选链值进入不可变付款网关快照；Bepusdt 负责汇率和钱包地址分配 | 已实现，依赖 Bepusdt 钱包；三条链模拟网关验收通过，不等同链上实收 |
| USDT 下单未知状态 | `createGatewayCheckout:467` 先保存 creating；网络不确定保存 unknown；维护发现被打断创建；回调解除未知状态 | 不盲目重建同 order_id，避免替换用户已经看到的收银台 | 有限实现：真实告警与回调恢复存在，**没有自动查询网关订单并对账**；需要人工核实未知结果 |
| Stripe | `payments.go:98 stripe` 外部 API → 收银台；`stripeWebhook:570` | 有真实 HTTPS API 与回调验签，实现整笔退款；设置仍以私有配置为主 | 已实现但不是当前用户主用渠道；无独立真实 Stripe 商户验收 |
| 到账幂等与金额 | `billing.go:236 paidEvent` → Payment/Order 双金额币种校验 → 去重 → grant | 同一付款不重复发权益；退款后的迟到通知不会恢复权益 | 已实现；网关、Stripe、PostgreSQL 唯一索引和重复通知测试 |
| 已收钱但无法履约 | `paidEvent` 调 `grantPurchase` 失败 → Payment=paid、Order=paid_review、运维事件 | 收款不被丢弃，不伪装为已开通；依旧支持退款 | 已实现；基础周期过期与节点缺失的三 provider 回归 |
| 续费排队 | `grantPurchase:82` 按周期权益末尾计算开始/结束 | 下一周期配额不能提前消耗；流量包不会把退款后重购周期推迟 | 已实现；`TestJourneyRefundedCycleAddonDoesNotDelayNewSubscription` |
| 流量加购 | `trafficBaseEnd:54` 找当前匹配周期 → 即时配额、沿用当前到期时间 | 需有当前有效周期，不能替代基础套餐；与当前节点范围交集相关 | 已实现；流量耗尽→加购→恢复访问→加购退款回归 |
| 全局额度与设备 | `leases.go:86 leaseUpdate` → 对账旧计数 → 回收自身未用预留 → 从有效权益分配最多 64MiB | 全局设备计数与多节点额度预留，防止每个节点各发一份额度 | 已实现；`TestCommercialMultiNodeQuotaExhaustionAndDevices`、崩溃恢复既有报告 |
| 节点异常预留对账 | `reserved/reconcileLease`、`payments.go:339` 管理 reconcile 接口 | 未上报的预留不自动释放；只在节点确认/管理员核实后释放差额，留操作审计 | 已实现，人工操作依赖运维证据；`TestCommercialManualReconciliationRejectsLiveLease` |
| 退款 | `refundPayment` 撤销订单权益并重排续费；Stripe API 或易支付/USDT 外部退款后登记 | 易支付/USDT 接口明确只登记**已完成**外部退款，不声称自动转账；重复记录不重复扣权益 | 已实现既定流程；有资金流水与权益回归。无部分退款与自动钱包转账 |
| 节点添加/安装 | `node_setup.go:110 adminNodeSetup` → 一次性安装凭据/文件清单 → shell 下载验 hash → complete 回报 | IP/端口/域名真正生成节点配置；完成前不暴露可用配置，错误阶段会回报 | 已实现；安装接口/权限/摘要钉住/重试测试。新服务器执行和 DNS/证书依赖外部条件 |
| 节点手工配置/启停/删除 | `api.go:581`、`validateNode`、引用保护 | 保存验证 CA/ECH/地址；测试模式不会误造正式节点；被套餐/待付订单/有效权益引用不能删除 | 已实现；`TestJourneyManualNodeScopeAndDeletionReferences` |
| 用户配置下载 | `profile.go:11 profile` → 权益校验 → 用户 token/设备 ID → JSON 或 zip | 返回的是真正可用的私人连接配置与 CA；停用/无额度/范围不符拒绝 | 已实现；`TestAdminCRUDValidationAndProfileBundle` |
| 节点心跳与流量 | `api.go:890 syncNode` → 节点认证 → 累计计数增量事务 → Grants | 心跳、在线连接、版本、能力与流量实际保存；倒退/重复计数受约束 | 已实现；`TestNodeCumulativeReportsAreIdempotent` |
| 网站设置/公告/发布 | `api.go:835 settings` → State.Access/Announcement/Release → `me/public/settings/release` | 注册/试用控制真实入口；公告真实显示；发布信息只描述已存在的安装包 | 已实现但**不上传或编译安装包、不部署网站、不自动升级客户端** |
| 工单 | `operations.go:29 support` → 所有权/角色/MFA → Ticket/Replies 事务 | 创建、回复、关闭真实改变状态；最多 10 个未关闭工单，200 回复/工单 | 已实现；工单隔离与 UI 测试。无分派、附件、邮件回复桥接、SLA 工单引擎 |
| 运维事件 | `operations.go:159 incident` → opened/resolved/ack → audit/Outbox | 心跳、网络探测、租约、邮件失败、未知收银台及新增备份事件可见；ack 只表示确认，不假装修好故障 | 已实现；运维和邮件去重回归 |
| 邮件投递 | `challengeEmail` 加密 Outbox → `RunMaintenance` → `deliver:317` → `sendMail:253` | SMTP 要求 TLS/STARTTLS，发送失败退避重试；成功清除正文；test 模式只查看不外发 | 已实现，依赖 SMTP。现有测试验证拒绝明文与通知去重，未证明真实邮箱成功收件 |
| 网络探测 | `operations.go:380 probeNodes` → `client.New/PublicGET` → 校验 HTTP2、状态码、严格模式 ECH | 真正对节点公开入口建立外部连接，不只是读取心跳字段 | 有限实现：这是控制器到节点的握手/公开页可用性探测，**不是用户所在地下载/视频吞吐测试** |
| JSON 落盘 | `store.go:150 Update` → 私有副本 → 回调 → 原子替换 | 失败不提交、返回错误；不是内存假保存 | 已实现；落盘重启、回滚、损坏文件拒绝覆盖测试 |
| PostgreSQL | `postgres.go:255 updatePostgres` → row lock/transaction → 唯一约束 → commit | SQL 事务与幂等约束真实执行；并非只把 DB 配置显示为已连接 | 已实现；历史隔离 schema 回归。本地本轮未配置 DSN 的用例会明确 skip |
| 加密备份/恢复 | `backup.go:26 EncryptedBackup` → AES-GCM；`RestoreNew:85` → 空目标检查 → 导入 | 实际验证密钥、完整性和状态；拒绝覆盖现有库/文件 | 已实现；密文篡改、新 schema 恢复测试。备份文件不替代保存私有配置和解密密钥 |
| 定时备份与保留 | `deploy/backup-control.py` → 控制器 CLI → status 原子写入 → 336 份保留 | 本轮 installer 接入 service/timer；失败也留无密钥状态；两小时新鲜度影响正式开售 | 已补齐调用链；失败/恢复维护回归。无异地备份复制、自动恢复演练或恢复网页按钮 |
| 详细审计日志 | `record` → Store.Update/updatePostgres → `auditSnapshot/enrichAudit`（`audit.go`） | 新记录拥有可读名称/摘要/允许名单差异；不序列化完整私密结构 | 本轮实现；`audit_details_test.go`。历史仅四字段的记录兼容保留；不是不可篡改审计系统 |

## 三、配置字段是否真正被消费

| 配置组 | 消费点 | 管理入口及边界 |
|---|---|---|
| `listen/public_url/data_file/database_url` | 控制器 main 启动监听、URL/回调生成、Store 初始化；配置相对路径解析 | 私有配置文件。`listen` 必须 loopback；数据库配置未提供不会伪装 PostgreSQL，而使用 JSON；正式开售明确要求 PostgreSQL |
| `admin_email/admin_password` | 仅空存储初始化管理员；已有存储以保存的账号/密码为准 | 不是“每次启动重置管理员密码”。账号改密走已有安全 API |
| `registration/trial_hours` | `accessSettings` 优先保存的 State.Access，回退启动配置；商业模式禁用普通试用 | 已有运行时网页入口；商业模式非零试用现明确拒绝 |
| `commercial.enabled/payment_mode` | 商业订单路线、测试/正式范围、付款方法、开售准备检查 | enabled 仅文件；payment_mode 可由后台 PaymentConfig 覆盖；禁用支付不抹掉历史财务记录 |
| `payment_secret/webhook_secret` | Stripe Basic/Bearer API 鉴权和 HMAC 回调验签 | 文件配置；易支付/Bepusdt 不使用这两个 Stripe 字段 |
| `security_key` | AES-GCM 加密 MFA、邮件正文、商户密钥；不同 purpose 作关联数据 | 文件配置，需要独立备份；丢失时不能凭数据库恢复这些密文；没有自动轮换/重加密 API |
| `require_admin_mfa/beta_invite_only` | 员工后台门禁与注册邀请验证 | 文件配置；本轮拒绝强制 MFA 却没有安全密钥的不可能组合 |
| `terms/privacy/refund_policy/live_approved` | 公共购买信息与真实收款准备门槛 | 文件配置，不存在完整的后台政策编辑/经营审批工作流；live_approved 是运营者声明，不是外部资质核验 |
| `mail.mode/host/port/username/password/from` | 模式分流、TLS/SMTP AUTH、发件 envelope/header | 文件配置；test 不是实际发送。准备检查表示“已配置”，不等于实时 SMTP 登录/收件测试 |
| `mail.alerts_to` | 运维故障/恢复邮件收件人 | 文件配置；无收件人或邮件 disabled 时不把通知标成已发出；测试队列的 SentAt 仍为 0 |
| `PaymentConfig.epay.*` | gatewayForMethod/签名收银台/原商户凭据快照回调 | 已有后台表单；启停、支付宝/微信选择、URL、商户 ID、密钥真正生效；密钥空值保留原值 |
| `PaymentConfig.bepusdt.trade_types` | 三链方法列表、实际 create-transaction 请求、付款快照 | 已有后台表单；历史 trade_type 兼容。网站本身不持有钱包、不运行区块扫描器，后者由 Bepusdt 承担 |
| `Plan.*` | 价格与付款精确比对、权益天数/额度/设备/节点范围 | 已有后台表单；没有 stock/inventory/coupon 字段，不能声称支持库存/优惠码 |

所有配置文件读取经过 `internal/config/config.go:66 Read` 的 `DisallowUnknownFields` 和“只能一个 JSON 对象”校验；HTTP 普通 JSON 请求经过 `api.go:73 decode` 严格检查。未发现把未知配置普遍吞掉并成功返回的统一空壳。工单 status 和商业试用属于已定位的“字段存在但没有实际作用”例外，已修正。

## 四、仍然有限或尚未自动化的能力

1. **主动查单与财务自动对账未实现。** 易支付/USDT 依赖可信回调；未知创建结果告警后需要网关人工核实，没有自动查询交易、重放漏单、账单导入和差异修复任务。前端已告知人工核实，不能称为自动对账已完成。
2. **退款能力为整笔。** 易支付 V1/USDT 先外部退款再登记、Stripe 可调用整笔退款；无部分退款/跨币种换算退款/自动链上转账。外部退款登记明确检查身份并撤权益，是既定真实管理动作。
3. **部分运营配置仅存在私有文件入口。** SMTP、告警收件人、法律政策、数据库、强制 MFA 策略、开售批准没有统一网页配置 API；网站支付网关表单不能替代这些条件。修改这些文件一般需要控制器重启。
4. **后台周期不是精确实时保证。** `RunMaintenance` 同一 goroutine 串行执行维护、单封邮件发送和节点探测；每节点探测最多 8 秒，节点较多/离线时会延迟下一轮。90 秒是离线判断阈值，不保证 90 秒以内发送通知；当前没有独立探测工作池。
5. **邮件吞吐有限。** `deliver` 每轮最多处理一封，失败指数退避；没有并行投递、退信处理、送达回执、管理员重试队列页面或工单新回复邮件通知。成功 SMTP DATA 不等于最终邮箱收件。
6. **容量上限与长期归档未完整产品化。** 代码有总订单 10,000、总工单 10,000、每工单 200 回复等上限；没有管理员归档/清理历史订单、支付事件、审计日志、过期挑战和旧租约的工作流。PostgreSQL 仍以完整状态为业务视图，不能按高并发大型计费平台承诺。
7. **审计为事务内业务留痕。** 当前记录成功提交的关键动作、实际操作者和安全差异；没有登录来源 IP、失败登录事件全集、防篡改哈希链、外部 WORM 存储、审批工作流、导出与保留策略。旧日志不回填详细字段，不能凭新版 UI 推断旧变更前后值。
8. **未开发的业务产品能力。** 库存/销售名额、优惠券、佣金返利、发票、自动扣费续订、套餐按比例升级差价、退款审批、多商户路由等，在当前模型、接口和后台都没有完整实现；它们不是已存在却无效果的按钮，也不属于本次少量修复就能补齐的功能。
9. **后台发布与真实运营依赖分开。** 下载地址只登记 URL/hash/version；节点安装需要用户在服务器执行命令；服务开售需要网关、钱包、邮件、DNS、有效节点和经营声明。本轮没有替用户完成外部商户真实结算或邮件实收验收。

## 五、遗留代码/字段

| 位置 | 检查结果 | 判定 |
|---|---|---|
| `commercial_model.go PaymentEvent.BodyHash` | 定义了字段，但检索不到赋值/消费路径 | 保留字段尚未使用；不能声称已保存通知原文哈希。当前验签/幂等依赖其他实际字段，不影响已实现机制 |
| `billing.go usage()` | 仓库内检索只有函数定义 | 遗留辅助函数，当前账单/租约不经过它；不是自动计量入口 |
| `billing.go consumeEntitlements()` | 仓库内检索只有定义 | 遗留旧扣量路径；当前真实扣量由 `reconcileLease` 更新权益 Used，不能因为此函数无调用就判断流量没有扣费 |
| `billing.go billingNow` | 定义但无实际读者 | 遗留时钟变量，不是可调周期引擎；生产代码使用实际 Unix 时间 |
| `operations.go` 原请求 `Status` | 接受 JSON 但从不读取 | 已移除并测试，避免静默忽略 |

没有为“清理看起来没用的代码”而改变计费路径；是否删除上述无调用兼容遗留项可另作纯整理，不能把删除当成补全未实现业务。

## 六、详细审计的安全边界与验收

新增 Audit JSON 字段：`actor_name`、`subject_name`、`summary`、`changes:[{field,label,before,after}]`。原 `time/actor/action/subject` 保留，旧历史没有新增字段也能读取。允许名单按账户、节点、套餐、订单、付款、网站设置、支付设置、工单、安装邀请、告警、租约逐类构造，未使用反射序列化整个秘密对象。

记录价格、节点范围、角色、封禁、启用、状态等真实前后差异。口令/口令哈希、token、私钥、MFA 密钥/恢复码、网关 secret、CA/ECH 原始材料、邮件/工单正文不进入日志。网关与下载地址只显示协议和主机；路径、URL 用户信息、查询、fragment 不记录。公告/发布说明只显示有无与长度。密钥改变只显示“已配置（已更新）”；用于差异判断的内部指纹不落入 Audit。退款自由文本理由、对账自由文本依据不复制到日志，防止误粘贴敏感内容；保留对应明确动作和对象。

新增测试位于 `internal/control/audit_details_test.go`：

- `TestAuditDetailsAPIChangesPersistAndRefundActor`：真实 API 改价/设备数差异、实际管理员退款和重启保存。
- `TestAuditWhitelistRedactsSecretsAndBodies`：敏感标记、URL 路径/查询、正文均不出现；安全配置变化仍被记录。
- `TestAuditLegacyHistoryDeletionAndRollback`：旧记录原样、删除前对象名称、失败事务不留成功日志。
- `TestAuditGatewayEventsUseSystemActor`：渠道到账/退款不被记成购买者管理动作。
- `TestPostgresAuditDetailsPersist`：同样的字段和隐私约束在独立 PostgreSQL schema 中落盘；需要测试 DSN，未设置时明确 skip。

本轮运维测试位于 `operations_audit_test.go`：备份缺失/损坏/过期/同秒失败/未来时间、去重、恢复、邮件未配置与测试邮件不伪装实发、商业关闭不报警、工单无效参数 400 与真实关闭成功。

本地本轮后端测试日志保存在 `.local/backend-audit-v0.5.5/`。新增详细审计专项连同退款/运维回归已通过；数据库相关用例须以最终部署验收的实际运行结果为准。历史 v0.5.5 实际支付网关/存储验收见 `admin-payments-gateway-v0.5.5.json`；该报告明确 `mocked_payment_gateways:true`、`real_charge:false`。不存在“测试通过所以真实收款已开通”的结论。
