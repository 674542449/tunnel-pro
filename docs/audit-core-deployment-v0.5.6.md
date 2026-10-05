# 传输内核、命令行、诊断和发布链逐项审计

审计基线为网站 v0.5.5、桌面 v0.5.0；本次网站修复发布为 v0.5.6，桌面修复发布为 v0.5.1。网络内核仍沿用 H2。以下逐项读取入口、调用链和真实效果；测试名称是工作空间现有可执行证据，不表示本轮重新做了多运营商测速。

## 内核与诊断功能矩阵

|编号|功能与入口|真实调用/效果|结论及证据|
|---|---|---|---|
|K01|配置读取 `internal/config/config.go:66`|JSON 解码拒绝未知字段，Resolve 将相对证书路径按配置目录解析|已实现；配置和集成测试覆盖错误字段、非法地址|
|K02|固定 IP 引导 `config.go:92`、`transport.go:71`|服务器 IP 必须为字面值；TLS 服务器名独立设置|已实现，不依赖客户端 DNS 查找节点|
|K03|TLS 加密 `config.go:215`|TLS 1.3 下限、X25519、证书链校验，Go 协商受支持的 AEAD|已实现，未自创密码算法；TestAuthAndCertificateFailures|
|K04|严格 ECH `config.go:215`|配置 ECHConfigList；拒绝握手失败降级|已实现；TestECHRejectionDoesNotDowngrade|
|K05|公开/内层证书 `config.go:239`|按握手域名选择证书，逐次握手从文件载入公开证书|已实现；公开证书文件更新可生效|
|K06|H2 池与轮换 `transport.go:79`|按需建连，限制池大小，老连接排空时保留已有业务流|已实现；TestH2PoolRotationPreservesActiveBusiness|
|K07|H2 失效处理 `transport.go:196,290`|超时后用 PING 区分慢目标和坏通道，仅对安全的新请求重试|已实现；SlowOrigin/DeadCarrier/CallerCancellation 三类回归|
|K08|TCP 隧道 `transport.go:381`、`server.go:89`|Extended CONNECT 后双向复制，半关闭，取消清理|已实现；TestTransportsAndPrivacy、TestStreamIsolationAndConcurrency|
|K09|SOCKS5 TCP `proxy.go:166`|解析地址、建立 H2 流、转发|已实现；TestSOCKSAndHTTPProxy|
|K10|普通 HTTP 代理 `proxy.go:305`|解析绝对地址、移除逐跳头、转发请求与响应|已实现；TestHTTPForwardKeepsWriteSideOpenUntilResponse 是 Steam 类下载回归|
|K11|SOCKS5 UDP `proxy.go:377`|本地 UDP ASSOCIATE 通过 H2 Capsule 保留报文边界|已实现；TestSOCKSUDPAssociate；不是原生 QUIC|
|K12|UDP Capsule `wire/udp.go:83`|有界报文、上下文检查、扩展 Capsule 跳过、队列满丢弃|已实现；TestMalformedCapsulesFailBoundedly / TestCapsuleExtensionAndContextIsolation|
|K13|鉴权前目标隔离 `server.go:89`|未授权请求返回公共 HTTPS 响应；鉴权后才解析目标|已实现；TestAuthGateBeforeTargetLookup|
|K14|非支持协议 `server.go:129`|对非 connect-udp 的扩展协议返回 501|明确拒绝不支持的请求，不是正在销售的空实现|
|K15|目标地址过滤 `server.go:228`|默认拒绝私网、回环、链路本地；白名单只用于显式配置的测试目标|已实现；地址过滤和 IPv6 集成测试|
|K16|目标 IP 偏好 `server.go:228`|只排序 DNS 已返回且通过过滤的地址|已实现；TestPreferredTargetIPsRemainDNSBoundAndFiltered；不是任意 DNS 注入|
|K17|托管账号撤销 `nodeagent/agent.go`、`server/access.go`|授权同步、账号禁用、token 轮换和租约撤销影响活动流|已实现；TestManagedH2TCPUDPAndLiveRevocation / TestCommercialStrictH2QuotaAndRefundRevocation|
|K18|本地控制页 `client/ui.go:43`|状态、探测、代理启用/恢复和退出均接到真实方法|已实现；TestControlAPIHostAndCSRF；只属于独立配置版|
|K19|连接检测 `client/ui.go:76`|TLS 与节点公共 HTTPS GET，检查 200|实现的是节点公共连通性检测，不能代表任意目标/账号额度/视频质量正常；页面文案已明确|
|K20|常驻日志 `diagnostics/log.go:56,92,189`|异步有界记录、落盘、轮换和清理|已实现；最多 16×16 MiB，满后淘汰旧文件；RotationRetention / ConcurrentShutdown 测试|
|K21|应用流关联 `client/diagnostics.go:50`|目标、进程归属、流阶段、方向结束、通道标识|已实现；错误字段分类，避免写完整私密错误文本；CorrelateTimeout 测试|
|K22|独立探测 `diagnostics.go:277`|定时探测不消耗业务统计；记录故障与恢复|已实现；TestHealthProbeRecoveryDoesNotChangeBusinessCounters|
|K23|历史日志分析 `tools/analyze-connection-logs.py`|读取 JSONL、归类故障/目标与时间|已有命令行工具；网站没有集中日志上传/检索系统|
|K24|Windows 进程识别 `processowner/owner_windows.go`|系统 TCP/UDP 表关联 PID|已实现；其他系统返回 unsupported_platform 是平台边界|
|K25|公开页面 `server.go:69`|PublicDir 文件服务或内置普通 HTTPS 页面|已实现；页面不是完整业务网站，也不能保证无法被识别|
|K26|拥塞控制|传输依赖操作系统 TCP，部署节点可配置 BBR|没有自定义拥塞算法，也没有保证不受运营商 QoS 的实现|
|K27|HTTP/3、QUIC|当前配置只允许 h2，运行代码没有 H3 传输入口|按用户要求移除；历史文档/报告不等于当前功能|
|K28|TUN / 全应用接管|当前入口为 SOCKS5、HTTP 和 Windows 系统代理|未实现；不遵循系统代理的应用和本地 DNS 不自动入隧道|
|K29|浏览器 TLS 指纹模拟|使用 Go TLS 实现，无浏览器指纹模板|未实现；不能称为 Chrome 指纹|
|K30|密钥与内层证书生成 `pki/pki.go:18,48`|随机 ECH 密钥、约一年 CA、约六个月内层叶证书|生成已实现；内层 CA 私钥未作为可持续签发工具保存，没有自动轮换和信任迁移|

## 命令、部署、发布矩阵

|编号|功能与入口|真实效果|结论/限制|
|---|---|---|---|
|D01|`cmd/tunnelx-admin/main.go`|生成节点配置、CA、内层证书和 ECH 密钥；拒绝覆盖已有 server.json|已实现；没有“自动续签内层证书”子命令|
|D02|`cmd/tunnelx-server/main.go`|检查 TLS/代理配置、启动服务器、可加载管理 agent|已实现；`-check`不是进程启动/外部连通性保证|
|D03|`cmd/tunnelx-client/main.go`|绑定代理端口、写日志、启动控制页、退出恢复代理|已实现；独立配置版与账号桌面版是不同入口|
|D04|`cmd/tunnelx-control/main.go`|初始化、配置检查、运行、导出、加密备份、新库恢复|已实现；`-init`默认非商业文件存储，不会创建 PostgreSQL/商户|
|D05|`cmd/tunnelx-check/main.go`|连通性、受控上传下载、UDP、并发、视频/测速报告|已实现测试工具；需要显式测试夹具或下载 URL|
|D06|`cmd/tunnelx-soak/main.go`|有截止时间的循环下载与 JSON 结果|已实现；不是自动运行多运营商长期监控|
|D07|`cmd/tunnelx-fixture/main.go`|受控回环 TCP/UDP/HTTP 测试服务|真实测试夹具，不是产品占位接口；不应暴露给公网|
|D08|节点一键安装 `control/setup/install-node.sh`|取限时清单、SHA256 校验、生成密钥、配置 Caddy/systemd、回报完成或失败|已实现；需要 DNS、端口、包源、root。删除后台记录不会远程卸载服务器|
|D09|主节点安装 `deploy/install.sh`|安装内核、证书、systemd 与续期同步定时器|已实现；原有主节点不能当成商业计费节点自动转换|
|D10|公开证书同步 `deploy/sync-certificate.sh`|验证域名、剩余期限、链和密钥匹配后原子替换证书对|已实现；主节点脚本假定 Let's Encrypt 的 Caddy 路径；其他源需人工配置。新节点安装器支持遍历 Caddy 证书源|
|D11|挂接管理 `deploy/attach-managed-node.sh`|检查 agent 配置后安装 systemd override 并重启节点|已实现；是服务器命令，不是网站一键远程执行|
|D12|后台安装 `deploy/install-control.sh`|安装控制器、节点分发文件、私有配置和服务|基线漏装备份任务，本次补齐脚本、service、timer及首份备份|
|D13|加密备份 `deploy/backup-control.py`|调用真实加密导出，写原子状态、保留 336 份|本次新增失败状态和同秒文件唯一性；测试覆盖成功/失败/恢复、密钥保留与滚动保留|
|D14|备份告警 `control/operations.go`|商业模式检查失败、缺失、损坏、未来时间及过期，记录恢复|本次补齐；控制器整体停机时仍需要外部监控|
|D15|离机备份|曾手动下载加密备份到本机|没有持续自动异地复制；手动下载不能代替该功能|
|D16|数据恢复|`-restore-new`检查新文件/空数据库，验证密文后恢复|已实现并有 PG 恢复测试；没有自动灾备切换|
|D17|控制器构建 `tools/build-control.ps1`|测试、交叉编译和打包所需节点二进制|已实现；Windows race 测试依赖合适的 C 工具链|
|D18|桌面构建 `tools/build-desktop.ps1`|vendor、H2 补丁、语法检查、Wails 构建|已实现；本次补显式 Python 路径及源码/发布版本一致性检查|
|D19|管理打包 `tools/package-control.py`|ZIP/源码/许可证、凭据排除与 SHA256 检查|基线默认版本错误且强制同版本桌面/缺文档，无法打包现版本；本次解耦、读取源码版本并用稳定部署说明|
|D20|客户端更新|网站保存版本、HTTPS 地址、SHA256；客户端打开下载页|手动更新已实现；自动下载校验安装、签名分发未实现|
|D21|生产部署工具|私有 `.local` 脚本提供当前服务器备份、原子替换、回滚和验收|运维脚本存在，但不是公共包自带完整集群部署系统；私密目录不进入公开源码包|
|D22|发布说明|README 仍主要描述 v0.4.x，历史 ADMIN-DESIGN-v0.5.0 说尚无支付宝微信|基线文档过时；本次更新主入口，历史文档保留版本语境|

## 不是产品空壳的搜索命中

`mock` 大多位于支付测试、DNS/TLS 夹具；`501 Not Implemented` 是拒绝未支持的协议扩展；`unsupported_platform` 是非 Windows 分支；HTML 的 `placeholder` 是输入提示；界面默认“暂时没有公告”为空数据状态。这些没有被计入未实现按钮。

## 本轮没有替代验证的事项

没有再次证明所有运营商路径、Netflix/Steam 的持续吞吐或长期抗干扰能力。没有执行真实扣款、真实链上退款，也没有在全新独立 Debian 主机重新安装全部 systemd 服务。证据区分源码追踪、隔离测试、历史验收和本次线上检查。
