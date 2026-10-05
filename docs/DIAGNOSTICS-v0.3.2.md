# tunnelX v0.3.2 持续连接诊断

本次升级从客户端启动后开始记录，历史的 v0.3.1 累计计数无法补成带时间和应用归属的记录。客户端保持运行即可持续写日志；Codex 对话窗口无需保持打开。服务端协议、加密、节点地址和 H2/H3 选择策略沿用原配置。

本机已于 **2026-10-02 04:58:39（北京时间）** 切换到 v0.3.2，正式记录会话为 `6b9557b0`。上线后的两个采样周期均已写入 H2、H3 成功探测，实际 Steam、Chrome、Edge、Telegram 等代理连接已获得进程归属；该次验收中日志丢弃与磁盘错误均为 0。上线验收用于确认记录功能持续运行，后续异常需要根据新日志判断。

## 记录内容

| 事件 | 可以用于判断什么 |
|---|---|
| `client_started` / `client_stopped` | 程序版本、启动时间、会话、正常退出与记录覆盖范围 |
| `connection_requested` / `connection_opened` | 请求程序的 exe 名称、PID、进程创建时间，目标域名/IP、端口，实际 H2/H3 与载体连接编号 |
| `connection_error` / `connection_open_failed` | 建连、上传、下载、响应头等阶段的失败；只保存固定错误分类、数字系统/协议错误码 |
| `stream_direction_ended` / `connection_ended` | 应用或目标的 EOF、复位、超时、关闭，以及持续时间和上下行字节数 |
| `transport_fallback` | 回退时间、H2→H3 或 H3→H2、相关应用与请求，是否已确认载体失效 |
| `carrier_opened` / `carrier_failed` / `carrier_closed` / `carrier_draining` | 载体建立、故障、关闭、温和轮换或 GOAWAY；正常轮换单独标记 |
| `path_probe_ok` / `path_probe_failed` / `path_probe_recovered` | 独立 H2/TCP、H3/UDP 探测的成功、失败及恢复，耗时、HTTP 状态、ECH 是否接受 |
| `heartbeat` / `connection_progress` | 每 30 秒汇总连接和流量，变化中的连接进度、日志丢弃及磁盘错误计数 |

进程归属通过 Windows 的 TCP 所有者表匹配完整本地连接四元组。HTTP CONNECT、普通 HTTP、SOCKS TCP 均记录；SOCKS UDP 使用其控制 TCP 连接的进程归属。查询失败时明确标记未识别，不猜测程序名称。记录覆盖实际经过 tunnelX 的流量；应用直连、其他代理、Steam 自身内部日志中的所有会话并不自动纳入。

目标 EOF 表示观察到对端结束发送，正常文件下载也会出现，不能直接视为断网。应用主动关闭和正常 QUIC 关闭也保留单独分类。载体故障、单一源站错误和整条路径异常需要结合探测与同一时段其他应用一起判断。

## 独立路径探测

默认每 30 秒分别通过强制 H2、强制 H3 请求当前节点的公开首页，采用与业务相同的证书和 ECH 校验。两类探测各使用独立载体，不重置业务连接，不向业务回退计数加入探测结果。一次探测最多等待 5 秒，失败后继续后续周期，成功时写入恢复事件。

探测不携带节点鉴权令牌，不连接用户的目标站点。公开首页返回非 200 单独标为 HTTP 状态问题，不将其直接解释为网络路径中断。恢复事件的 `observed_failure_window_ms` 是采样观察窗口，不能当作精确中断时长。短于采样间隔且未影响已建立业务连接的故障可能没有记录；日志也不能仅凭超时证明运营商 QoS。

## 保存和隐私

默认目录是客户端 exe 旁的 `logs`，本机为 `C:\Users\tanzh\Desktop\tunnelX\dist\windows-client-v0.3.2\logs`。UTF-8 JSONL，每行一个事件；文件名与原始事件时间为 UTC，查询工具会转换为北京时间。每个运行会话使用独立编号，重启后旧日志保留。

默认单文件 16 MiB、最多 16 个文件，总量约 256 MiB；满后按最旧文件轮换，所以保存天数取决于连接量和重启次数。仅清理名称符合 tunnelX 日志格式的文件，不删除无关文件。后台异步写入，不按数据包输出；关键事件立即同步到磁盘，其他事件每 5 秒同步，正常退出排空队列。队列拥堵或磁盘错误会计数并显示在状态接口中。异常终止不会产生 `client_stopped`，不能单凭缺失退出记录判定网络中断。

只记录诊断元数据，不保存正文、完整 URL、路径、查询参数、请求头、Cookie、密码、鉴权令牌、原始远端错误消息或进程命令行。目标域名/IP 与 exe 名称本身也属于使用记录，日志保存在本机；发布打包会排除运行日志和系统代理恢复状态。

## 查询

在项目根目录运行以下命令，时间未注明时区时按北京时间解析。可以只查 Steam，也可以省略 `--app` 查看所有应用及路径探测。

```powershell
python tools/analyze-connection-logs.py --log-dir dist/windows-client-v0.3.2/logs
python tools/analyze-connection-logs.py --log-dir dist/windows-client-v0.3.2/logs --since "2026-10-02 05:00:00" --until "2026-10-02 08:00:00"
python tools/analyze-connection-logs.py --log-dir dist/windows-client-v0.3.2/logs --app steam.exe
```

输出包括覆盖的起止时间、会话、各应用错误和回退数量、路径失败与恢复实例，以及日志丢弃和磁盘错误。`--app` 只显示该应用关联事件，查询整条路径时应省略此参数。仍在写入的最后一行会暂时跳过并计数；完整的无效行另行计数。

默认记录自动启用。可在启动时用 `-log-dir` 指定可写目录，或用 `-health-interval 30s` 调整采样间隔（范围 1 秒至 5 分钟）。

## 验证证据

新增日志轮换、并发写入、跨重启保留、Windows IPv4/IPv6 进程归属，以及慢源站未使健康载体被误判的测试。完整 race 测试、静态检查和原有 HTTP/Steam 半关闭回归均通过。

实际 Windows exe 使用独立测试端口和本地 TCP/UDP 故障转发器，对真实节点进行验收：HTTPS、SOCKS UDP、ECH 和手动探测正常；人为停滞 TCP 后记录业务 H2→H3 回退、H2 探测失败与恢复；人为丢弃 UDP 后记录 H3 探测失败与恢复，TCP 业务仍成功。验证调用程序的 PID 和 exe 名称被正确归属，查询参数和节点令牌不在日志中，测试没有改变系统代理或中断正在使用的客户端。测试日志仅存于 `.local`，不会混入正式运行日志。

最终 exe 验收见 `acceptance-diagnostics-v0.3.2.json` 和 `acceptance-auto-client-v0.3.2.json`；实际切换时间、进程和初始记录状态见 `activation-v0.3.2.json`，上线后的定时写入验证见 `logging-live-v0.3.2.json`。
