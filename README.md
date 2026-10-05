# tunnelX：网站与 Windows 客户端统一发布

Windows 账号客户端、管理和购买网站，以及适用于 Ubuntu / Debian 的 Linux 节点。网站与 Windows EXE 共用根目录 `VERSION`，构建与打包必须通过一致性校验；网络内核版本独立。发布步骤见 [统一发布规范](docs/UNIFIED-RELEASE.md)。

管理地址：[tunnelX 控制台](https://test.xiaguamail.com/control/)。当前功能和限制请先看 [工作空间详细审计](docs/workspace-audit-v0.5.6.md)、[网站部署说明](docs/CONTROL-DEPLOYMENT.md) 和 [用户/管理员完整旅程](docs/user-admin-acceptance-v0.5.5.md)。历史文档按版本保留，其中“尚未接入支付宝/微信”等描述仅适用于当时版本。

## 账号客户端与网站

Windows 桌面支持账号登录、MFA/邀请码、节点列表和连接、系统代理、诊断记录以及网页购买入口。v0.6.0 重构为连接、线路、订阅和设置四个页面，补齐连接进度与取消、偏好记忆、节点入口健康状态、订单确认、下载更新入口和脱敏诊断导出。新用户数据存入 Windows 用户目录，旧便携数据保持兼容。详见 [Windows 使用说明](docs/WINDOWS-v0.6.16.md)。发布包不包含账号凭据，更新仍通过下载新包手动完成。

网站支持 PostgreSQL、套餐与订单快照、周期续费、流量加购、五种支付选项、退款、角色/MFA、节点限时安装、工单、告警和详细操作审计。支付宝/微信使用易支付；USDT 使用 bepusdt，网络为 TRC20、BEP20、Polygon。网关商户配置与真实结算、SMTP 投递和正式政策需要经营方完成；模拟购买不扣款。

本轮新增可查看的退款原因与流水、节点外部探测结果、安全的操作前后差异，以及备份失败和恢复告警。没有实现的能力与缺少配置的功能分别列在审计报告中，不以“有按钮”作为实现证据。

## 网络内核

内核只使用 HTTP/2；HTTP/3、QUIC、跨协议回退和 H3 探测已按要求移除。默认严格 ECH、TLS 1.3、四条 H2 连接、5 秒空闲检测、2 秒 PING 超时及 120 秒温和轮换。确认坏通道后，仅新建 CONNECT 可重试一次，不重放业务数据。

Windows 桌面客户端支持绕过大陆、全局系统代理和 TUN 三种模式，TUN 使用随包附带的 Wintun 与 tun2socks，需要管理员权限。系统代理模式只覆盖遵循该设置的应用，其余应用可显式配置 SOCKS5/HTTP。UDP 通过 H2 Capsule 传输，会受 TCP 队头阻塞影响。没有保证不受运营商 QoS 的能力。

## 独立配置版 Windows 客户端

1. 先退出旧版，再将 `dist/tunnelX-windows-x64-v0.4.0-private.zip` 解压到可写的新目录。该包保留 owner 独立配置访问方式。
2. 双击客户端。程序启动本地代理，并打开 `http://127.0.0.1:9080` 控制面板。
3. 点击“检测连接”，按需启用 Windows 系统代理。正常启动不会自动修改系统代理。
4. 其他应用可设置 `SOCKS5 127.0.0.1:1080` 或 `HTTP 127.0.0.1:8088`；SOCKS 应使用远端解析，例如 curl 的 `socks5h`。
5. 在面板点击“退出 tunnelX”。关闭浏览器标签页不会退出代理程序。

配置包含访问凭据，不能公开分享。`client.json` 默认严格 ECH；`client-normal.json` 为普通模式，可用 `start-normal.cmd` 启动。两种模式均验证证书，普通模式外层 SNI 显示公开域名。

```powershell
.\tunnelx-client.exe -config .\client.json -no-browser
.\tunnelx-client.exe -restore-proxy
.\tunnelx-check.exe -config .\client-normal.json -strict-config .\client.json -out .\acceptance.json
```

退出会恢复程序保存的 HKCU 代理设置；崩溃后，下次成功启动或恢复脚本可恢复。如果其他软件修改代理地址，程序保留备份，避免覆盖新设置。升级会重新建立应用连接，下载由应用继续或重试。

JSON 拒绝未知字段。`transport` 仅接受 `h2`，也可省略；旧 `auto_tcp_preference` 必须移除。发布包已转换。独立测试应复制程序到独立目录，避免共享正式客户端的 `state/`。

## 传输与加密

| 项目 | 本版行为 |
|---|---|
| 外层加密 | TLS 1.3、X25519，Go TLS 协商 AES-GCM / ChaCha20-Poly1305 |
| ECH | X25519 / HKDF-SHA256 / AES-128-GCM；严格模式拒绝 ECH 降级 |
| 证书 | 支持公开证书或无域名自建 CA；客户端验证节点 CA 和名称，不安装到 Windows 系统根证书库 |
| DNS | 固定 IP 引导；目标域名由服务端在鉴权后解析 |
| TCP | H2 Extended CONNECT，多流复用，支持半关闭 |
| UDP | H2 CONNECT-UDP，通过可靠 Capsule 流保留数据报边界 |
| 连接池 | 目标 4 条，按需建立、轮询分配；轮换让既有流继续运行 |
| 故障处理 | 请求头超时先用 PING 确认；确认失效后淘汰，可重试一次新请求 |
| 拥塞控制 | 操作系统 TCP；当前 Linux 节点配置 BBR |
| 探测响应 | 未鉴权请求获得普通 HTTPS 响应，鉴权前不解析或连接目标 |
| 资源限制 | 连接上限、流控窗口、有限 UDP 队列、空闲超时、systemd 内存限制 |

UDP 业务会受 TCP 队头阻塞影响；节点 TCP 不通时无法改用 UDP/H3。证书或 ECH 错误不会降低隐私要求重试。协议沿用 TLS、HTTP/2 和 Capsule 标准，没有自创加密原语；当前 Go TLS 握手指纹不等同于浏览器。

## 持续日志

默认记录应用进程、目标域名/IP 与端口、建连结果、流结束、H2 通道状态和恢复。独立 H2 探测每 30 秒运行一次，不占用应用连接。完整 URL、查询参数、HTTP 凭据及业务载荷不进入日志。

日志为程序旁 `logs/events-*.jsonl`，最多 16 个文件，每个 16 MiB；容量满后淘汰最旧日志。退出停止记录，重启开启新会话。`/api/status` 提供日志目录、写入数、丢弃数和磁盘错误数。升级已按原字节保留旧日志；新会话不再产生 H3 或回退事件。发布包不包含运行日志或代理恢复文件。历史分析工具：`python tools/analyze-connection-logs.py --help`。

## 服务端部署

需要 systemd、Python 3、openssl 和系统 CA。后台节点安装器支持公开证书或无域名自建证书，推荐按 [网站部署说明](docs/CONTROL-DEPLOYMENT.md) 生成安装命令。Ubuntu 26.04 ARM64 与独立 Debian 13.7 ARM64 systemd 虚拟机均已实测，包括节点重启、证书续期和整机重启恢复，见 [Debian 验收报告](docs/debian-systemd-acceptance-v0.6.16.json)。Linux amd64 已交叉编译，尚未在独立主机运行。下面是独立配置版内核的部署方式。

```sh
apt-get update
apt-get install -y python3 openssl ca-certificates
# 解压 tunnelX-linux-servers-v0.4.0.tar.gz 后：
./linux-arm64/tunnelx-admin -out ./config -domain YOUR_DOMAIN -ip YOUR_SERVER_IP -port 8443
# x86_64 主机使用 linux-amd64。
```

将生成的严格客户端配置与 origin CA 安全复制给客户端。服务端保留配置、ECH 密钥和内层证书私钥。

- Caddy 已取得域名证书：执行 `sudo sh deploy/install.sh YOUR_DOMAIN caddy`，小时定时器同步续期结果。
- 其他证书来源：将 full chain 与私钥放到 `config/cert.pem`、`config/key.pem`，执行 `sudo sh deploy/install.sh YOUR_DOMAIN files`；续期 hook 更新 `/etc/tunnelx/public-cert/current/` 证书对。

安装器不占用 443，tunnelX 仅监听 8443/TCP，防火墙允许此 TCP 端口即可。服务端 UDP 出口需可访问目标 UDP 服务。服务使用独立 tunnelx 用户。

```sh
/opt/tunnelx/bin/tunnelx-server -config /etc/tunnelx/server.json -check
systemctl restart tunnelx
systemctl status tunnelx-certificate.timer
journalctl -u tunnelx
```

默认拒绝私网、回环、链路本地目标，线上没有测试夹具白名单。可选 `preferred_target_ips` 仅排序当前 DNS 返回且通过地址检查的 IP。见 [下载修复](docs/PERFORMANCE-v0.2.1.md)。

本节点内层证书到期日为 2027-04-01 UTC，私有 CA 为 2027-10-01 UTC；尚未实现其自动续期，届时需更新并同步客户端。静态 owner Token 在服务端 tokens 数组中管理，重启生效。管理账号通过控制服务动态下发，默认每 5 秒更新。

## 构建与验收

使用 Go 1.27.0，依赖固定且包含于 vendor。所有构建和测试使用 `-tags=http2legacy`，选用 x/net Extended CONNECT 实现。见 [依赖修改](THIRD_PARTY_PATCHES.md)。

```powershell
.\tools\build.ps1
.\tools\build-desktop.ps1
python tools/package-platform.py
go test -race -tags=http2legacy ./... -count=1 -timeout 120s
go vet -tags=http2legacy ./...
python tools/package-release.py --version v0.4.0 --strategy-report docs/five-plan-comparison-v0.3.json
python tools/verify-release.py --version v0.4.0 --strategy-report docs/five-plan-comparison-v0.3.json
```

本版已通过完整 race 测试、真实 Windows 账号连接与 HTTPS / SOCKS UDP、管理流量上报、封禁/解封、Ubuntu / Debian 各 14 项传输验收、各 1,000 次管理流关闭压力回归及正式节点 8 项安全验收。v0.3.3 的局部 TCP 故障/恢复及阻断外层 UDP 报告保留作历史证据。故障仅注入独立测试客户端的本地转发器。控制接口及安全校验已测试，没有浏览器视觉验收。

旧版五方案、H3 和速度报告保留作历史证据，不代表本版功能或速度。本次没有新增多运营商、长期可用性、玩家延迟或播放器卡顿率测试，也不能保证不会被识别、封 IP 或运营商 QoS。Steam 修复见 [报告](docs/STEAM-v0.3.1.md)。
