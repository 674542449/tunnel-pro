#!/bin/bash
set -e

DOMAIN="${1:?用法: ./setup.sh <域名> [名称]}"
NAME="${2:-$DOMAIN}"

echo "============================="
echo "  一键部署代理服务端"
echo "  域名: $DOMAIN"
echo "============================="

# 1. 安装 Caddy
if ! command -v caddy &> /dev/null; then
    echo "[1/5] 安装 Caddy..."
    apt-get update -qq
    apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https curl ca-certificates
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
    apt-get update -qq
    apt-get install -y -qq caddy
else
    echo "[1/5] Caddy 已安装，跳过"
fi

# 2. 写 Caddyfile — 所有流量都转发给后端
echo "[2/5] 配置 Caddy..."
cat > /etc/caddy/Caddyfile << CADDYEOF
${DOMAIN} {
    reverse_proxy 127.0.0.1:8080
}
CADDYEOF

# 3. 放一个伪装页面
echo "[3/5] 创建伪装网站..."
mkdir -p /var/www/html
cat > /var/www/html/index.html << 'HTMLEOF'
<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>Welcome</title></head>
<body><h1>It works!</h1><p>This server is running normally.</p></body>
</html>
HTMLEOF

# 4. 部署服务端程序
echo "[4/5] 部署服务端..."
DEPLOY_DIR=/opt/tunnel
mkdir -p $DEPLOY_DIR
cp server-linux-arm64 $DEPLOY_DIR/tunnel-server
cp server.json $DEPLOY_DIR/server.json
chmod +x $DEPLOY_DIR/tunnel-server

cat > /etc/systemd/system/tunnel.service << 'SVCEOF'
[Unit]
Description=Tunnel Server
After=network.target

[Service]
ExecStart=/opt/tunnel/tunnel-server -c /opt/tunnel/server.json
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
SVCEOF

systemctl daemon-reload
systemctl enable tunnel
systemctl restart tunnel

# 5. 重启 Caddy
echo "[5/5] 启动 Caddy..."
systemctl restart caddy

# 检测 IP
IP=$(curl -s --max-time 5 ifconfig.me || hostname -I | awk '{print $1}')
PSK=$(python3 -c "import json; print(json.load(open('server.json'))['psk'])")

# 生成分享链接（不含 path，客户端会自动计算动态路径）
LINK_JSON="{\"name\":\"${NAME}\",\"addr\":\"${DOMAIN}\",\"ip\":\"${IP}\",\"psk\":\"${PSK}\"}"
LINK_B64=$(echo -n "$LINK_JSON" | base64 -w0 | tr '+/' '-_' | tr -d '=')

echo ""
echo "============================="
echo "  部署完成!"
echo "============================="
echo ""
echo "服务状态："
systemctl is-active tunnel && echo "  tunnel: 运行中" || echo "  tunnel: 未运行"
systemctl is-active caddy && echo "  caddy:  运行中" || echo "  caddy:  未运行"
echo ""
echo "─────────────────────────────"
echo "分享链接 (粘贴到管理器):"
echo ""
echo "tunnel://${LINK_B64}"
echo ""
echo "─────────────────────────────"
