# v0.3.3：仅 HTTP/2

客户端和服务端均移除 H3，包括 QUIC 连接、HTTP/3 服务监听、原生 HTTP Datagrams、跨协议回退和冷却、H3 探测、QPACK 修改及 quic-go / qpack 依赖。面板仅显示 H2，`/api/mode` 已删除；配置只接受 `transport: h2`，旧 `auto_tcp_preference` 不再接受。

## 保留的行为

TLS 1.3、X25519、AES-GCM / ChaCha20-Poly1305 协商、严格 ECH、证书校验、鉴权及目标地址检查保持不变。H2 池保留 4 条连接、5 秒空闲检测、2 秒 PING 超时、120 秒温和轮换；确认通道失效后允许新建 CONNECT 重试一次。既有数据不重放，健康通道不会因单个慢源站被淘汰。

SOCKS5 UDP ASSOCIATE 保留，UDP 业务通过 H2 可靠 Capsule 流传输。客户端本地 SOCKS UDP 套接字和服务端访问目标的 UDP 套接字属于应用功能；隧道没有 UDP 监听或 QUIC。节点 TCP 不通时没有另一个传输协议接替，UDP 业务也受 TCP 队头阻塞影响。

Steam 普通 HTTP 修复、HTTPS CONNECT、半关闭、源站地址排序、代理恢复、进程归属及持久日志保留。

## 实际上线

Windows 于 **2026-10-02 05:59:12（北京时间）** 启动 v0.3.3，日志会话 `7a41d552`，程序位于 `dist/windows-client-v0.3.3/tunnelx-client.exe`。旧日志 `events-20261001-205839-6b9557b0-000000.jsonl` 已按原字节复制到新目录并核对 SHA256；原文件也保留。

节点于服务端记录的 **2026-10-02 06:00:55（北京时间）** 重启一次以更换二进制。重启起止均记在这一秒，没有测量更细时间。机器时钟可能有偏差；这一窗口附近的连接关闭应结合计划维护判断。生产配置 SHA256 未改变，TLS/ECH 私钥及访问凭据没有替换。服务以 `tunnelx` 用户运行，8443 仅有 TCP 监听。

- [Windows 切换](activation-v0.3.3.json)
- [节点部署](server-deployment-v0.3.3.json)
- [节点状态](server-health-v0.3.3.json)
- [代理恢复状态复核](proxy-state-recheck-v0.3.3.json)

服务端保留失败恢复用的旧二进制备份；旧源码和验收报告属于历史档案，没有加载到当前程序中。

## 验收

| 验证 | 结果和范围 |
|---|---|
| Go race / vet | 全部通过，覆盖 ECH、鉴权、代理、半关闭、失效判断及并发 |
| 删除项 | 配置拒绝 h3 / auto，H3 隧道请求被拒绝；服务端端口不持有 UDP 监听 |
| Capsule 编解码 | RFC 公布数值、边界、截断和非最短编码测试通过，无 QUIC 依赖 |
| Windows 实际程序 | HTTPS、SOCKS UDP DNS、进程归属和日志通过，阻断外层 UDP 后仍正常 |
| 局部 TCP 故障 | 独立本地转发器阻断约 8 秒，失败及恢复写入日志，恢复后应用重试成功 |
| Ubuntu 26.04 ARM64 | 普通 / 严格 H2 共 14 项通过，含 64 MiB 文件校验、16 MiB 上传、UDP、并发及安全拒绝 |
| Debian 13.7 ARM64 | 同样 14 项通过，共享 Ubuntu 宿主内核 |
| 正式节点 | 8 项安全验收通过，运行进程二进制 SHA256 与发布构建一致 |

证据：[Windows 故障](acceptance-diagnostics-v0.3.3.json)、[H2 业务](acceptance-h2-client-v0.3.3.json)、[Ubuntu](acceptance-ubuntu-v0.3.3.json)、[Debian](acceptance-debian-v0.3.3.json)、[服务端汇总](server-validation-v0.3.3.json)、[正式节点](acceptance-production-v0.3.3.json)。

Windows amd64 与 Linux amd64 / arm64 均已构建，Linux amd64 未在独立主机实际运行。没有浏览器视觉验收，没有重新测量玩家延迟、视频卡顿率、Steam 完整游戏或 ISO 全文件吞吐，也没有新增长期、多运营商数据。

## 持续记录

每 30 秒仅探测 H2，记录心跳、目标连接和载体状态。新会话没有 H3 / 跨协议回退事件；旧历史事件保留原内容，分析脚本仍能读取。默认 16 个 16 MiB 文件轮换；容量满后逐步淘汰最早日志，应用退出后停止记录。

[持续日志复核](logging-live-v0.3.3.json) 验证周期探测与心跳持续运行、丢弃及磁盘错误为 0。故障验收日志位于 `.local/` 独立测试目录，未混入正式日志。
