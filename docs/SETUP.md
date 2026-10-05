# tunnelX 从零搭建教程

本教程覆盖管理面板、节点和客户端的完整搭建流程。假设你有：

- 一台用于管理面板的 VPS（Ubuntu / Debian，可与节点同机或分开部署）
- 一台或多台用于节点的 VPS（Ubuntu / Debian，推荐海外）
- 一个域名（用于管理面板 HTTPS；节点可选择域名或自建证书免域名）
- 本地 Windows 电脑（用于构建和使用客户端）

---

## 一、部署管理面板（控制台）

管理面板负责用户注册/登录、套餐管理、订单支付、节点管理等。

### 1.1 准备服务器

```bash
apt update && apt install -y python3 nginx certbot python3-certbot-nginx
```

### 1.2 上传管理面板二进制

在本地 Windows 构建完成后（见第三节），将以下文件上传到服务器：

```
tunnelx-control-linux-arm64    # 或 amd64，取决于服务器架构
deploy/install-control.sh
deploy/tunnelx-control.service
deploy/tunnelx-control-backup.service
deploy/tunnelx-control-backup.timer
deploy/backup-control.py
```

也可以直接从 GitHub Release 下载：

```bash
# ARM64 服务器
wget https://github.com/674542449/tunnel-pro/releases/download/v0.6.17/tunnelx-control-linux-arm64
chmod +x tunnelx-control-linux-arm64

# 放到安装目录结构中
mkdir -p linux-arm64 deploy
mv tunnelx-control-linux-arm64 linux-arm64/tunnelx-control
```

### 1.3 安装管理面板

```bash
sudo sh deploy/install-control.sh https://你的域名/control
```

安装器会：
- 创建 `tunnelx-control` 系统用户
- 在 `/etc/tunnelx-control/control.json` 生成私有配置（含随机管理员密码）
- 安装 systemd 服务，监听 `127.0.0.1:18081`
- 启用每小时加密备份

### 1.4 配置 Nginx 反向代理

```nginx
server {
    listen 443 ssl http2;
    server_name 你的域名;

    ssl_certificate     /etc/letsencrypt/live/你的域名/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/你的域名/privkey.pem;

    location /control/ {
        proxy_pass http://127.0.0.1:18081/;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
    }
}
```

申请证书：

```bash
certbot --nginx -d 你的域名
```

### 1.5 获取管理员密码

```bash
sudo cat /etc/tunnelx-control/control.json
```

找到 `admin_email` 和 `admin_password` 字段，用它们登录管理面板：`https://你的域名/control/`

### 1.6 可选：启用 PostgreSQL

默认使用文件存储。如需 PostgreSQL（推荐生产使用）：

```bash
apt install -y postgresql
sudo -u postgres psql -c "CREATE USER tunnelx WITH PASSWORD '你的密码';"
sudo -u postgres psql -c "CREATE DATABASE tunnelx OWNER tunnelx;"
```

编辑 `/etc/tunnelx-control/control.json`，添加：

```json
{
  "database_url": "postgres://tunnelx:你的密码@127.0.0.1:5432/tunnelx"
}
```

重启服务：

```bash
sudo systemctl restart tunnelx-control
```

---

## 二、部署节点

节点是实际承载代理流量的服务端。支持两种证书模式。

### 方式 A：公开证书模式（需要域名）

1. 将域名 A 记录指向节点服务器 IP
2. 在节点服务器安装 Caddy（用于自动申请证书）：

```bash
apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install -y caddy python3 openssl
```

3. 在管理面板 → 节点管理 → 添加节点，填写：
   - 名称：例如 `韩国首尔`
   - 地区：例如 `kr`
   - IP：节点公网 IP
   - 端口：`8443`
   - 域名：指向该节点的域名
   - 证书模式：公开证书

4. 生成的安装命令复制到节点服务器执行即可。

### 方式 B：自建证书模式（无需域名）

1. 在管理面板 → 节点管理 → 添加节点，填写：
   - 名称、地区、IP、端口
   - 证书模式：自建证书

2. 同样复制生成的安装命令到节点服务器执行。

此模式会自动创建私有 CA 和证书，不需要 DNS、不需要 Caddy、不需要开放 80/443。只需开放所填的节点端口（如 8443/TCP）。

### 节点安装完成后验证

```bash
systemctl status tunnelx           # 查看服务状态
journalctl -u tunnelx --no-pager   # 查看日志
```

### 防火墙

只需开放节点端口（默认 8443/TCP）：

```bash
# ufw
ufw allow 8443/tcp

# iptables
iptables -A INPUT -p tcp --dport 8443 -j ACCEPT
```

### 可选：开启 BBR

```bash
echo "net.core.default_qdisc=fq" >> /etc/sysctl.conf
echo "net.ipv4.tcp_congestion_control=bbr" >> /etc/sysctl.conf
sysctl -p
```

---

## 三、构建

### 3.1 环境要求

| 工具 | 版本 | 用途 |
|------|------|------|
| Go | 1.27.0+ | 编译所有 Go 代码 |
| Node.js | 18+ | 桌面前端 JS 检查 |
| Wails | v2 | 桌面客户端构建 |
| Python | 3.10+ | 构建脚本和打包 |

### 3.2 构建服务端 + 独立客户端

```powershell
.\tools\build.ps1
```

产物在 `dist/` 目录：
- `dist/linux-arm64/tunnelx-server` — ARM64 节点服务端
- `dist/linux-amd64/tunnelx-server` — x86_64 节点服务端
- `dist/linux-*/tunnelx-control` — 管理面板
- `dist/windows-amd64/tunnelx-client.exe` — 独立配置版客户端

### 3.3 构建桌面客户端

```powershell
.\tools\build-desktop.ps1
```

产物：`dist/desktop-v0.6.17/tunnelx-desktop.exe`

---

## 四、使用客户端

### 4.1 桌面客户端（推荐）

1. 下载 `tunnelx-desktop.exe` 放到任意目录
2. 如需 TUN 模式，将 `runtime/` 文件夹（含 `wintun.dll` 和 `tun2socks.exe`）放在 exe 同级目录
3. 双击运行，登录你在管理面板注册的账号
4. 选择节点 → 连接

**代理模式：**

| 模式 | 说明 |
|------|------|
| 绕过大陆 | 大陆域名和 IP 直连，其余走代理（推荐日常使用） |
| 全局代理 | 所有流量走代理 |
| TUN | 全局接管系统流量（需管理员权限） |

**本地代理端口：**
- SOCKS5：`127.0.0.1:1080`
- HTTP：`127.0.0.1:8088`

系统代理开启后，浏览器等遵循系统代理的应用自动走代理。其他应用可手动设置 SOCKS5 或 HTTP 代理地址。

### 4.2 独立配置版客户端

适用于不需要账号系统、直接用配置文件连接的场景：

```powershell
.\tunnelx-client.exe -config .\client.json
```

配置文件 `client.json` 示例：

```json
{
  "server_name": "节点域名",
  "server_ip": "节点IP",
  "port": 8443,
  "token": "服务端配置中的token",
  "privacy": "strict",
  "ech_config": "ECH配置的base64",
  "socks_listen": "127.0.0.1:1080",
  "http_listen": "127.0.0.1:8088",
  "web_listen": "127.0.0.1:9080"
}
```

如使用自建证书模式，额外添加：

```json
{
  "ca_file": "origin-ca.pem"
}
```

---

## 五、管理面板功能一览

登录管理面板 `https://你的域名/control/` 后可使用：

- **仪表盘**：在线节点数、注册用户数、活跃连接
- **用户管理**：查看/封禁/解封用户
- **节点管理**：添加/删除节点、查看心跳状态、一键安装命令
- **套餐管理**：创建/编辑套餐（流量、时长、价格）
- **订单管理**：查看订单、退款
- **支付设置**：配置支付宝/微信（易支付）、USDT（bepusdt）
- **操作审计**：完整操作日志

---

## 六、升级

### 升级节点

在管理面板生成新的安装命令，在节点服务器重新执行即可。服务会自动重启，活跃连接由客户端重建。

### 升级管理面板

```bash
# 上传新版 tunnelx-control 二进制
sudo systemctl stop tunnelx-control
sudo cp tunnelx-control /opt/tunnelx/bin/tunnelx-control
sudo systemctl start tunnelx-control
```

### 升级客户端

下载新版 `tunnelx-desktop.exe` 替换旧版即可。退出旧版后再替换。

---

## 七、常见问题

### 客户端连接失败

1. 确认节点服务正在运行：`systemctl status tunnelx`
2. 确认防火墙已开放节点端口
3. 确认域名已正确指向节点 IP（公开证书模式）
4. 在管理面板检查节点心跳状态

### TUN 模式无法启动

- 需要以管理员身份运行客户端
- 确认 `runtime/wintun.dll` 和 `runtime/tun2socks.exe` 在 exe 同级目录

### 绕过大陆模式下某些国内网站走了代理

绕过模式使用 11 万条大陆域名 + 9,600 条 IP 段规则。如有遗漏，可在设置 → 直连例外中手动添加域名或 IP。

### 忘记管理员密码

```bash
sudo cat /etc/tunnelx-control/control.json | python3 -c "import sys,json;print(json.load(sys.stdin)['admin_password'])"
```
