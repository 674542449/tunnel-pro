# Tunnel Pro

基于 WebSocket over TLS 的安全代理系统，支持多节点管理、Web 后台、Windows 原生 GUI 客户端。

## 架构概览

```
┌──────────────┐     WSS (TLS)      ┌──────────────────┐
│  Windows GUI │ ◄═══════════════► │  Caddy (TLS终端)  │
│  (Wails App) │   uTLS Chrome指纹  │        ↓          │
│              │                    │  tunnel-server    │
│  SOCKS5/HTTP │                    │  (WebSocket 节点) │
│  127.0.0.1   │                    └──────────────────┘
│     :7890    │                             ↑ 心跳上报
└──────────────┘                    ┌──────────────────┐
                                    │  API Server      │
                                    │  (管理后台+用户API)│
                                    │  /admin 管理面板  │
                                    └──────────────────┘
```

## 核心特性

**协议层**
- WebSocket over TLS 传输，Caddy 自动 HTTPS 证书
- uTLS Chrome 浏览器指纹模拟，抗 TLS 指纹检测
- HMAC-SHA256 认证 + 时间戳防重放
- 动态 WebSocket 路径，每 30 分钟自动轮转
- 二进制帧协议：7 字节头部 `stream_id(4) + cmd(1) + pad_len(2)`，随机填充抗流量分析

**客户端**
- 混合端口（127.0.0.1:7890）：自动识别 SOCKS5 / HTTP 代理
- 连接池 + 自动重连健康检查（指数退避，最多 5 次）
- 系统代理自动设置/清除（Windows 注册表 + wininet.dll）
- 系统托盘：最小化到托盘、右键菜单（显示/断开/退出）
- 启动时自动检查 GitHub Release 新版本
- 实时上传/下载速度显示

**服务端**
- 多节点独立部署，心跳 + 流量上报到 API
- 伪装静态网站，非 WebSocket 请求返回正常网页
- systemd 服务管理，自动重启

**管理后台**
- 用户管理（注册/试用/续期/封禁）
- 节点管理（状态监控/在线人数/流量统计）
- 套餐 & 订单系统
- 一键部署命令生成
- Claude 风格 UI（暖白底色 + 橙棕强调色 + 深色侧边栏）

## 项目结构

```
tunnel-pro/
├── cmd/
│   ├── api/            # API 服务端 + 管理后台
│   │   └── main.go     # JWT 认证、用户/节点/套餐/订单 CRUD、内嵌 HTML 管理面板
│   ├── server/         # 节点服务端
│   │   └── main.go     # WebSocket 接入、mux 会话、心跳上报、流量统计
│   ├── gui/            # Windows GUI 客户端 (Wails v2)
│   │   ├── main.go     # Wails 入口配置
│   │   ├── app.go      # Go 后端（登录/连接/代理/速度/更新检查）
│   │   ├── tray.go     # 系统托盘（go:embed 内嵌图标）
│   │   └── frontend/   # Vue 3 + Vite 前端
│   ├── client/         # CLI 客户端（命令行版）
│   │   └── main.go
│   └── manager/        # TUI 管理客户端
│       └── main.go
├── internal/
│   ├── proto/          # 二进制帧协议、HMAC 认证、动态路径
│   ├── mux/            # 多路复用器（stream 管理）
│   ├── relay/          # 双向数据转发 + 流量计数
│   └── socks5/         # SOCKS5 协议处理
├── scripts/
│   └── tunnel-node.sh  # 节点一键部署脚本
├── Caddyfile.example   # Caddy 配置示例
├── server.example.json # 节点配置示例
└── client.example.json # 客户端配置示例
```

## 快速开始

### 1. 部署 API 服务端

```bash
# 下载
wget https://github.com/674542449/tunnel-pro/releases/latest/download/api-linux-amd64
chmod +x api-linux-amd64

# 创建配置
cat > api.json << 'EOF'
{
    "listen": "127.0.0.1:8081",
    "jwt_secret": "your-random-jwt-secret",
    "admin_user": "admin",
    "admin_pass": "your-admin-password",
    "report_key": "your-report-key"
}
EOF

# 运行
./api-linux-amd64 -c api.json
```

管理面板访问：`http://IP:8081/admin`

支持三种暴露方式：
- **直连**：`listen` 改为 `0.0.0.0:8081`
- **Nginx Proxy Manager**：反代到 `127.0.0.1:8081`
- **Cloudflare Tunnel**：`cloudflared tunnel --url http://127.0.0.1:8081`

### 2. 部署节点

使用一键脚本（详见 [tunnel-node](https://github.com/674542449/tunnel-node)）：

```bash
curl -fsSL https://raw.githubusercontent.com/674542449/tunnel-node/main/tunnel-node.sh | bash -s install \
  --domain node1.example.com \
  --psk YOUR_PSK \
  --api-url https://admin.example.com \
  --node-id 1 \
  --report-key YOUR_REPORT_KEY
```

或手动部署：

```bash
# 下载节点二进制
wget https://github.com/674542449/tunnel-pro/releases/latest/download/tunnel-server-linux-amd64
chmod +x tunnel-server-linux-amd64
mv tunnel-server-linux-amd64 /opt/tunnel/tunnel-server

# 配置
cat > /opt/tunnel/server.json << 'EOF'
{
    "listen": "127.0.0.1:8080",
    "psk": "your-pre-shared-key",
    "api_url": "https://admin.example.com",
    "node_id": 1,
    "report_key": "your-report-key"
}
EOF

# 安装 Caddy 并配置反代
# your-domain.com {
#     reverse_proxy 127.0.0.1:8080
# }
```

### 3. Windows GUI 客户端

从 [Releases](https://github.com/674542449/tunnel-pro/releases) 下载 `tunnel-pro.exe`，双击运行。

- 支持邮箱登录、游客登录（设备绑定）
- 试用激活后即可连接节点
- 混合代理端口 `127.0.0.1:7890`（同时支持 SOCKS5 和 HTTP）
- 连接后自动设置系统代理，断开后自动恢复

## 配置说明

### 节点配置 (server.json)

```json
{
    "listen": "127.0.0.1:8080",
    "psk": "预共享密钥（需与管理后台节点配置一致）",
    "web_root": "/var/www/html",
    "api_url": "https://admin.example.com",
    "node_id": 1,
    "report_key": "节点上报密钥"
}
```

### Caddy 配置 (Caddyfile)

```caddyfile
your-domain.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy 自动申请 Let's Encrypt TLS 证书，域名需提前解析到节点 IP。

## 从源码构建

### 前置条件

- Go 1.21+
- Node.js 18+ (GUI 前端)
- [Wails CLI](https://wails.io/) (GUI 构建)

### 构建命令

```bash
# API 服务端
go build -o api ./cmd/api

# 节点服务端
go build -o tunnel-server ./cmd/server

# CLI 客户端
go build -o client ./cmd/client

# GUI 客户端 (Windows)
cd cmd/gui && wails build

# 交叉编译 Linux (amd64/arm64)
GOOS=linux GOARCH=amd64 go build -o tunnel-server-linux-amd64 ./cmd/server
GOOS=linux GOARCH=arm64 go build -o tunnel-server-linux-arm64 ./cmd/server
```

## 技术栈

| 组件 | 技术 |
|------|------|
| 传输协议 | WebSocket over TLS |
| TLS 伪装 | uTLS (Chrome 指纹) |
| 认证 | HMAC-SHA256 + 时间戳防重放 |
| 多路复用 | 自定义二进制帧协议 |
| 节点服务端 | Go + gorilla/websocket |
| API 服务端 | Go net/http + JWT + bcrypt |
| GUI 客户端 | Wails v2 (Go + Vue 3 + Vite) |
| 系统托盘 | getlantern/systray (go:embed) |
| TLS 反代 | Caddy (自动 HTTPS) |
| 节点部署 | Bash 脚本 + systemd |

## Release 产物

| 文件 | 说明 |
|------|------|
| `tunnel-pro.exe` | Windows GUI 客户端 |
| `tunnel-server-linux-amd64` | 节点服务端 (x86_64) |
| `tunnel-server-linux-arm64` | 节点服务端 (ARM64) |
| `api-linux-amd64` | API 服务端 (x86_64) |
| `api-linux-arm64` | API 服务端 (ARM64) |

## License

MIT
