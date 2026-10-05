# tunnelX 从零搭建教程

本教程覆盖管理面板、节点和客户端的完整搭建流程。

**你需要：**
- 一台 VPS（Ubuntu / Debian）用于管理面板
- 一台或多台海外 VPS 用于节点
- 一个域名（管理面板 HTTPS 必需；节点可选，自建证书模式不需要）
- 一台 Windows 电脑（使用客户端）

---

## 一、部署管理面板

### 一键安装（推荐）

```bash
curl -fsSL https://raw.githubusercontent.com/674542449/tunnel-pro/master/deploy/quick-install-control.sh | bash
```

脚本会交互式引导你完成所有步骤：

```
  请选择部署方式:

  1) Cloudflare 隧道  ← 推荐，零端口暴露，零证书
  2) Nginx 反向代理     传统方式，需开放 443 端口
  3) 仅安装面板         不配置反代，手动处理
```

**选 1（Cloudflare 隧道）：** 需要域名托管在 Cloudflare。脚本会提示你去 CF 面板创建隧道、粘贴 Token，然后自动安装 cloudflared 并启动。无需开放任何端口，无需申请证书。

**选 2（Nginx 反代）：** 脚本自动安装 Nginx + Certbot，申请 Let's Encrypt 证书，配置反向代理。需要域名 DNS 已指向服务器 IP，防火墙放行 443 端口。

**选 3（仅安装面板）：** 只装管理面板，反代自己搞。适合有经验的用户。

安装完成后屏幕会显示管理员邮箱和密码。升级时再次执行同一命令即可，已有配置自动保留。

### 自定义路径

管理面板路径完全可自定义。安装时 Nginx 模式会询问路径（默认 `/control`），你可以改成 `/admin` 或任何前缀。

已安装后想改路径：

1. 修改 `/etc/tunnelx-control/control.json` 中的 `public_url`
2. 修改 Nginx 配置中的 `location` 和 `proxy_pass`
3. 重启：`systemctl restart tunnelx-control && systemctl reload nginx`

### 手动安装

从源码构建后手动部署：

```bash
sudo sh deploy/install-control.sh https://你的域名/control
```

### 可选：启用 PostgreSQL

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

节点是实际承载代理流量的服务端。在管理面板添加节点后，会生成一行安装命令。

### 步骤

1. 登录管理面板 → 节点管理 → 添加节点
2. 填写：名称、地区、IP、端口（默认 8443）、证书模式
3. 点击生成安装命令
4. SSH 到节点服务器，粘贴执行

### 证书模式

| 模式 | 需要域名 | 需要 Caddy | 说明 |
|------|---------|-----------|------|
| 公开证书 | 是 | 是 | 域名 A 记录指向节点 IP，Caddy 自动申请 Let's Encrypt 证书 |
| 自建证书 | 否 | 否 | 自动生成私有 CA 和证书，零依赖 |

自建证书模式只需开放节点端口，不需要 DNS、不需要 Caddy、不需要 80/443。

### 安装后验证

```bash
systemctl status tunnelx
journalctl -u tunnelx --no-pager -n 20
```

### 防火墙

只需开放节点端口（默认 8443/TCP）：

```bash
ufw allow 8443/tcp
```

### 可选：开启 BBR

```bash
echo "net.core.default_qdisc=fq" >> /etc/sysctl.conf
echo "net.ipv4.tcp_congestion_control=bbr" >> /etc/sysctl.conf
sysctl -p
```

---

## 三、使用客户端

### 桌面客户端（推荐）

1. 从 [GitHub Release](https://github.com/674542449/tunnel-pro/releases/latest) 下载 `tunnelx-desktop.exe`
2. 如需 TUN 模式，将 `runtime/` 文件夹（含 `wintun.dll` 和 `tun2socks.exe`）放在 exe 同目录
3. 双击运行 → 登录 → 选择节点 → 连接

**代理模式：**

| 模式 | 说明 |
|------|------|
| 绕过大陆 | 大陆域名和 IP 直连，其余走代理（日常推荐） |
| 全局代理 | 所有流量走代理 |
| TUN | 全局接管系统流量（需管理员权限） |

**本地端口：** SOCKS5 `127.0.0.1:1080`，HTTP `127.0.0.1:8088`

### 独立配置版客户端

不需要账号系统，直接用配置文件连接：

```powershell
.\tunnelx-client.exe -config .\client.json
```

---

## 四、升级

### 升级管理面板

重新执行一键安装脚本即可，已有配置自动保留：

```bash
curl -fsSL https://raw.githubusercontent.com/674542449/tunnel-pro/master/deploy/quick-install-control.sh | bash
```

### 升级节点

在管理面板重新生成安装命令，到节点服务器执行即可。

### 升级客户端

下载新版 `tunnelx-desktop.exe` 替换旧版即可（先退出旧版）。

---

## 五、常见问题

**客户端连接失败**
1. 确认节点服务运行：`systemctl status tunnelx`
2. 确认防火墙已放行节点端口
3. 在管理面板检查节点心跳状态

**TUN 模式无法启动**
- 需要管理员身份运行客户端
- 确认 `runtime/wintun.dll` 和 `runtime/tun2socks.exe` 在 exe 同目录

**忘记管理员密码**
```bash
sudo cat /etc/tunnelx-control/control.json | python3 -c "import sys,json;print(json.load(sys.stdin)['admin_password'])"
```
