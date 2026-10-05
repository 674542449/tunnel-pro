# 工作空间网页功能逐项核对（v0.5.5 基线）

审计日期：2026-10-03（北京时间）。范围为 `internal/control/web` 的用户、管理员、客服、财务可见页面、按钮、字段和计数，沿着真实 API 核对到权限、状态持久化或节点生效链。未审计第三方依赖，未打开 GUI 浏览器，未发起真实支付。本文首先记录已发布 v0.5.5 的现状，再明确标记计划随 v0.5.6 发布的源码修正；“源码已修正”不等于已经部署，部署结果以本轮最终发布验收为准。

没有发现用随机数或硬编码业务统计伪装实际订单、用户、节点和流量的情况。发现的主要问题是：退款数据已保存但页面缺明细，测试邮箱入口错误地跟随支付模式，商业模式提供了无法生效的试用时长编辑框。三项均有隔离真实服务的失败复现，随后做了源码修正。另有确实缺少网页入口的服务器配置、诊断及管理能力，见末节。

## 判定方法和证据

| 标记 | 含义 |
| --- | --- |
| 实现 | 可见动作有接口或明确的本地行为，能追踪到真实数据或副作用。不能仅据此认定所有异常情况都通过验收。 |
| 依赖配置 | 代码与接口存在，依赖商户、SMTP、DNS、证书、节点服务或服务器配置。缺少真实密钥不算占位实现。 |
| 缺入口 | 数据或底层能力已经存在，但网页没有查看/配置/处理入口。 |
| 占位 | 页面出现预留文案或装饰，但没有承诺执行的业务动作；必须说明它的性质。 |
| 缺陷 | UI 提供的行为与实际结果不符，并有代码路径或动态复现证据。 |

源码位置缩写：

- **A**：[app.js](C:/Users/tanzh/Desktop/tunnelX/internal/control/web/app.js)，常规工作空间、表单和鉴权。
- **C**：[commerce.js](C:/Users/tanzh/Desktop/tunnelX/internal/control/web/commerce.js)，交易、账单、安全和运维。
- **H**：[index.html](C:/Users/tanzh/Desktop/tunnelX/internal/control/web/index.html)，固定入口与 DOM。

表内 `A:162` 等表示上述文件的行号（按本次核对时源码，后续加入审计详情可能使行号变化）。API 均相对 `/api/`；标明 GET/POST，避免把读取数据和修改数据混为一谈。

| 证据 | 已核验材料 | 实际证明范围 |
| --- | --- | --- |
| E0 | 前后端源码逐项阅读 | 路由存在、调用条件、权限和落库链；未单独动态测到的条目明确写“静态”。 |
| E1 | [常规后台 DOM 验收](C:/Users/tanzh/Desktop/tunnelX/docs/admin-dom-acceptance-v0.5.5.json) | 23 个真实 API / DOM 场景，包括节点配置包、表单保存、搜索分页、试用和会话失效；无视觉渲染。 |
| E2 | [商业页面 DOM 验收](C:/Users/tanzh/Desktop/tunnelX/docs/admin-commerce-dom-v0.5.5.json) | 21 个场景，包括网关配置、SVG、链选择、邀请码、测试购买、退款、工单和角色。 |
| E3 | [用户完整旅程](C:/Users/tanzh/Desktop/tunnelX/docs/buyer-journey-v0.5.5.json) | 14 个实际 served JS + API 场景，包括首购、续费、加购、退款、工单、找回及跨用户隔离。 |
| E4 | [管理员完整旅程](C:/Users/tanzh/Desktop/tunnelX/docs/admin-journey-v0.5.5.json) | 9 组管理流程，包括 13 个管理页、节点生命周期、角色权限以及服务重启后的持久化。 |
| E5 | [强制 MFA 验收](C:/Users/tanzh/Desktop/tunnelX/docs/admin-mfa-dom-v0.5.5.json) | 8 个场景，真实 served JS、绑定、解锁、恢复码登录、防重放和旧会话撤销。 |
| E6 | [支付与数据库验收](C:/Users/tanzh/Desktop/tunnelX/docs/admin-payments-gateway-v0.5.5.json) | 32 个 Go 测试；网关使用模拟服务，数据库相关测试使用隔离 PostgreSQL schema，其他场景使用隔离文件存储。 |
| E7 | [本次缺失功能基线复现](C:/Users/tanzh/Desktop/tunnelX/docs/workspace-feature-baseline-v0.5.5.json) | 发布版 v0.5.5 的 5 个失败检查：用户/管理员/财务退款明细、商业试用输入、测试邮件入口；失败符合所列缺陷。 |
| E8 | [本次修复源码回归](C:/Users/tanzh/Desktop/tunnelX/docs/workspace-feature-fixed-source-v0.5.5.json) | 当前修复源码临时构建的 6 个 DOM/API 检查均通过。程序为隔离本地控制服务，未对线上作这些测试写入。 |
| E9 | [退款、详细审计及节点诊断回归](C:/Users/tanzh/Desktop/tunnelX/docs/workspace-feature-dom-v0.5.6.json) | v0.5.6 修复源码 9 项真实服务/DOM 检查通过，包括实际套餐价格 1.00→2.00 CNY 的审计差异；节点探测值为显式 fixture 数据，心跳走真实认证 API，未假称做了公网测速。旧日志兼容使用组件 fixture。 |

## 共用入口、登录和账号安全

| 功能/字段 | UI 位置 | API / 真实行为 | 持久化或生效链 | 证据 | 结论 |
| --- | --- | --- | --- | --- | --- |
| 访客套餐及服务说明 | C:48 `publicShop` | GET `public/commerce` | 只展示 State.Plans 中启用项、服务配置中的政策、发布信息 | E2、E3 | 实现；政策正文依赖配置 |
| 登录邮箱、密码、验证码/恢复码 | H:55；A:103 | POST `login`，GET `me` | 密码哈希校验、MFA 防重放、服务端 Session、HttpOnly Cookie；前端持有 CSRF | E1、E5 | 实现 |
| 注册及邀请码 | H:61；A:103；C:49 | POST `register` | 校验注册开关、一次性邀请、邮箱和密码；写 User、Session、邮箱验证 Challenge 和加密 Outbox | E2、E3 | 实现；真实投递依赖 SMTP |
| 退出登录 | A:105 | POST `logout` | 删除当前服务端会话并清 Cookie；清私有 DOM，恢复公开目录 | E3 | 实现 |
| 找回密码 | C:47、49 | POST `security/forgot`、`security/reset` | 生成限时一次性 Challenge；更新哈希，撤销会话/节点凭据及旧找回挑战 | E3、E6 | 实现；SMTP 依赖配置 |
| 邮箱验证与重新发送 | C:36、49 | POST `security/verify`、`security/resend` | 单次消费 Challenge、保存 EmailVerifiedAt；未验证账号不能创建商业订单和领取配置 | E2、E3、E6 | 实现 |
| 安全状态标识 | C:33 | GET `security/status` | MFA/邮箱状态和强制策略来自账户及当前 Session，不是本地假状态 | E5 | 实现 |
| 修改本人密码 | C:37 | POST `security/password` | 校验旧密码及必要 MFA；更新哈希、撤销旧会话和节点凭据 | E2、E6 | 实现 |
| 开启二次验证 | C:38 | POST `security/mfa/begin`、`security/mfa/confirm` | 加密密钥、限时绑定挑战、TOTP 检查、一次性恢复码哈希、会话升级和撤销其他会话 | E5 | 实现；手工输入密钥，未提供二维码扫描 |
| 关闭二次验证 | C:39 | POST `security/mfa/disable` | 非强制账号校验密码+动态码/恢复码，清密钥并撤销会话；强制工作账号不显示且服务端拒绝 | E5、E6 | 实现 |
| 已开 MFA 的重新登录/返回管理后台 | C:35、36；A:93-102 | `logout` / `login` / `me` 和本地导航 | 不绕过服务端 MFA；管理员、财务、客服按角色返回对应页面 | E5 | 实现 |
| 首次安全链接与同页链接 | C:49-51 | `#invite`、`#verify`、`#reset`、`#security` | 等身份初始化结束再处理；令牌使用后从地址栏移除；同页 hashchange 也处理 | E3 | 实现；v0.5.5 已修正找回弹窗竞态 |
| 导航与快捷入口 | A:97-102、108-125 | 根据 `me.role` 构造入口；调用对应列表 GET | 服务端再次做角色/MFA 校验；隐藏按钮不是唯一保护 | E1、E4、E5 | 实现 |
| 搜索、分页、取消、保存、错误提示 | A:28-59 | 浏览器本地过滤真实 API 集合；保存调用各业务 POST | 防重复提交，接口失败保留表单；异步旧渲染丢弃；搜索不是服务端全文索引 | E1、E3 | 实现 |
| 15/30/10 秒自动刷新 | A:177-178；C:52 | 重读真实 API | 账号/订单/账单/付款/节点/告警按页刷新；弹窗、保存和输入焦点期间有保护 | E1、E3 | 实现；没有推送通知协议 |

## 普通用户购物、权益和售后

| 功能/字段 | UI 位置 | API / 真实行为 | 持久化或生效链 | 证据 | 结论 |
| --- | --- | --- | --- | --- | --- |
| 我的账号状态、到期和用量 | A:108-125 | GET `me` | `accountForNode` 由当前权益、消费及测试范围计算；上传/下载不是随机数 | E1、E3、E6 | 实现 |
| 流量额度与进度 | A:116-118 | `me.user.upload/download/traffic_limit` | 商业计费走 Entitlements、Leases 及节点上报；非商业使用 User 额度 | E6 的跨节点额度/租约测试 | 实现；页面不展示每个设备的明细 |
| 设备数 | A:119 | `me.user.device_limit` | 商业模式由租约做跨节点设备限制；非商业按节点限制 | E6 | 实现 |
| 免费试用激活 | A:120 | POST `trial` | 非商业模式写一次性 TrialUsed 和有效期/1 GiB；商业模式服务端明确不允许 | E1 | 实现（非商业）；商业设置旧入口有缺陷，见修复项 |
| 套餐金额、币种、周期、流量、设备数 | A:150-155；C:48 | GET `plans` / `public/commerce` | 来源为真实已启用 Plan，支持订阅周期或当前周期流量加购 | E2、E3 | 实现 |
| 创建订单 | A:151-154 | POST `orders` | 验证邮箱、套餐、测试邀请、节点范围、加购资格；保存不可变套餐快照和到期时间 | E3、E4、E6 | 实现；没有购物车 |
| 取消订单 | A:156-161 | POST `orders/:id/cancel` | 验证归属和待付款状态，落库 cancelled；不能取消已收款单 | E1、E3 | 实现 |
| 模拟付款 | C:6、16 | POST `orders/:id/checkout`、`payments/:id/test-confirm` | 明确确认后生成测试 Payment/Entitlement；只用于测试节点，不真实扣款 | E2、E3 | 实现，明确标记测试 |
| 支付宝/微信选择及 SVG | C:12-16 | checkout `method=alipay/wxpay` | 签名易支付 V1 下单；付款快照保留原网关凭据；回调签名、金额、订单检查后开通 | E2、E6 | 实现；真实商户配置/开售条件依赖配置 |
| USDT 三链选择及 SVG | C:7-16 | checkout `usdt_trc20/usdt_bep20/usdt_polygon` | bepusdt 下单 trade_type 分别为 usdt.trc20 / usdt.bep20 / usdt.polygon；订单锁定网络 | E2、E6 | 实现；真实网关钱包与余额依赖外部配置 |
| 支付结果/到期/退款状态 | A:156-161；C:42 | GET `orders`、`billing` | 状态来自订单和付款；浏览器返回网站不能直接开通；回调幂等落库 | E3、E6 | 实现 |
| 提前续费 | 同创建订单/付款入口 | 原套餐再次创建并付款 | 新周期排在现有周期末尾；不会提前消费未来周期额度 | E3、E6 | 实现；这是手动续费，不是自动扣款订阅 |
| 流量加购 | A:151；C:42 | 创建 kind=traffic 的订单及付款 | 沿用当前有效且匹配节点范围的周期，单独记录额度；无基础周期时拒绝 | E3、E4、E6 | 实现 |
| 我的权益周期、状态、使用量 | C:42 | GET `billing` | 仅当前用户 Entitlements，显示待生效/生效/到期/退款撤销 | E3、E6 | 实现 |
| 我的付款记录 | C:42 | GET `billing` | 仅归属本人订单的 Payment.Public，移除 Gateway 私有快照 | E3、E6 | 实现 |
| 我的退款原因/流水/金额/状态 | C:29 `refundLedger`、C:42 | GET `billing` 的 refunds | 真实 Refund，按本人付款归属筛选；新增明细表使用 textContent 展示内容 | E7 失败→E8 通过 | 基线缺入口；本次源码已补齐 |
| 可用节点列表 | A:143-149 | GET `nodes` | 列出已启用且非待安装节点；不按个人套餐隐藏所有未授权节点，配置领取时再检查具体权益 | E1；profile.go 静态 | 实现；“可见”不表示该账户必有使用权 |
| 下载节点配置 ZIP | A:146；A:40 | GET `nodes/:id/profile.zip` | 校验邮箱、节点和有效权益，生成包含本账户 Token/CA/client.json 的真实 ZIP | E1；profile.go:11 | 实现 |
| 下载 Windows 客户端 | A:107、124；C:48 | 配置的 HTTPS release.url | 外部文件链接；后台校验 URL/SHA256；网页不自动安装或替换客户端 | E1、E2 | 实现；文件可用性依赖发布地址 |
| 提交售后工单 | C:44 | POST `tickets` | 校验内容与数量，写 Ticket/Reply/时间/审计；用户看自己的工单 | E2、E3 | 实现 |
| 查看/回复工单 | C:44 | GET `tickets`、POST `tickets/:id/reply` | 写新回复、状态回 open；消息用纯文本展示 | E3、E6 | 实现 |
| 关闭及重新打开工单 | C:44 | POST `tickets/:id/close` / `reply` | 关闭写 closed；再次回复会重新 open | E3 | 实现；重开复用回复操作 |

## 管理员的用户、节点、套餐、订单和设置

| 功能/字段 | UI 位置 | API / 真实行为 | 持久化或生效链 | 证据 | 结论 |
| --- | --- | --- | --- | --- | --- |
| 总览用户/节点/在线/订单计数 | A:112 | GET `admin/summary` | State 集合长度及 90 秒心跳计算；不是财务收入统计 | E1、E4；api.go:519 | 实现；在线仅指近期心跳 |
| 用户列表、状态和额度 | A:136-142 | GET `admin/users` | 用真实账户和权益投影；邮箱、角色、封禁、到期、额度来自存储 | E1、E4 | 实现 |
| 封禁/解除封禁 | A:138 | POST `admin/users/:id/update` | 写 Disabled，后续鉴权/节点授权检查失效；禁止直接封禁管理员 | E4 | 实现 |
| 角色管理 | A:139 | POST `admin/users/:id/role` | 允许 user/support/finance/admin；禁止改本人和移除最后管理员；撤销旧会话 | E2、E4 | 实现 |
| 管理员重置密码 | A:140 | POST `admin/users/:id/password` | 新哈希、新节点 Token、旧会话/找回挑战撤销；本人操作后回登录页 | E1、E6 | 实现 |
| 撤销旧节点凭据 | A:141 | POST `admin/users/:id/rotate` | 写新 TunnelToken；节点同步后旧凭据失效，客户端需要重新连接 | E0 api.go:793，集成授权链 | 实现；不是删除账户 |
| 管理员手工续期 | A:138（非商业） | POST `admin/users/:id/renew` | 修改有效期/额度/设备数并审计；商业模式前后端都禁止绕过订单计费 | E1、E6 | 实现（非商业） |
| 一键节点添加的名称/地区/IP/端口/域名/范围 | A:79-84 | POST `admin/nodes/setup` | 创建 pending Node、限时一次性安装令牌、测试/正式范围；返回真实 shell 命令 | E1、E4；node_setup.go:110 | 实现；服务器执行、DNS、网络依赖外部环境 |
| 安装命令复制 | A:73-78 | Clipboard API；失败则选中文本供 Ctrl+C | 复制真实生成命令；无自动远程执行假象 | E1 | 实现 |
| 重新生成安装命令 | A:147 | POST `admin/nodes/:id/install` | 新限时令牌撤销旧令牌；setup/config/file/complete 端到端状态机 | E1、E4 | 实现 |
| 手动添加/编辑节点 TLS/ECH/CA/IP/端口/状态 | A:85-92 | GET/POST `admin/nodes[/id]` | 校验配置，写 Node；更改后供后续客户端下载/连接使用，保留 AgentKey 与范围 | E1、E4 | 实现；网页不会自动改远端服务器的监听端口或证书 |
| 测试节点/正式节点范围 | A:83、90-92、145 | `test_only` | 创建时落库；模拟付款强制测试范围；编辑既有节点保留原范围防混用 | E2、E4、E6 | 实现 |
| 节点启用/停用 | A:147 | POST `admin/nodes/:id/enabled` | 更新 Enabled，控制下发和后续连接权限；不等同远端 systemctl stop | E1、E4 | 实现 |
| 删除节点 | A:72、147 | POST `admin/nodes/:id/delete` | 名称确认、停用/离线与引用保护，移除节点；保留必要历史 | E1、E4、E6 | 实现 |
| 下载节点接入配置 | A:147 | GET `admin/nodes/:id/agent-config` | 返回 API URL/node_id/AgentKey/state_file，生成本地 agent.json | E4 对凭据保持的检查；E0 | 实现 |
| 节点状态/活动连接 | A:145 | GET `admin/nodes` | Pending/安装错误/LastSeen/Active 来源真实上报；没有伪造速度测试 | E1、E4 | 实现；Active 是节点报告快照 |
| 节点 H2/ECH 外部探测详情 | A `nodeProbeStatus`、节点表 | API 返回 last_probe/probe_ok/probe_latency_ms | operations.go:354 周期真实探测并写 Node、失败告警；本次在管理员节点表加入独立探测列，含状态、时间和连接检查耗时 | E0、E9 | 基线缺入口；本次源码已补，普通用户表不增加管理员诊断列 |
| 新增/编辑套餐全部字段 | A:62-71 | POST `admin/plans[/id]` | 名称、天数、价格分币、币种、流量字节、设备数、类型、状态、NodeIDs 落库；旧订单快照不变 | E1、E2、E4、E6 | 实现 |
| 套餐节点范围 | A:70-71 | 同套餐保存接口 | 全部/指定节点显式选择，禁止空选与无效节点；当前/历史引用有保护 | E2、E4、E6 | 实现 |
| 启停及删除套餐 | A:153 | POST `admin/plans/:id`、`/delete` | 先停用再名称确认删除；历史订单仍保留快照 | E1、E4 | 实现 |
| 管理订单与用户对应 | A:156-161 | GET `admin/orders`、`admin/users` | 用户 ID 映射邮箱、真实订单状态/快照；可取消待付款订单 | E1、E4 | 实现 |
| 核实并开通 | A:160（非商业） | POST `admin/orders/:id/fulfil` | 旧人工模式下写已付状态和权益；商业模式明确禁用，使用回调或模拟付款 | E1，api.go:803 | 实现（非商业） |
| 注册开关 | A:163-165 | GET/POST `admin/settings` | Runtime AccessSettings 落库，注册端点立即读取；服务重启后保留 | E1、E4 | 实现 |
| 商业模式试用时长 | A:165 | 旧 UI 可写 trial_hours，但 accessSettings 固定返回 0 | 模式本来不支持免费试用，编辑框却假装可设置 | E7 失败→E8 通过 | 基线缺陷；本次禁用字段、解释原因、固定提交 0 |
| 公告 | A:166-172 | POST `admin/settings` | State.Announcement 落库，用户总览显示纯文本，保留其他设置 | E1、E4 | 实现 |
| 发布版本/HTTPS URL/SHA256/更新说明 | A:168-172 | POST `admin/settings`，GET `release` | 校验并保存 Release；前台生成 HTTPS 下载链接和校验值；手动更新 | E1、E2 | 实现；不是后台上传程序包功能 |
| 操作审计 | A `auditIdentity/auditOperation/auditChanges`、审计页 | GET `admin/audit` | 基线仅 Time/Actor/Action/Subject；本次增加可读操作者/对象名、中文摘要、安全字段白名单的前后差异、可展开表格，保留原 ID/动作码 | E1、E4、E9；audit.go | 基线过简；本次补详细审计，旧日志明确显示未保存差异，不捏造历史值 |

## 付款、退款、运维及工作账号

| 功能/字段 | UI 位置 | API / 真实行为 | 持久化或生效链 | 证据 | 结论 |
| --- | --- | --- | --- | --- | --- |
| 易支付 URL/商户号/密钥/支付宝微信开关 | C:17-28、31 | GET/POST `admin/payment-settings` | URL 验证、密钥 AES-GCM 密封、runtime 配置持久化；留空保留密钥；旧订单保留原凭据 | E2、E6 | 实现；商户资料依赖用户配置 |
| bepusdt URL/Token/三链开关 | C:17-28、31 | 同上 | 网络白名单、至少一链、网关密封；用于实际下单 trade_type | E2、E6 | 实现；钱包在 bepusdt 中管理 |
| 支付模式开关 | C:18 | 同上 | disabled/test/gateways/既有 stripe；只有可用配置和开售条件齐全才允许正式 checkout | E2、E6 | 实现 |
| 网关已保存密钥状态 | C:31 | payment-settings 返回 key_configured | 不回显密钥；不是假开关 | E2、E6 | 实现 |
| 清除已保存商户密钥 | C:17-28 未提供 | 后端 gatewayInput.ClearKey / `clear_key` 已实现 | 网页只提供留空保留/输入新密钥；关闭渠道不等于删除旧密钥 | E0 payment_gateways.go:134/141 | 缺入口；可先关闭渠道，清密钥须受控 API |
| 支付宝/微信/三链图标 | C:7-15、31；icons.svg | 原生 SVG symbol/use，无业务 API | 图标和 method/network 一一对应；纯标识不承担付款执行 | E2 | 实现（视觉标识，非支付替代品） |
| 回调地址展示 | C:31 | GET `admin/payment-settings` | 由 PublicURL 生成，创建付款自动提交；webhook 验签/金额/订单与幂等落库 | E6 | 实现；公网路由/HTTPS 依赖部署 |
| 付款与退款管理列表 | C:43 | GET `admin/payments`、新增 `admin/refunds` | 管理员/财务可查，服务端权限限制；私有网关快照不对前端返回 | E2、E6、E8 | 实现 |
| 模拟退款 | C:43 | POST `admin/payments/:id/refund` | 测试 Payment、Refund 状态及对应 Entitlement 撤销，后续周期调整 | E3、E4、E6 | 实现，不转账 |
| Stripe 整笔退款 | C:43 | 同上，后端 Stripe refunds API | 保留 pending Refund、幂等外部退款、回调确认后撤销权益 | E6 的签名/状态测试，E0 | 实现；未用真实 Stripe 商户扣款/退款验收 |
| 易支付/USDT 已完成退款登记 | C:43 | POST `admin/payments/:id/manual-refund` | 校验原因/流水后落库退款并撤销权益；网页明确先在渠道完成退款 | E6 | 实现（登记），不是自动钱包转账接口 |
| 管理员/财务查看退款理由、流水、金额、状态 | C:29、43 新增 | GET `admin/refunds` | 明细来自持久化 Refund，关联付款币种，纯文本显示，财务仍不能改网关 | E7 失败→E8 通过 | 基线缺入口；本次已补 |
| 客服工单台 | C:44；A:100 | GET/POST `admin/tickets`、`/:id/reply/close` | 客服可回工单和看告警，不可管理网关、订单权益或其他账户 | E4 | 实现 |
| 财务工单只读 | C:44 | GET `admin/tickets` | 只读通知窗，无回复/关闭按钮；后端同样禁止写 | E4 | 实现 |
| 告警列表、首次/恢复时间和确认 | C:45 | GET `admin/incidents`、POST `/:id/ack` | 节点离线、网络探测、邮件失败、租约对账等真实事件写入；确认不代表恢复 | E6，operations.go:159/176 | 实现 |
| 预留流量对账 | C:32 | GET `admin/leases`、POST `/:id/reconcile` | 管理员核实累计字节，限制不小于已用且不大于预算；有效租约拒绝释放，保留依据 | E6 | 实现；只在有待对账记录时出现 |
| 存储类型与邮件队列计数 | C:45 | GET `admin/operations` | Store.Backend/Healthy、未发送 Outbox 计数来自服务端 | E0 operations.go:64 | 实现；计数不说明所有邮件已经成功投递 |
| 正式开售检查清单 | C:45 | GET `admin/readiness` | 检查 DB、邮件模式、MFA、商户、政策、商业节点、批准、近期备份、告警收件人；checkout再次强制检查 | E2、E6 | 实现；部分检查是“配置满足”，不代表实测商户/SMTP业务成功 |
| 生成试运营邀请 | C:45 | POST `admin/beta/invites` | 保存令牌哈希、到期与使用状态；生成真实一次性链接 | E2、E3 | 实现 |
| 邀请清单/撤销/使用者追踪页面 | C:45 只有生成按钮 | 无公开管理 list/revoke 路由 | Invites 已持久化，但没有网页管理闭环 | E0 payments.go:384 | 缺入口；撤销端点也尚未实现 |
| 查看测试邮件 | C:45 | GET `admin/mail/test` | 仅 mail.mode=test 时解密查看，不向外发送；真实 SMTP 正文禁止读取 | E7 失败→E8 通过 | 基线条件错误；已改用新增 ops.mail_mode |
| 运维邮件告警投递 | C:45 展示告警，没有 SMTP 配置表单 | 后台 maintenance → Outbox → sendMail | TLS SMTP、失败退避、告警/恢复去重；不是前端通知模拟 | E6 | 依赖配置；网页缺配置入口 |

## 新修正的复现与结果

1. **退款明细缺入口**：创建两个不同账户的测试订单并完成付款/退款。`billing.refunds` / `admin/refunds` 中有原因和流水，原界面没有这些内容。已加入真实退款明细表。用户只取自己的账单结果；管理员和财务读取受权限保护的退款集合。恶意 HTML 形态的退款原因显示为文本，未生成 img 节点。证据 E7/E8。
2. **测试邮件入口使用错误条件**：保持 `mail.mode=test`，把支付模式改为 `gateways`（使用隔离假网关资料、不实际付款）。`admin/mail/test` 正常 200，但原运维页隐藏按钮。后端 operations 新增只读 mail_mode，前端按邮件模式显示。证据 E7/E8。
3. **商业试用输入无效**：商业模式设置表单允许输入 1–168 小时，后台保存后却固定展示 0，且 `/trial` 明确拒绝。没有擅自扩大免费授权；已将商业模式该字段禁用、解释获邀模拟购买方式，并固定提交 0。非商业试用仍保留原实现。证据 E7/E8。
4. **详细操作审计**：真实 API 连续把套餐价格从 1.00 CNY 改为 2.00 CNY，新审计返回并展示管理员邮箱、套餐名称、中文摘要和前后值；details 可展开。旧四字段日志仍可读，明确说明没有历史字段差异。密钥/密码等秘密不进入完整值差异，公告/正文类内容按安全策略记录设置状态或长度；并不声称所有文本都能全文回放。证据 E9 和对应后端审计测试。
5. **节点诊断只有接口没有界面**：新增管理员专用“最近外部探测”列，显示尚未探测/成功/失败、北京时间和耗时；把心跳在线与外部探测分开。回归用认证心跳 API 加显式失败探测 fixture，确认可以同时看到“心跳在线”和“外部探测失败”；普通用户表无诊断列。耗时说明是连接检查时间，不是下载测速。证据 E9。

## 目前没有网页入口的能力与未实现边界

| 项目 | 当前真实情况 | 应如何描述 |
| --- | --- | --- |
| SMTP 主机/端口/账号/发件人、告警收件人 | CommercialConfig/MailConfig 与真实发送代码存在；服务器配置文件管理，没有网页保存路由 | 依赖配置 + 缺网页入口，不是假发邮件 |
| 服务条款、隐私、退款政策 | 已可从配置传到公共页面并参与正式开售检查，缺网页编辑入口 | 配置缺失时是明确的待填写文案；不能声称政策已经完成 |
| 正式开售批准、数据库连接、安全密钥、强制 MFA 策略 | 属于服务器部署配置；网页只报告开售检查结果 | 缺网页配置入口；不应把解除检查当作补功能 |
| 加密备份、恢复和下载备份 | 有控制器命令、系统备份与恢复验收，网页只有最近备份是否满足条件 | 底层已实现，网页操作与历史明细未实现 |
| 节点外部探测历史/延迟 | 本次已补最近探测值的管理员显示，仍没有按时间保存的延迟历史曲线 | 最近结果已实现，尚不是完整网络质量看板 |
| 发票、税额、优惠券、购物车、自动周期扣款 | 没有可见按钮或业务接口；现有续费是再次创建并支付订单 | 未实现的新业务，不应称为已经做完，也不是隐藏的假按钮 |
| 部分退款 | 现有规则及 UI 都明确整笔退款 | 尚未实现；没有部分退款入口 |
| 钱包地址/汇率/链上转账执行 | 交由 bepusdt 网关管理，站点负责请求和回调 | 外部网关能力边界，不是站点内置钱包 |
| 批量管理/批量导出、邀请撤销、邮箱修改、恢复码再生成 | 当前网页没有相应动作；部分底层数据存在 | 没有实现完整产品入口，不应宣传已有 |
| 模态框中的“只显示一次恢复码”、安装命令只读窗 | 真实返回值临时展示；并未承诺后续可重新查看密钥 | 已实现的安全约束，不是占位 |
| network.svg 插画、SVG 品牌图标、空状态图、问候语 | 装饰和说明；不参与业务计数或权限判断 | 有意的界面内容，不应计作未实现业务 |

审计没有以“存在按钮”代替“功能验收”。有些能力只完成源码链核对，有些依赖外部配置，有些已有真实 API/数据库/DOM 自动化证据。上述证据不能代替真实商户到账、真实 SMTP 投递或每一种公网环境下的节点运行验收。
