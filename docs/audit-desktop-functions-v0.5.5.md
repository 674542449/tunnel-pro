# Windows 桌面功能逐项审计

审计日期：2026-10-03（Asia/Shanghai）。审计基线为网站 v0.5.5、已发布桌面 v0.5.0；本轮修复已进入桌面 v0.5.1 源代码，网站后续版本为 v0.5.6。本文记录源代码与无 GUI 验证结果，不代表旧版运行中的客户端已自动获得修复。版本发布与最终原生程序验收另见发布报告。

范围：`desktop/main.go`、`desktop/ui/*`、`internal/desktop`、`internal/systemproxy`、`internal/processowner`、`internal/config`，以及它们实际调用的客户端代理、控制 API、日志接口。第三方 `desktop/vendor` 与生成的 Wails bindings 不作为自有功能审计对象。本轮没有替换用户正在使用的程序、修改实际 Internet Settings 代理键或安装自启动任务。

## 结论与本轮修复

Windows 路径下，登录、节点配置获取、H2/ECH 连接、本地 HTTP/SOCKS 代理、系统代理设置、诊断落盘和托盘功能都能追踪到实际实现。不是只有按钮或固定成功返回。以下四项真实问题已补齐：

1. **会话过期后仍显示已登录。** 原 `Query`/`Connect`/`Probe` 将 HTTP 401 返回给前端，却保留本地 token；`Status.logged_in` 因 token 非空持续为 true，登录表单被隐藏。现在仅清理被当前请求明确以 401 拒绝的同一 token，关闭该会话的隧道，并将清理后的状态重新写入 DPAPI 文件。403（含 MFA）、429、500 与网络故障均不触发登出；迟到的旧 token 响应不会清理替换后的新会话。
2. **没有活动隧道时，遗留系统代理无法恢复且关闭返回成功。** 原 `disconnect()` 在 `mux == nil` 时直接返回 nil。本次为孤儿代理增加恢复路径，并放在 Wails 单实例锁建立后的启动回调、无 GUI 控制端口成功绑定后以及显式恢复/断开/退出操作中。恢复期间先绑定快照里的本机代理端口；其他程序仍在监听时拒绝恢复，保留快照。注册表已被外部程序改过时同样保留，禁止覆盖。界面显示原因及重试按钮，退出恢复失败不会静默消失。
3. **流量加购误显示为固定天数，套餐没有金额与币种。** 套餐按钮现在显示金额、币种；流量加购显示“当前有效周期”。订单新增“已付款待处理”的中文状态。
4. **网页购买/退款后，桌面刷新只更新节点。** 刷新现在按顺序重新读取节点、账号、套餐、订单，用户从网站返回后可得到最新权益和订单状态。

明确尚未提供的产品能力：开机自启动、启动后自动连接、全局 TUN/虚拟网卡接管、客户端内完整支付收银台、自动下载/校验/安装升级、日志一键导出、节点自动故障切换。现有代码没有把这些能力伪装成已完成入口。

## 功能矩阵

以下行号对应审计完成时源代码；调用链中的路径相对于工作区根目录。

| 用户入口 / 功能 | 调用链与关键位置 | 实际效果 | 验证 / 明确限制 |
|---|---|---|---|
| 启动与读取设置 | `desktop/main.go:97` → `config.Read`（`internal/config/config.go:66`）→ `desktop.New`（`internal/desktop/engine.go:60`） | 读取可执行文件同目录 `settings.json`，支持 `-config`、`-state-dir`；缺省本地端口为 1080/8088/9080；拒绝未知 JSON 字段与多对象配置 | Shell 无 GUI 编译通过。需要部署包提供有效配置；配置或 DPAPI 文件损坏时仍是启动失败，尚无设置修复向导 |
| 登录 | `desktop/ui/app.js:7` → `App.LoginSecure`（`desktop/main.go:44`）→ `Engine.LoginSecure`（`internal/desktop/engine.go:135`）→ `control.ClientRequest`（`internal/control/api.go:994`） | 真实 POST `/api/login`，发送邮箱/密码/验证码；成功保存会话 token，不保存明文密码 | 历史原生 v0.5.0 验收已覆盖安全登录；本轮真实本机 HTTP 服务验证会话拒绝处理 |
| 注册 / 邀请码 | `desktop/ui/index.html:3`、`desktop/ui/app.js:7` → `LoginSecure(..., true)` → `/api/register` | 使用相同登录状态保存路径；邀请码传给服务器，注册与试运营准入由服务端判断 | 注册按钮没有按后台公开注册开关自动隐藏；关闭注册时由服务器明确拒绝，不能绕过限制 |
| 二次验证登录 | `desktop/ui/index.html:3` → `LoginSecure` 的 `code` 参数 | 支持验证器代码和恢复码随登录提交 | 历史原生验收包含不填代码拒绝、恢复码成功、重复恢复码拒绝。绑定/关闭 MFA、找回密码等操作通过网站门户完成 |
| 本机凭据保护 | `Engine.saveLogin`（`internal/desktop/engine.go:100`）→ `protect`（`internal/desktop/protect_windows.go:10`）→ Windows DPAPI → `control.WriteFile`（`internal/control/store.go:23`） | token、设备 ID、邮箱与 API 地址存入 `state/auth.dpapi`；原子替换文件；Windows 当前用户加密 | `TestDPAPICredentialsRoundTripAndTamper` 本轮通过。凭据不可直接移到另一 Windows 用户/机器使用 |
| 过期会话恢复 | `Engine.request`（`internal/desktop/engine.go:172`）→ `control.RequestError`（`internal/control/model.go:355`）→ `disconnect` + `saveLogin` → UI `refresh`（`desktop/ui/app.js:4`） | 同一 token 的 401 清理内存与磁盘状态，并显示登录表单；旧账户内容清除 | 新增 query/connect/probe 三条入口测试、重启检查、迟到响应与非 401 反向测试；全部通过 |
| 退出账号 | `desktop/ui/app.js:7` → `App.Logout`（`desktop/main.go:48`）→ `Engine.Logout`（`internal/desktop/engine.go:336`） | 先断开隧道、恢复代理，再请求 `/api/logout`，清理本机会话 | 服务端登出请求失败时仍清理本地 token；断开时代理恢复失败会报错，避免误称已完整退出 |
| 保存管理 API 地址 | `desktop/ui/app.js:7` → `SetAPI`（`internal/desktop/engine.go:112`）→ `ValidateURL`（`internal/desktop/url.go:10`） | 必须先断开；要求 HTTPS（仅 loopback 调试允许 HTTP）；拒绝用户信息、查询串、片段、末尾斜线；保存后清理登录 | 保存目标是状态根目录 `settings.json`。自定义 `-config` 指向其他文件时，保存不会改写那个指定文件；默认发布目录不受此限制 |
| 账号 / 公告 / 用量 | `desktop/ui/app.js:5` → Query `/api/me` | 展示邮箱、账号可用状态、到期、累计流量与公告 | 本轮 DOM 验证刷新后的账号状态。后台运行时不会每秒重新取账户 API；1 秒轮询仅取本地连接状态 |
| 节点列表与选择 | `desktop/ui/app.js:6` → Query `/api/nodes` | 真实拉取当前账号可见节点；选择保存在页面内存，显示地区、在线状态、H2 | 选择节点不会隐式重连；已连接时需要先断开再连接。退出程序后不保留所选节点 |
| 刷新 | `desktop/ui/app.js:7` → `nodes()` → `account()` → `/api/nodes`、`/api/me`、`/api/plans`、`/api/orders` | 用户从网站购买、退款或管理员调整后，可同步查看最新节点、账户和订单 | 本轮 DOM 用调用序列和变更后的页面内容验收；以前只更新节点，本次修复 |
| 测延迟 | `desktop/ui/app.js:6` → `App.Probe`（`desktop/main.go:53`）→ `Engine.Probe`（`internal/desktop/engine.go:377`）→ `Mux.PublicGET`（`internal/client/transport.go:419`） | 获取授权配置与 CA，建立独立 H2/ECH 连接，要求响应 200 且 ECH 被接受，返回耗时毫秒 | 是 TLS/H2/HTTP 探测耗时，不是 ICMP ping；每次最多约 10 秒，不测速吞吐。过期登录处理本轮已覆盖 |
| 连接节点 | `desktop/ui/app.js:7` → `App.Connect`（`desktop/main.go:50`）→ `Engine.Connect`（`internal/desktop/engine.go:205`）→ `client.New`（`internal/client/transport.go:60`） | 真实获取 `/api/nodes/:id/profile`；写 CA 文件；强制 `h2` + `strict`；检查 TLS/ECH，再验证账号接入，最后启动本地代理 | 历史原生 v0.5.0 测试包含付费测试节点、严格 ECH、8 MiB 字节一致性。当前账号准入探测会尝试节点 IP 的 TCP 443；只开放自定义节点端口的手工节点存在额外可达性要求 |
| 本地 SOCKS5 / HTTP 代理 | `Engine.Connect` → `client.NewProxy` / `Proxy.Start`（`internal/client/proxy.go:33`、`:37`） | 绑定 loopback SOCKS 与 HTTP 监听，处理 HTTP CONNECT/普通 HTTP 与 SOCKS；实际业务通过 H2 核心 | 地址由 settings 配置，UI 仅展示、不编辑。端口占用会给出退出旧版程序的错误；没有 TUN 全局接管，不等于所有程序流量都自动代理 |
| 系统代理开关 | `desktop/ui/index.html:4` → `Connect(id, checkbox)` → `Manager.Enable`（`internal/systemproxy/proxy_windows.go:43`） | 保存 HKCU Internet Settings 原值，设置当前 HTTP 代理，暂停 PAC，并通知 WinINet 刷新 | 本轮使用隔离测试注册表键验证，未触碰用户真实代理。复选框控制本次连接，不是即时切换，也未持久保存勾选偏好 |
| 遗留代理自动恢复 / 手动恢复 | `App.startup`（`desktop/main.go:63`）或 headless 成功监听后 → `Engine.RecoverProxy`（`internal/desktop/engine.go:189`）→ `Manager.RestoreOrphan`（`internal/systemproxy/proxy_windows.go:104`） | 恢复前临时占住快照中的本机端口；有监听则保留他人代理；外部改写注册表时拒绝覆盖；状态与重试按钮可见 | 新增孤儿恢复、活跃监听、外部变更三个隔离注册表场景通过。`Engine.New` 不恢复代理，避免在 Wails 单实例锁之前改动系统设置 |
| 断开连接 | `desktop/ui/app.js:7` / 托盘 → `Disconnect`（`internal/desktop/engine.go:311`） | 恢复系统代理、取消诊断、关闭 HTTP/SOCKS 监听和活动连接、记录停止事件、释放日志 | 无活动 mux 时也执行安全孤儿恢复；恢复错误明确返回。不是空成功分支 |
| 连接状态、速度、时长 | 1 秒 UI interval（`desktop/ui/app.js:8`）→ `Engine.Status`（`internal/desktop/engine.go:347`） | 从真实字节计数计算上下行速率，展示连接时间、ECH 状态、本机日志统计 | “已连接”表示本地 mux 存在，不保证任意目标站点此刻可达；网络中断的详细判断仍在诊断日志中；没有自动选择备用节点 |
| 套餐 / 创建订单 | `desktop/ui/app.js:5` → `/api/plans` → `/api/orders` | 显示金额、币种与周期；点击创建真实待付订单；商业模式引导网站完成付款 | 流量加购显示当前有效周期，本轮 DOM 覆盖 USD/CNY 金额；桌面不包含易支付/USDT 收银台或退款操作 |
| 试用 | `desktop/ui/app.js:5`、`:7` → `/api/trial` | 按 API 返回的资格与试用小时数显示按钮，申请后重新读取账号 | 后端负责一次性与商业隔离校验；旧版原生平台验收已覆盖试用，不是只在界面上改状态 |
| 最近订单 | `desktop/ui/app.js:5` → `/api/orders` | 展示最近五笔订单状态；含已付款待处理、已退款、已取消等 | 不提供桌面内取消、退款、完整历史筛选；通过网站处理 |
| 购买、账单与账号安全 | `desktop/ui/app.js:10` → `App.OpenPortal`（`desktop/main.go:47`）→ `PortalURL`（`internal/desktop/engine.go:156`）→ Wails `BrowserOpenURL` | 调起系统浏览器打开配置的管理网站 | 不共享桌面 Bearer token 至浏览器，不是单点登录；网站可能需要独立登录 |
| 检查更新 | `desktop/ui/app.js:7` → `/api/release` | 真实读取发布版本、说明、下载 URL、SHA256，并与本地版本字符串比较 | **仅检查和展示。** 没有后台下载、哈希计算、签名验证、安装、回滚；不会自动替换当前程序 |
| 设置与诊断 | `Engine.Connect` → `diagnostics.New`（`internal/diagnostics/log.go:56`）+ `RunDiagnostics`（`internal/client/diagnostics.go:277`）→ `Status.logging` | JSONL 持续落盘；独立 H2 健康探测每 30 秒记录失败/恢复；展示已写、丢弃、写入错误数与目录 | 默认单文件 16 MiB、保留 16 个；不是无限留存。UI 没有日志浏览、导出/上传按钮，也没有邮件告警 |
| 连接所属进程 | HTTP ConnContext / SOCKS（`internal/client/proxy.go:49`、`:167`）→ `processowner.Lookup`（`internal/processowner/owner_windows.go:20`） | 用完整本机 TCP 四元组查进程 PID、可执行文件名、创建时间，进入诊断元数据 | IPv4/IPv6 本轮 Windows 测试通过；查不到时记录错误类型，不伪造进程；不记录命令行或完整程序路径 |
| 收起 / 窗口右上关闭 | `desktop/ui/app.js:7` → `App.Minimize`（`desktop/main.go:55`）；Wails `HideWindowOnClose`（`desktop/main.go:122`） | 隐藏窗口，连接继续运行；托盘可重新显示 | 不是退出。未用 GUI 自动化点击，此项为绑定与实现审阅、桌面模块编译验证 |
| 托盘显示 / 断开 / 退出 | `desktop/main.go:63` 的 systray 菜单循环 → Wails WindowShow / Engine.Disconnect / App.Quit | 真实系统托盘菜单；恢复失败时重新显示窗口供处理 | 自有调用链完整，系统托盘可视交互本轮未渲染 |
| 退出程序 | `desktop/ui/app.js:7` → `App.Quit`（`desktop/main.go:56`）→ Disconnect → Wails Quit → OnShutdown | 正常路径先恢复代理，再退出并释放日志；恢复失败显示错误、保留程序供重试 | 本轮 DOM 验证 Promise 错误可见且按钮恢复可用；操作系统强杀无法运行退出回调，依赖下次安全恢复 |
| 单实例 | Wails `SingleInstanceLock`（`desktop/main.go:122`）→ OnSecondInstanceLaunch → WindowShow | GUI 第二次启动显示已有窗口；本次恢复逻辑避免在实例锁前直接恢复代理 | 不把 headless 模式纳入 GUI 单实例锁；headless 通过 loopback 控制端口绑定冲突检查并要求测试隔离目录 |
| 无 GUI 验收模式 | `-headless` → `serveHeadless`（`desktop/main.go:132`） | 本机真实 JSON API：状态、登录、查询、连接、断开、代理恢复、退出；验证 Host、Origin 和随机 CSRF | 仅允许 literal loopback；不是远程管理接口。没有 `/probe`、`/set-api` 路由，相关能力属于 Wails 绑定 |
| 开机自启动 / 自动连接 | `Settings`（`internal/desktop/engine.go:25`）、`desktop/main.go` 和全部 UI 中没有对应字段、按钮或系统任务写入 | **未实现 / 未提供。** `startup(ctx)` 只是 Wails 启动回调，不是 Windows 开机自启 | 没有写 Run 注册表、Startup 快捷方式或计划任务；不能把启动回调算作自启能力 |
| HTTP/3 / 安卓 / iPhone | `desktop/main.go:1` Windows build tag；`config.Client.Validate`（`internal/config/config.go:92`）；连接强制 H2 | Windows 桌面只实现 H2；符合此前去掉 H3 的要求 | 非 Windows 的 `protect_other` 返回不支持，`systemproxy/proxy_other` 是平台降级桩，不是 Windows 路径的缺功能；未提供 Android/iOS 客户端 |

## 验证证据与边界

### 缺陷前后证据

新增 [engine_windows_test.go](C:/Users/tanzh/Desktop/tunnelX/internal/desktop/engine_windows_test.go) 首次运行时：

- Query、Connect、Probe 三条路径均出现 `401 must clear the current local session; logged_in=true`。
- 空 mux 的 Close 后仍是本机代理地址，出现 `orphaned system proxy was not restored`。
- 活跃其他监听和外部代理修改场景均从 Close 获得 nil，测试分别报告没有明确说明所有权/外部变更。

修复后这些测试全部通过，同时覆盖不误登出的 403/MFA、429、500、连接失败与替换会话保护。系统代理验证限定于新建的 `HKCU\Software\tunnelX-desktop-tests\<随机值>`；原有 systemproxy 测试限定于 `HKCU\Software\tunnelX-tests\<随机值>`，两者均清理自己的测试键。

### 本轮执行

1. `go test -tags=http2legacy ./internal/desktop ./internal/systemproxy ./internal/processowner ./internal/config -count=1 -v -timeout 90s`：通过。
2. 新增迟到响应保护后，再次运行 `go test -tags=http2legacy ./internal/desktop -count=1 -v -timeout 90s`：通过。
3. 在 desktop 模块执行 `go test -mod=mod -tags=http2legacy ./... -run '^$' -timeout 90s`：Windows 自有 Shell 编译通过，不启动窗口。
4. `node tools/test-desktop-ui.mjs`：六项 DOM 验收通过；[机器可读报告](C:/Users/tanzh/Desktop/tunnelX/docs/audit-desktop-ui-v0.5.5.json)。该测试用受控桥接对象验证 UI，实际 HTTP、DPAPI、注册表恢复由上述 Go 测试验证，不混称完整原生点击验收。

### 已有历史证据

- [v0.5.0 原生桌面验收](C:/Users/tanzh/Desktop/tunnelX/docs/admin-desktop-acceptance-v0.5.0.json)：真实原生 exe、无窗口渲染；安全登录、严格 ECH 测试节点、大文件字节一致性、DPAPI、MFA/恢复码、防重放、原系统代理保持及日志。
- [v0.4.0 平台验收](C:/Users/tanzh/Desktop/tunnelX/docs/acceptance-desktop-platform-v0.4.0.json)：历史注册、试用、HTTP/TCP/UDP、64 MiB 下载、封禁/解封与日志证据。只作为已有覆盖，不能替代本轮版本完整网络验收。

### 本轮没有声称完成的验收

没有渲染窗口、点击系统托盘、执行真实付款、安装自启动任务、接管用户当前代理或重启运行中的客户端。本轮审计与源码修复不会自行升级用户现有 v0.5.0 程序。更新下载安装、日志导出与自动故障切换均保留为明确未提供能力。

