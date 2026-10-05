# tunnelX v0.3 客户端规范补充

线上协议、`Tunnelx-Version: 1`、TLS 套件、ECH、鉴权及目标限制保持原样，继续使用 [v0.1](PROTOCOL-v0.1.md)、[v0.2](PROTOCOL-v0.2.md) 和 [v0.2.1](PROTOCOL-v0.2.1.md) 的帧格式。现有 Ubuntu / Debian 服务端兼容。

## H2 配置

新增客户端可选字段；旧客户端严格拒绝未知字段，需先升级程序。

| 字段 | 缺省值 | 范围 / 意义 | 本次胜出配置 |
|---|---:|---|---:|
| `h2_connections` | 1 | 1–8，目标 H2 连接数 | 4 |
| `h2_read_idle_seconds` | 25 | 1–120，未读到帧时开始 PING 检查 | 5 |
| `h2_ping_timeout_seconds` | 5 | 1–30，PING 等待上限 | 2 |
| `h2_max_age_seconds` | 0 | 0 禁用；10–3600，超过年龄后停止分配新请求 | 120 |
| `h2_retry_new_connection` | false | 已确认 H2 传输故障时，允许一次新连接建立请求 | false |

胜出包另设置 `transport=auto`、`auto_tcp_preference=h2`、`connect_timeout_seconds=2`。缺省值保留原单连接行为；使用发布包中的配置才会启用本次选出的策略。

## 分配与轮换

按需建立 H2 连接直到目标数量，之后轮询分配新请求。选取时使用 HTTP/2 的请求预留，避免刚分配但尚未发送的请求被当作空闲连接关闭。H2 建连不持有传输模式 / H3 的全局锁。

轮换只标记旧连接排空，既有业务继续传输。排空连接没有活动、预留或待处理请求时关闭。总连接数最多为目标数量的两倍；排空数量达到上限时推迟继续轮换。拥塞或吞吐变慢本身不会触发迁移。HTTP/2 多流仍共用 TCP，TCP 队头阻塞没有被 HTTP/2 消除；分池只缩小同一连接受影响的范围。[HTTP/2 RFC 9113](https://www.rfc-editor.org/rfc/rfc9113.html#section-1)。

## 故障与重试

CONNECT 响应头超时并不自动代表外层连接故障。启用 H2 重试或自动模式时，若连接仍可接收请求，先在指定期限内 PING。成功收到 ACK 则保留连接，避免中断其他业务；该请求仍可在自动模式下尝试 H3，但不将正常 H2 通道置入冷却。确认连接故障后关闭并移出池，禁止后续请求继续复用。

`h2_retry_new_connection=true` 允许在业务字节尚未发送时进行一次 H2 新请求；仍失败时，自动模式可以继续尝试 H3。胜出配置将此开关关闭，以便 TCP 路径不可用时直接使用 H3。证书错误、ECH 拒绝、明确鉴权 / 目标拒绝以及调用方取消不触发该重试，也不放宽隐私要求。

只有业务请求建立阶段允许回退。已建立流的业务字节不重放、不迁移；应用需自行重试或续传。真正失效的连接上已有流会被关闭。年龄轮换则保留健康的已有流。

响应等待超时 / 取消后，迟到的 RoundTrip 响应会关闭 Body，避免保留无调用方使用的响应。原有 30 秒传输冷却和同隐私模式回退规则继续适用。

## 五方案工具

`tunnelx-lab` 针对当前测试部署对比五组配置。完整复测需要在服务器临时准备受控 HTTPS 文件，运行结束后恢复原 Caddy 配置：

```powershell
.\tunnelx-lab.exe -config .\client.json -rounds 3 -soak-seconds 130 -out .\five-plans.json
# 仅运行真实 WAN 上的受控 TCP / UDP 故障测试
.\tunnelx-lab.exe -config .\client.json -phase recovery -out .\recovery.json
```

本工具使用严格 ECH，不修改系统代理或全局网络参数。故障只在本机测试转发器施加；不是运营商 QoS 仿真器。临时文件准备和清理脚本位于 `tools/prepare-public-benchmark.sh`、`tools/cleanup-public-benchmark.sh`，只适用于其明确验证的测试域名配置。
