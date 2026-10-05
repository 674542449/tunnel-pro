# tunnelX Windows v0.6.11

完整解压安装包，再运行 `tunnelx-desktop.exe`。不要单独移动 EXE：TUN 依赖旁边的 `runtime` 目录。Windows 需要 WebView2 Runtime。

## 三种代理模式

- **绕过大陆**：国内域名、大陆 IPv4/IPv6 和局域网直连，其余流量经现有 HTTP/2 节点。遵循系统代理的应用自动使用；其他应用需手动设置本地 SOCKS/HTTP 代理。连接偏好中的系统代理需开启。
- **全局代理**：进入本机 HTTP/SOCKS 代理的流量统一经节点。不遵循 Windows 系统代理的应用不会自动被此模式接管。
- **TUN 模式**：退出客户端后右键“以管理员身份运行”。通过 Wintun 虚拟网卡接管 TCP/UDP，再转给本机 SOCKS 和现有 H2 内核。节点、管理服务 IP 保留直连出口；局域网沿用更具体的原有路由。ICMP 等非 TCP/UDP 协议不在接管范围，不能用 ping 成败判断代理是否可用。

连接期间不能切换模式，请先断开。旧版偏好升级后保留全局行为，不会擅自切换分流。

## 国内分流规则

来源：[Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat) 与 [gaoyifan/china-operator-ip](https://github.com/gaoyifan/china-operator-ip)。内置快照固定上游提交并附 SHA256；设置页可更新，失败保留旧规则，下次连接生效。

匹配顺序：局域网、直连例外、国内域名/IP、已知代理域名；未知域名最多等待本地 DNS 500 ms，所有解析结果均为大陆公网 IP 才直连，否则代理。精确域名与域名后缀分开处理。直连例外支持域名、IP、CIDR，最多 256 条，仅影响绕过大陆模式。

规则不是永久完整清单，企业新增域名、境外业务和第三方 CDN 可能需要更新或补例外。直连流量不经过节点，因此不会产生节点端的套餐流量计费；本机速度图会包含经过本机代理处理的直连字节。

## TUN 恢复与依赖

正常断开恢复本程序创建的路由、DNS 策略。客户端意外退出时独立守护进程通过标准输入关闭检测并清理；未完成的操作保存恢复记录，下次以管理员权限启动可在设置中“恢复网络与代理设置”。不应手动删除状态记录来代替网络恢复。

TUN 适配层采用 [tun2socks](https://github.com/xjasonlyu/tun2socks) 和 [Wintun](https://www.wintun.net/)，并非自行发明的 IP 网络栈；现有 H2 传输和加密不变。安装包附上游源码快照、许可证、Go 依赖声明及签名原版 Wintun DLL。重建见 `tools/build-tun-runtime.py`，重建后需要重新构建和验收客户端。

v0.6.11 已对真实虚拟网卡在隔离测试网段执行 IPv4/IPv6 TCP/UDP 转发、正常退出、网络栈崩溃及所有者进程退出恢复测试。此测试不修改真实默认路由和系统 DNS，不等同于对所有 Windows 网络环境或其他 VPN 共存情况的验证。

后台与本安装包使用同一产品版本 v0.6.11。更新不会自动替换已运行的旧客户端，请退出后再启动新版。
