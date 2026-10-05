# tunnelX v0.1 线上实现规范

本文件描述实际可互操作的实现。此前协议草案中的 TUN、跨平台客户端、BBR/FEC、业务优先级调度和动态 ECH 发现仍属于后续工作。

## 传输与身份

同一地址和端口接受 TLS 1.3 HTTP/2（TCP）和 QUIC HTTP/3（UDP）。沿用 quic-go 的 v1/v2 支持和 v1 优先顺序。客户端仅使用配置中的 IPv4/IPv6 字面地址拨号，域名作为证书身份。ALPN 为 `h2` 或 `h3`。密钥协商为 X25519；记录层加密交给 TLS 实现协商，支持 AES-128-GCM、AES-256-GCM、ChaCha20-Poly1305。关闭 0-RTT 业务发送和服务端 0-RTT 接受。

普通模式校验公开域名和系统信任链。严格模式通过本地配置中的 ECHConfigList 加密内层 ClientHello，内层身份为 `edge.tunnelx.invalid`，使用配置包携带的 CA 校验。ECH 使用 DHKEM(X25519, HKDF-SHA256)、HKDF-SHA256、AES-128-GCM。外层 `public_name` 为公开域名，ECH 拒绝时继续要求外层公开证书校验并终止，客户端不自动采用服务端的 retry 配置。

证书错误、ECH 拒绝、调用者取消和已知协议/目标拒绝不会触发传输回退。自动模式可在传输握手或 CONNECT 响应头建立失败时切换 H2；严格配置保留 ECH。H3 失败后 30 秒冷却，仅影响新连接。流不迁移，业务数据不重放。

## 鉴权及版本

所有隧道请求在 TLS 内携带：

```text
Authorization: Bearer <32-byte-or-longer-random-base64url-token>
Tunnelx-Version: 1
Priority: u=3, i
```

服务端首先以 SHA256 摘要恒定时间比较全部配置的 Token，要求只有一个 Authorization 字段。失败请求走普通 HTTP 服务路径，不生成目标 DNS 查询、连接或特定的“密码错误”协议响应。通过鉴权的响应携带 `Tunnelx-Version: 1`，版本错误返回 400。凭据字段设置 HPACK/QPACK never-index。

`Priority` 目前只是合法的 HTTP 提示；服务端没有实现独立的优先级调度器，不能据此承诺视频优先。

## TCP

普通 CONNECT 的 `:authority` 是目标 `host:port`，IPv6 使用 `[address]:port`。目标必须包含 1–65535 的端口。服务端在鉴权和版本检查后解析目标，筛除禁止的地址，连接选定 IP，避免二次 DNS 解析造成绑定变化。

成功响应为 200；请求 DATA 字节映射到目标 TCP，目标响应字节映射到响应 DATA。请求 FIN 对目标执行写半关闭，继续读取目标响应。流取消关闭目标连接。失败状态包括无效目标 400、禁止地址 403、容量耗尽 429、目标不可达 502、超时 504。

## UDP

使用 RFC 9298 Extended CONNECT：

```text
:method = CONNECT
:protocol = connect-udp
:scheme = https
:authority = tunnel-server-name:port
:path = /.well-known/masque/udp/{escaped-host}/{escaped-port}/
Capsule-Protocol: ?1
```

成功响应同时确认版本和 `Capsule-Protocol: ?1`。Context ID 固定为 0；未知 Context ID 丢弃。支持 RFC 9297 HTTP Datagrams 和 DATAGRAM capsule（Type 0）。H3 双方启用 Datagrams 时使用原生 Datagram；H2 使用可靠 Capsule 流。未知 capsule 在有限长度内跳过。超过 1 MiB 的 capsule、超过 65527 字节的 UDP 载荷、截断或无效 varint 会终止会话。每个流接收队列最多 64 个包，溢出丢弃。

原生 Datagram 不自行分片；超出当前 QUIC MTU 的包会失败或丢弃。当前实现不会自动把超大原生 Datagram 改为 Capsule。H2 Capsule 会重传底层丢失字节，因此 UDP 时延可能增加。

## 本地应用接口

SOCKS5 支持无认证回环客户端的 CONNECT 和 UDP ASSOCIATE；不支持 BIND 或 SOCKS UDP FRAG。每个 UDP ASSOCIATE 最多 32 个目标流，关联有效期绑定控制 TCP 连接，固定首个合法本地 UDP 来源地址和端口。HTTP 代理支持明文 HTTP 转发和 HTTPS CONNECT，不解密应用 HTTPS。

本地代理只允许配置为回环地址。控制面板检查 Host，写操作同时检查 Origin 和随机 CSRF nonce，响应禁止缓存、外部脚本和嵌入。状态接口不包含节点凭据。

## 已实现的限制

客户端默认 128 个应用连接，服务端默认 256 个活动目标，单个 H2/H3 连接最多 128 条流。QUIC 起始包 1200 字节，流接收窗口 1–8 MiB、连接窗口 4–32 MiB，启用上游 PMTU 探测和 paced CUBIC。服务端目标 I/O 默认空闲超时 120 秒；systemd 进程内存上限 512 MiB。

这是目标流的容量限制，不是完整的公网 TLS 握手抗 DoS 方案。尚未对公网大量恶意连接、持续主动探测和长期 IP 封锁进行压力验收。

## 标准来源

- [TLS 1.3, RFC 8446](https://www.rfc-editor.org/rfc/rfc8446)
- [HTTP/3, RFC 9114](https://www.rfc-editor.org/rfc/rfc9114)
- [HTTP/2 Extended CONNECT, RFC 8441](https://www.rfc-editor.org/rfc/rfc8441)
- [HTTP/3 Extended CONNECT, RFC 9220](https://www.rfc-editor.org/rfc/rfc9220)
- [HTTP Datagrams and Capsule, RFC 9297](https://www.rfc-editor.org/rfc/rfc9297)
- [CONNECT-UDP, RFC 9298](https://www.rfc-editor.org/rfc/rfc9298)
- [ECH, RFC 9849](https://www.rfc-editor.org/rfc/rfc9849)
