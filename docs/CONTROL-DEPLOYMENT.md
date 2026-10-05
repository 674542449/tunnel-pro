# tunnelX 网站控制器部署与维护

网站控制器、Windows 桌面和网络内核分别维护版本。当前版本见源码 `internal/control/model.go` 的 `ConsoleVersion`、`internal/desktop/engine.go` 的桌面版本常量，以及发布 SHA256 清单。网站升级不要求重新分发桌面或替换运行中的节点。

## 安装

在 Ubuntu / Debian 的 systemd 主机中解压控制器包，执行：

```sh
sudo sh deploy/install-control.sh https://YOUR_DOMAIN/control
```

程序监听 `127.0.0.1:18081`；HTTPS 反向代理、域名 DNS 和公开证书需要单独配置。私有配置为 `/etc/tunnelx-control/control.json`。初次安装生成随机管理员密码，不会在终端打印。已有配置不会覆盖。

安装器同时安装备份脚本、systemd service/timer，并执行首份加密备份。初次备份失败会使安装命令失败，不能把失败安装误认为已具备备份能力。每小时备份一次，保留最近 336 份。文件位于 `/var/backups/tunnelx-control`，密钥为 `/etc/tunnelx-control/backup.key`。密钥与 `commercial.security_key` 必须另外私密保存。

备份失败会保留上次成功时间并记录 `failed_at`。商业后台根据失败、缺失、损坏或两小时未更新状态记录告警；成功后记录恢复。SMTP 告警需要配置真实发件服务和收件人。故障导致整个控制器无法运行时，需要外部监控 systemd/健康接口。

## 商业配置

初始安装是文件存储的非商业管理服务，并不会自动创建 PostgreSQL、SMTP 商户或经营政策。启用商业功能前，在私有配置中设置：

- `database_url`：PostgreSQL 连接；数据库和最小权限账号需先创建。
- `commercial.enabled`、`payment_mode`、`security_key`、`require_admin_mfa`；安全密钥为 32 字节随机数的 Base64。开启商业、强制 MFA 或邮件功能都必须提供。
- `mail`：发件模式、SMTP 主机、端口、身份、发件地址与 `alerts_to`；生产使用经验证的 TLS。
- `commercial.terms`、`privacy`、`refund_policy`：经营方确认的真实政策。
- `commercial.live_approved`：正式开售许可；默认不开放。后台“开售准备”逐项检查，不会代替真实结算验收。

支付宝、微信和 USDT 网关配置在网站支付设置页填写。USDT 支持 TRC20、BEP20、Polygon。易支付和 bepusdt 的退款在渠道完成后登记流水，网站不会自动从钱包转账。Stripe 保留接口退款适配。模拟支付仅供获邀账号和管理员使用。

## 备份恢复

只向新文件或新的空数据库恢复，不能覆盖运行中的状态：

```sh
tunnelx-control -config /private/new-control.json \
  -restore-new /private/snapshot.txbk -backup-key /private/backup.key
```

定时备份目前保存在同机，跨主机自动备份、灾备切换和定期自动恢复演练尚未实现。手动下载过一份备份不代表具备持续异地备份。

## 构建与打包

```powershell
.\tools\build-control.ps1
python .\tools\package-control.py
```

打包器默认从源码读取控制器版本，只打包控制器及源码。它校验 ZIP、二进制一致性、H2 依赖和部署凭据排除，不再要求同版本桌面产物或旧的版本专用说明文件。

需要同时分发桌面时显式使用 `--with-desktop --desktop-version vX.Y.Z`，且必须有对应桌面构建、使用说明和匹配 SHA256 的原生程序验收报告。网站版本和桌面版本可以不同。

## 当前边界

节点一键安装、H2/ECH 连接、共享额度、退款撤销和日志都具有真实实现。HTTP/3 已按要求移除；Windows TUN 全局接管及自建证书模式的叶证书自动续期已实现。安卓/iOS 客户端、浏览器 TLS 指纹仿真、自动防 QoS、公开证书模式的内层证书自动轮换、自动客户端升级与代码签名尚未实现。更新入口提供手动下载和校验信息。详见工作空间功能审计报告。


## v0.6.16 自建证书与自定义名称

节点管理 → 添加节点 → 证书方式选择“自建证书 · 无需购买域名”。填写公网 IP、TCP 端口；外层证书名称与内层证书名称均可自定义，留空分别自动生成 `gateway-<节点ID>.tunnelx.invalid` 和 `edge-<节点ID>.tunnelx.invalid`。名称须为 ASCII DNS 格式；中文名称需先转换为 Punycode，不接受通配符、IP、URL 或含空格的名称。

在对应 Ubuntu/Debian 服务器执行生成的命令即可。此模式无需 DNS 解析，不安装或修改 Caddy，也不需要为证书申请开放 80/443；只需开放所填节点 TCP 端口。管理服务地址仍沿用原 HTTPS 地址与证书，该选项仅用于节点。

每个节点创建独立 ECDSA P-256 私有 CA。CA 证书通过已有可信管理通道下发，CA 私钥只保存在节点 `private-cert/ca/key.pem`（root、0600），运行节点服务的普通账户不能读取。客户端保持 TLS 1.3 / 严格 ECH / HTTP2，并检查证书信任链、有效期与内层名称，不关闭证书校验、不导入系统信任库。外层名称仍在握手中可见，不等同于流量不可识别。

叶证书有效期 90 天，定时器每小时检查，剩余不足 30 天时自动续签，以原 CA 签发并原子切换，节点无需重启。CA 有效期 10 年；距到期不足 120 天时续签明确失败，应安排新的 CA 配置分发，不会自行更换客户端信任根。备份应保护整套节点配置，尤其 CA 私钥、ECH 密钥及 origin-ca.pem；不能删除 CA 后静默重建。已有完成安装的节点不会自动切换证书模式或更改证书名称，应新建节点再迁移。

Ubuntu 已验证完整 systemd 安装、心跳、严格 ECH/HTTP2、认证转发下载、错误 CA/名称拒绝与同 CA 续期。2026-10-05 已在独立 Debian 13.7 ARM64 QEMU 虚拟机（独立内核、PID 1 为 systemd）补齐完整安装、认证转发、错误 CA/名称拒绝、SIGKILL 后自动拉起、实际 systemd 定时器续签临近过期证书，以及整机重启后节点/定时器自启与原配置恢复连接。使用 v0.6.16 已发布二进制；原宿主节点进程和二进制未变，测试虚拟机已关闭。报告见 [Debian systemd 验收](debian-systemd-acceptance-v0.6.16.json)。当前独立虚拟机验收覆盖 ARM64，未另建 Debian AMD64 虚拟机；网络采用 QEMU NAT，不代表各云厂商安全组均已验证。
