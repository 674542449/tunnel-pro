# tunnelX Desktop 与管理平台 v0.4.0

管理后台现为 v0.4.2，客户端和节点继续使用 v0.4.0。功能修复见 [管理后台 v0.4.1](ADMIN-FIXES-v0.4.1.md)，界面与 SVG 改版见 [管理后台 v0.4.2](ADMIN-DESIGN-v0.4.2.md)；下文的 v0.4.0 验收报告保留为历史记录。

本版参考 [tunnel-pro](https://github.com/674542449/tunnel-pro) 的账号、节点、套餐、管理后台与桌面客户端结构，独立实现这些功能。节点使用已有 tunnelX 内核：TLS 1.3、严格 ECH、HTTP/2 Extended CONNECT 和 UDP Capsule；没有接入参考项目的 WebSocket/uTLS 传输。参考版本固定在 `a6d50dec6f270dc3fe30ee1be35bad1344804a7a`，见 `tunnel-pro-reference.json`。

## 使用

管理平台：**https://test.xiaguamail.com/control/**。管理员账号和初始密码单独放在本机 `.local/platform/PRIVATE-ACCESS.txt`，不进入公开发布包。管理员账号也可登录 Windows 桌面客户端。

1. 先退出旧 tunnelX，释放本地 1080、8088 端口。
2. 将 `tunnelX-desktop-windows-x64-v0.4.0.zip` 解压到可写目录，启动 `tunnelx-desktop.exe`。需要 Windows x64 和 Microsoft WebView2 Runtime；程序尚未代码签名。
3. 登录后选择“韩国测试节点 · H2”，点击连接。勾选系统代理时，连接成功后才写入 Windows 代理设置。
4. 关闭窗口会收起到托盘，保持连接。用窗口或托盘的“退出”停止程序并恢复原代理。

新注册账号需要激活试用或由管理员开通。当前试用为一次性 24 小时、1 GiB；已开通账号不能领取试用并覆盖原有额度。邮箱目前仅作为账号标识，未接入邮箱验证或自动找回密码。

默认 SOCKS5：`127.0.0.1:1080`，HTTP：`127.0.0.1:8088`。系统代理只覆盖遵循该设置的应用。没有 TUN 驱动，应用直连和自行发出的 DNS 不会自动进入隧道。普通启动使用原生桌面窗口，既有命令行客户端仍可使用。

## 已实现功能

| 部分 | 功能 |
|---|---|
| Windows | 登录/注册、节点列表与延迟检测、连接/断开、托盘、上下行速率、系统代理、公告、订单、手动更新信息、持久诊断日志 |
| 账号 | 独立节点凭据、有效期、累计流量额度、封禁/解封、续期、重置密码、撤销节点凭据 |
| 节点 | 多节点登记、启停、接入配置导出、心跳、在线状态、活动连接数 |
| 套餐与订单 | 套餐记录、用户创建订单、管理员核实开通、重复开通幂等 |
| 管理后台 | 用户、节点、套餐、订单、审计、公告、发布版本与文件 SHA256 |
| Linux | Ubuntu/Debian，amd64/arm64 构建，systemd 独立低权限服务 |

支付由管理员人工核实；本版没有在线收款。更新信息由管理员填写 HTTPS 地址和 SHA256，客户端显示给用户手动下载，没有自动执行远端更新文件。

v0.4.1 后台界面提供节点/套餐添加、编辑、启停、停用记录删除，以及常用账号操作。新增节点需先配置其证书、ECH 密钥和 DNS，再登记客户端模板及 origin CA，导出该节点专用 `agent.json` 并安装到对应服务器。

## 内核与账号接入

传输加密保持原有 TLS 1.3：X25519 密钥交换，标准 AES-GCM/ChaCha20-Poly1305 协商。ECH 保持 X25519/HKDF-SHA256/AES-128-GCM。严格模式验证外层及内层证书，拒绝 ECH 降级。节点只监听 8443/TCP；UDP 应用数据经 H2 Capsule 传输。没有 HTTP/3、QUIC 或跨协议回退。

管理会话与节点访问凭据分开。账号密码使用随机盐、600,000 次 PBKDF2-SHA256；API 会话使用 256 位随机令牌，服务端仅保存会话哈希，有效期 24 小时。后台浏览器使用 HttpOnly/Secure/SameSite=Strict Cookie，并校验 Origin/CSRF。登录有 IP 频率及并发限制。

Windows 使用当前用户 DPAPI 保存会话，不保存明文登录密码。登录状态绑定 API 地址，切换地址会清除旧会话。节点获取每个账号的独立凭据，客户端使用每次安装固定的设备 ID。公网 profile 返回用户凭据和公有 CA，不包含节点管理密钥、TLS 私钥或其他用户凭据。

节点每 5 秒同步授权和累计字节数。封禁、停用节点、凭据撤销、到期和额度不足可关闭已有流；新请求在鉴权前不解析目标。控制平面失联时，已获取的策略最多有效 90 秒，过期后管理账号停止通行；恢复同步后可重连。

设备数按同一节点的并发设备 ID 计数，每个用户最多 128 个活动流。这是软件设备标识，不是硬件认证，也不是跨节点统一设备上限。流量只计隧道业务字节，不是运营商账单流量；多节点总额度按同步周期汇总，存在短时超额窗口。节点累计计数在上报前持久化，重试/重启不重复计费；突然掉电可能丢失最近约 5 秒未持久化计数。

为了兼容旧客户端，服务器配置中的原有 owner token 保留静态访问，不受管理账号额度限制，其流量不进入用户计费统计。需要统一管控时，所有客户端改用管理账号后，再由节点拥有者撤销静态 token。

## 日志、数据与维护

桌面程序连接成功后开始写 `logs/events-*.jsonl`，包括应用进程、目标域名/IP、端口、建连/结束/错误、H2 通道及恢复信息；退出或断开时停止。每 30 秒独立探测 H2。最多 16 个 16 MiB 文件，满后淘汰最旧文件。完整 URL、HTTP 凭据和业务载荷不写入日志。管理后台只接收用户 ID、字节计数、节点心跳和连接数，不上传目标访问记录。

服务端：

- 管理配置：`/etc/tunnelx-control/control.json`（含初始凭据，仅用于第一次初始化账号）。
- 管理数据：`/var/lib/tunnelx-control/control.json`。
- 节点配置：`/etc/tunnelx/server.json` 与 `/etc/tunnelx/agent.json`。
- 节点计数：`/var/lib/tunnelx/agent-state.json`。
- 服务日志：`journalctl -u tunnelx -u tunnelx-control`。

数据采用单进程 JSON 快照，先写临时文件、刷盘，再替换；失败不提交内存状态，损坏的数据文件不会被自动清空。Linux 同步目录元数据；Windows 使用 MoveFileEx 替换并刷写。适用于当前小规模部署，尚未实现数据库、多管理副本和跨地域高可用。

v0.4.1 可在“设置与公告”即时关闭公开注册或设置 `trial_hours: 0` 关闭试用。后台保存的设置优先于初始管理配置；未保存时沿用初始配置。修改初始配置密码不会重置现存管理员，应使用后台用户列表中的“重置密码”。

备份管理数据时先停止控制服务；备份节点计数时先停止节点。两者连同 TLS/ECH 配置、私钥一起作为私有备份保存。管理员重置密码会撤销该账号所有 API 会话及节点凭据，需要重新登录和连接。

内层证书到期日为 2027-04-01 UTC，私有 CA 为 2027-10-01 UTC，仍需届时更换并同步客户端；公开证书沿用现有续期定时器。JSON 快照及审计是运行记录，不能作为不可篡改财务账本。

## Ubuntu / Debian 部署

从 Linux 发布包目录执行，需要 systemd、Python 3、openssl、系统 CA 和已配置的 HTTPS 反向代理：

```sh
# 单独管理服务；不会自动改写已有 Caddy/Nginx 配置
sudo sh deploy/install-control.sh https://YOUR_DOMAIN/control
```

将以下段落合并进对应 Caddy 域名的现有站点中：

```caddyfile
redir /control /control/ 308
handle_path /control/* {
    reverse_proxy 127.0.0.1:18081
}
# 站点其他内容继续由其现有 handle 处理。
```

新节点先按主 README 生成并安装 H2 配置：

```sh
./linux-arm64/tunnelx-admin -out ./config -domain YOUR_DOMAIN -ip YOUR_IP -port 8443
sudo sh deploy/install.sh YOUR_DOMAIN caddy
# 管理后台登记节点，下载其私有 agent.json 后：
sudo sh deploy/attach-managed-node.sh /secure/path/agent.json
```

管理员登记节点时使用生成的严格客户端配置：固定 IP、8443 端口、内层名称、ECHConfigList 和 origin CA。节点需能访问管理 API 的 HTTPS 地址。API 仅绑定回环，不对公网开放 18081。

## 构建

根模块只包含 H2 核心依赖；Wails/托盘依赖放在独立的 `desktop/` Go 模块，并附带依赖许可证。Go 1.27，Node 用于语法检查，Wails CLI 2.16.0。构建时两处现有 H2 vendor 补丁会自动验证和重新应用。Wails 框架的本地 IPC/开发依赖不是节点传输协议。

```powershell
.\tools\build.ps1
.\tools\build-desktop.ps1
python tools/package-platform.py
python tools/package-release.py --version v0.4.0 --strategy-report docs/five-plan-comparison-v0.3.json
python tools/verify-release.py --version v0.4.0 --strategy-report docs/five-plan-comparison-v0.3.json
```

## 验收范围

- 完整 Go race 测试、go vet、两个界面 JS 语法检查、Windows 原生可执行文件构建。
- Ubuntu 26.04 ARM64、Debian 13.7 ARM64：各 14 项 H2/ECH 传输验收；Debian 使用独立根文件系统、共享宿主内核。
- 每个系统各 5 轮管理账号 TCP/UDP 与撤销测试，合计各 1,000 次并发突然关闭。
- Windows 实际桌面可执行文件以无窗口测试模式完成：注册、登录、试用、选节点、HTTPS、普通 HTTP 转发、SOCKS UDP、累计上报、封禁关闭现有连接、拒绝新连接、解封重连、DPAPI、日志和代理设置恢复检查。
- 通过账号连接下载两次 64 MiB 受控 HTTPS 文件，验证完整长度及 SHA256；这是到同一测试节点的短时性能样本。
- 正式节点 8 项安全验收，原有 owner 访问正常；节点仍没有 8443/UDP 监听。

验收发现过两次 H2 ResponseWriter 取消回调生命周期崩溃。已暂时恢复稳定内核，修复为停止或等待回调完成后才结束 handler，经 ARM64 压力回归后重新部署。历史记录保留在服务日志和部署报告中。

证据：`acceptance-desktop-platform-v0.4.0.json`、`acceptance-desktop-traffic-v0.4.0.json`、`server-validation-v0.4.0.json`、`managed-regression-v0.4.0.json`、`platform-deployment-v0.4.0.json`。

没有渲染客户端或后台页面，视觉效果及托盘点击尚未实测。Linux amd64 仅交叉编译；Android APK、iOS、在线支付、自动升级和 TUN 均未实现。本次没有长期、多运营商或 Steam 真实游戏下载复测，不能据此保证不会被识别或 QoS。
