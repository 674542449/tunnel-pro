# tunnelX v0.2.1：NTriver ISO 下载修复及验收

测试时间：2026-10-01 晚至 2026-10-02 凌晨，中国标准时间。Windows amd64 → `146.56.99.255:8443`，线上服务端为 Ubuntu ARM64。

本次已复现并修正 NTriver 下载经过测试节点时选中慢源站地址的问题。正在运行的 v0.2 客户端无需升级，即可使用上线的服务端修正；已有下载需要由应用重新建立连接。新版 Windows 客户端另包含连接检测隔离和重复启动保护。

## 测试对象及方法

用户提供站点 `ntriver.org` 和 Windows ISO 类别，未指定文件名、下载软件或具体速度。本次选取站点当时提供的代表性镜像 `zh-cn_windows_11_consumer_editions_version_26h2_x64_dvd_3622091b.iso`，返回总大小 9,112,440,832 字节。

站点使用下载链接生成器；测试取得的 HTTPS 实际下载主机为 `tempdelivery.13376767.xyz`。[NTriver 下载页面](https://ntriver.org/download-windows-11)、[生成器源码](https://github.com/ntriver-org/ntriver-org-site/blob/main/src/components/DriveLinkGenerator.jsx)用于确认下载流程。临时签名 URL 和访问凭据未写入报告或公开发布包。

每次读取有大小和时间上限的 HTTPS Range 样本，不下载完整 ISO。程序明确指定本地代理端口，验证目标证书和 HTTP 206 的 Content-Range；报告保留完整字节数与 SHA256。速度为收到的字节数除以整个请求耗时，包含连接和首字节等待。MB/s 按十进制计算，MiB 为二进制文件大小；Mbps = MB/s × 8。

## 复现和源站地址对照

| 路径 | 样本 | MB/s | 结果与证据 |
|---|---|---:|---|
| 修正前，正在运行的 Windows v0.2 / H2 | 首部 32 MiB | 1.86 | 完整、SHA256 一致；[原始记录](performance-ntriver-client-h2.json) |
| 修正前，同客户端四个并行分段 | 总请求 32 MiB | 0.65 | 约 20.2 MB / 30.9 秒，两段超时，未完成；[失败样本](performance-ntriver-h2-parallel4.json) |
| Windows 直连，对照 | 首部 32 MiB | 5.18 | 完整、同 SHA256；[原始记录](performance-ntriver-direct.json) |
| Windows 直连，四个分段对照 | 总计 32 MiB | 6.24 | 全部分段完整、同 SHA256；[原始记录](performance-ntriver-direct-parallel4.json) |
| 服务器出口，默认 IPv4 地址选择 | 请求 32 MiB | 0.02 | 只收到 625,007 字节，30 秒超时；[失败样本](ntriver-server-ipv4.json) |
| 服务器出口，指定 DNS 返回的快地址 | 首部 32 MiB | 17.66 | 完整、同 SHA256；[原始记录](ntriver-server-preferred.json) |

服务器当时解析到两个 IPv4 地址。逐地址保留原主机名和 TLS 校验，用同一 8 MiB Range 测量：`104.21.35.213` 为 7.14 MB/s，完整收到样本；`172.67.179.227` 为 0.088 MB/s，15 秒只收到约 1.32 MB，超时。[逐地址记录](ntriver-ip-probes.json)显示服务器出口到这两个地址的吞吐差异足以解释本次慢路径。

独立 H2 和 H3 连接的首部 8 MiB 样本分别为 4.10 和 4.14 MB/s，均校验一致：[H2](performance-ntriver-fresh-h2.json)、[H3](performance-ntriver-fresh-h3.json)。因此本次问题不能只通过切换 H2 / H3 来解决。尚无证据将该源站地址差异归因为运营商 QoS。

## 已上线的修正

服务端增加可选的 `preferred_target_ips` 地址排序，对 `tempdelivery.13376767.xyz` 优先选择实测较快的 `104.21.35.213`。只使用当前 DNS 返回且通过原有目标限制的地址；首选地址从 DNS 结果消失时恢复使用其他当前地址，不注入旧地址。TCP 连接建立失败仍尝试其余解析地址。[规范补充](PROTOCOL-v0.2.1.md)说明准确行为及配置。

所有代理下载继续经过服务器。外层 TLS / ECH、应用 TLS 校验、Token 和私网目标限制保持原有要求。未更改 Windows 直连规则、系统 DNS、服务器 hosts 或全局网络参数；服务器原先已有 BBR / fq。

上线前在服务端回环独立端口完成 [16 项预检](acceptance-origin-smoke.json)，随后备份原程序和配置，替换并短暂重启 tunnelX 服务。Caddy 未修改。备份保留在服务器 root 专用目录 `/opt/tunnelx/rollback-origin-v0.2.1/`，支持人工回滚。[部署记录](origin-deployment.json)与[服务健康记录](server-health.json)确认线上二进制 SHA256 与本次 ARM64 发布构建一致。

## 修正后的真实 ISO 样本

| 客户端 | 样本 | MB/s | Mbps | 证据 |
|---|---|---:|---:|---|
| 用户正在运行的 Windows v0.2 / H2 | 首部 32 MiB | 12.11 | 96.92 | [完整样本](performance-ntriver-after-32MiB.json) |
| 同一客户端 | 首部 128 MiB | 27.47 | 219.74 | [完整样本](performance-ntriver-after-128MiB.json) |
| 同一客户端 | 从 4 GiB 偏移读取 16 MiB | 6.70 | 53.63 | [完整 Range 样本](performance-ntriver-after-mid-file.json) |
| 新 v0.2.1 Windows EXE，严格模式 / auto | 首部 32 MiB | 11.35 | 90.79 | [实际程序复测](performance-ntriver-new-client-v0.2.1.json) |

32 MiB 样本的 SHA256 均为 `6a240ee4a7c6faf52b15a871a68d89f49923a737f1ccfa068b73cb5ee5cf8b8f`，与修正前、Windows 直连和服务端快地址对照一致。128 MiB 与中部样本完整返回正确 Range 并记录各自 SHA256，未进行独立来源的哈希对照。不同位置和大小的结果存在波动，27.47 MB/s 是该次短样本平均速度，不能承诺整个 ISO 持续达到这一速度。

暂停后继续、重新开始下载或在下载工具中重新建立连接，才能使用新的源站选择。已建立的业务流不会迁移、自动重放或改选目标地址。

## 客户端回归修正及验收

初次完整线上回归发现 H3 检测后，同一 Mux 上的目标拒绝检查超时；单独执行拒绝检查则正常。两次初始结果为 [13/16](acceptance-server-v0.2.1.json) 和 [12/16](acceptance-server-v0.2.1-retest.json)，保留原始失败记录。这表明检测与业务连接复用存在干扰条件，尚未定位到 QUIC 库内部的具体机制。

新版 `PublicGET` 为连接检测使用独立 Mux，响应关闭或取消时只关闭检测通道。鉴权检查也已收紧：错误 Token 必须收到普通服务的 HTTP 405，传输超时不再算作拒绝成功。

- [最终 Windows 到线上节点 16 项检查](acceptance-v0.2.1-final.json)：全部通过。覆盖普通 / 严格模式 H2、H3，错误 Token 明确拒绝、私网目标拒绝、证书错误和 ECH 拒绝禁止降级，以及 TCP 阻断时回退 H3。
- [新版真实 EXE 自动分流](acceptance-auto-client-v0.2.1.json)：TCP HTTPS 用 H2，SOCKS5 UDP DNS 用 H3，严格 ECH 成功，目标证书验证成功。
- [新版 EXE 启动和检测回归](acceptance-windows-startup-v0.2.1.json)：先执行 H3 检测，再建立 H2 TCP 和 H3 UDP 业务，均成功。重复占用代理端口以及仅占用控制端口两种场景均给出明确中文错误，原客户端继续运行、代理备份不变、Windows 代理注册表不变。测试副本使用独立端口并已退出。
- `go test -race -tags=http2legacy ./... -count=1 -timeout 120s` 与 `go vet -tags=http2legacy ./...` 最终全套通过，Windows amd64、Linux amd64 / arm64 构建完成。本轮早期曾有一次本地测试夹具 3 秒内未开始监听；该测试独立重跑 5 次和后续两次完整 race 检查均通过，未据此改动业务逻辑。
- 新增集成测试覆盖 H2 / H3、普通 / 严格模式下检测关闭并取消后业务仍能建立和完整传输。地址排序测试覆盖 DNS 地址缺失、其他域名不受影响以及目标过滤不被绕过。

服务端临时签名 URL、临时客户端配置、测试程序和回环服务配置已删除，测试端口 18543 已关闭。[清理记录](cleanup-origin-v0.2.1.json)；生产 TCP / UDP 8443、Caddy 和证书定时器均正常，私网目标白名单为空。未退出用户正在运行的客户端，也未更改其 Windows 系统代理。

本轮未下载或校验完整 9.1 GB ISO，未新增弱网、多运营商或长时间持续吞吐测试。当前地址偏好需部署者依据新测量维护，没有自动出口质量选择。浏览器图形界面未重新进行视觉验收；控制接口通过程序请求验证。

发布文件：`tunnelX-windows-x64-v0.2.1-private.zip`、`tunnelX-linux-servers-v0.2.1.tar.gz`、`tunnelX-source-v0.2.1.zip`。Windows 配置包含此节点的私密访问 Token；源码和 Linux 包不含此部署的 Token、ECH 私钥或内层证书私钥。
