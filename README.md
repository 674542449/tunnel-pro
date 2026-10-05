# tunnelX

基于 HTTP/2 的安全代理，包含管理面板、Windows 桌面客户端和 Linux 节点服务端。

## 架构

```
管理面板 (tunnelx-control)        节点服务端 (tunnelx-server)
127.0.0.1:18081                   :8443/TCP
  - 用户注册/登录/MFA               - H2 Extended CONNECT (TCP)
  - 节点管理 + 一键安装              - CONNECT-UDP + Capsule (UDP)
  - 套餐/订单/支付                   - TLS 1.3 + ECH
  - 操作审计                        - 未鉴权请求返回普通页面
  - Web 管理后台

Windows 桌面客户端 (tunnelx-desktop)
  - 账号登录 + 节点选择
  - 系统代理 / TUN 模式
  - SOCKS5 127.0.0.1:1080
  - HTTP   127.0.0.1:8088
```

## 快速部署

### 1. 安装管理面板

```bash
# Ubuntu / Debian，root 执行，交互式三选一
curl -fsSL https://raw.githubusercontent.com/674542449/tunnel-pro/master/deploy/quick-install-control.sh | bash
```

脚本交互式引导，支持三种部署方式：

| 方式 | 需要域名 | 需要开端口 | 需要证书 | 复杂度 |
|------|---------|----------|---------|-------|
| Cloudflare 隧道 | 是（CF 托管） | 无 | CF 自动 | 最低 |
| Nginx 反代 | 是 | 443 | Let's Encrypt 自动 | 低 |
| 仅安装面板 | 否 | 自行决定 | 自行处理 | 手动 |

选择后脚本自动下载最新版本、安装反代、申请证书，全部搞定。管理面板路径可自定义。

### 2. 添加节点

登录管理面板 → 节点管理 → 添加节点 → 复制安装命令到节点服务器执行。

支持两种证书模式：
- **公开证书**：需要域名，自动通过 Caddy 申请证书
- **自建证书**：无需域名，自动生成私有 CA

节点只需开放一个 TCP 端口（默认 8443）。

### 3. 使用客户端

下载 `tunnelx-desktop.exe`，登录账号，选择节点，连接。

| 模式 | 说明 |
|------|------|
| 绕过大陆 | 大陆域名和 IP 直连，其余走代理 |
| 全局代理 | 所有流量走代理 |
| TUN | 全局接管（需管理员权限 + runtime 文件夹） |

## 构建

需要 Go 1.27.0+、Node.js 18+、Wails v2、Python 3.10+。

```powershell
# 服务端 + 独立客户端（linux-amd64, linux-arm64, windows-amd64）
.\tools\build.ps1

# 桌面客户端
.\tools\build-desktop.ps1
```

所有构建使用 `-tags=http2legacy`。产物在 `dist/` 目录。

## 技术特性

| 项目 | 规格 |
|------|------|
| 传输协议 | HTTP/2 Extended CONNECT (TCP) + CONNECT-UDP Capsule (UDP) |
| 加密 | TLS 1.3, X25519, ECH 严格模式 |
| 证书 | 公开证书或自建 CA，客户端验证不安装到系统证书库 |
| 连接池 | 4 条 H2 连接，按需建立，120 秒温和轮换 |
| 路由 | 11 万条大陆域名 + 9,600 IP CIDR + 2.7 万强制代理域名 |
| 凭据保护 | Windows DPAPI 加密存储 |
| 伪装 | 未鉴权请求返回普通 HTTPS 页面 |
| 管理后台 | 支持自定义路径前缀，PostgreSQL 可选 |
| 支付 | 易支付（支付宝/微信）+ bepusdt（USDT） |

## 文档

- [完整搭建教程](docs/SETUP.md) — 管理面板、节点和客户端的详细部署步骤
- [依赖修改说明](THIRD_PARTY_PATCHES.md) — vendor 中的第三方代码修改记录
