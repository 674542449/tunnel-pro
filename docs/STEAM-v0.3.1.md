# Steam 下载兼容性修复：v0.3.1

2026-10-02 在当前 Windows 电脑定位并修复普通 HTTP 转发的提前半关闭问题。修复版已替换正在运行的 v0.3，用户重试后确认“下载已经恢复”。加密、严格 ECH、H2 四连接池及 H3 回退配置继续保留，Ubuntu / Debian 服务端无需更新。

## 原因与对照

Steam `content_log.txt` 在 03:09–03:10 记录国内 CDN 的下载清单请求经过 `127.0.0.1:8088`，连续收到 `502 Bad Gateway`，最终报 `No connection`。游戏下载已经进入 HTTP 代理，不能归因于应用绕过系统代理。

旧版 `forwardHTTP` 在写完 HTTP 请求后调用 `Tunnel.CloseWrite()`，服务端将其映射为目标 TCP 的发送方向关闭。HTTP 本身已经通过报文头、Content-Length 或分块编码标记请求结束，无需额外发送 FIN。这几个 CDN 在收到早期 FIN 后直接断开，使代理读不到响应头并返回 `Response failed`。

03:15 对同一份尚有效的下载清单 URL，只改变是否提前半关闭发送方向，结果如下。请求经过同一服务器和加密传输；URL 查询参数、下载凭据未写入报告。

| 下载节点 | 旧版 HTTP 代理 | SOCKS TCP 保持发送方向开放 | SOCKS TCP 提前半关闭 |
|---|---|---|---|
| dl.steam.clngaa.com | 502 / Response failed | 200 | RemoteDisconnected |
| st.dl.eccdnx.com | 502 / Response failed | 200 | RemoteDisconnected |
| xz.pphimalayanrt.com | 502 / Response failed | 200 | RemoteDisconnected |
| cache7-hkg1.steamcontent.com | 200 | 200 | 200 |

不同 CDN 对 FIN 的处理不同，解释了部分下载仍然正常的现象。Steam 的 HTTPS 下载走 CONNECT 隧道，也绕开了这段普通 HTTP 转发逻辑。该对照直接支持 HTTP 兼容性错误，不支持将此次失败认定为运营商 QoS。

原始脱敏结果见 [修复前对照](steam-diagnosis-before.json)。

## 修改与验证

`internal/client/proxy.go` 删除普通 HTTP 请求写完后的提前 `CloseWrite`，在完整读取响应后关闭整个隧道。SOCKS 和 CONNECT 的双向半关闭能力保留。

- 新增真实 H2 CONNECT 集成回归测试，模拟收到早期 FIN 就取消请求的 CDN。修改前 GET、POST、HEAD 均返回 502；修改后通过，检查分段响应、完整内容、分块上传、签名路径与查询参数、逐跳头过滤，以及应用到代理连接的连续使用。
- `go test -race -tags=http2legacy ./... -count=1 -timeout 120s` 与 `go vet -tags=http2legacy ./...` 通过。
- 使用实际 Windows 修复版 EXE，在独立端口测试 H2、H3、自动三种模式。三个国内 CDN 的状态和响应 SHA256 均与不改写数据的 SOCKS TCP 一致。此时旧的 Steam 清单 URL 已过期，源站返回 401；这项结果验证正确转发源站响应，不代表成功下载清单。
- HTTPS CONNECT、UDP DNS、连接检测后业务传输、严格 ECH 检查通过，隔离测试未修改 Windows 系统代理。见 [修复版验证](acceptance-steam-v0.3.1.json)与 [TCP / UDP 验证](acceptance-auto-client-v0.3.1.json)。
- 对不带 CDN 鉴权参数的缓存文件块 URL，源站返回 403，HTTP 与 SOCKS 的响应一致。该探测不作为成功下载验收。见 [无鉴权探测](steam-unauthenticated-chunk-probe-v0.3.1.json)。

03:21 切换正在运行的客户端为 `dist/windows-client-v0.3.1/tunnelx-client.exe`。节点身份、本地三个端口、系统代理设置，以及退出时恢复原设置的备份均已核对。旧版保留在原目录。见 [切换记录](activation-v0.3.1.json)。

修复版客户端 SHA256：`e0011057ea9ffbe12648ae1a4421b3f74a2d631f6c65450e4cb53e252ca848f5`。

## 实际 Steam 重试与边界

用户在切换后确认下载恢复。03:22–03:23 的 Steam 日志记录多个清单返回 200，进入文件块下载，并出现 `105.602 Mbps` 的瞬时速率记录。此次重试使用香港 SteamCache 的 HTTPS 节点，与修复前的国内 HTTP 节点不同，因此不能把这条实际速率记录当作同节点性能对照；普通 HTTP 缺陷由前述控制 FIN 的实验单独确认。

验收覆盖请求转发正确性和实际下载开始恢复；没有等待整个大型游戏下载、安装或更新完成，也未进行这次修复后的长时 QoS 评估。脱敏日志和用户确认见 [实际使用记录](steam-user-verification-v0.3.1.json)。
