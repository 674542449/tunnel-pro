#!/bin/bash
set -e

INSTALL_DIR="/opt/tunnel"
SERVICE_NAME="tunnel"
GITHUB_REPO="674542449/tunnel-pro"
CONFIG_FILE="$INSTALL_DIR/server.json"
CADDY_CONFIG="/etc/caddy/Caddyfile"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${GREEN}[OK]${NC} $1"; }
warn()  { echo -e "${YELLOW}[!]${NC} $1"; }
error() { echo -e "${RED}[X]${NC} $1"; }

detect_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) error "不支持的架构: $arch"; exit 1 ;;
    esac
}

install_caddy() {
    if command -v caddy &>/dev/null; then
        info "Caddy 已安装，跳过"
        return
    fi
    info "正在安装 Caddy..."
    apt-get update -qq
    apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https curl ca-certificates
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg 2>/dev/null
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
    apt-get update -qq
    apt-get install -y -qq caddy
    info "Caddy 安装完成"
}

write_caddyfile() {
    local domain="$1"
    cat > "$CADDY_CONFIG" <<CEOF
${domain} {
    reverse_proxy 127.0.0.1:8080
}
CEOF
    info "Caddy 配置已写入 ($domain)"
}

write_server_config() {
    local psk="$1" api_url="$2" node_id="$3" report_key="$4"
    mkdir -p "$INSTALL_DIR"
    cat > "$CONFIG_FILE" <<JEOF
{
    "listen": "127.0.0.1:8080",
    "psk": "${psk}",
    "web_root": "/var/www/html",
    "api_url": "${api_url}",
    "node_id": ${node_id},
    "report_key": "${report_key}"
}
JEOF
    info "节点配置已写入"
}

create_service() {
    cat > /etc/systemd/system/${SERVICE_NAME}.service <<SEOF
[Unit]
Description=Tunnel Node Server
After=network.target

[Service]
ExecStart=${INSTALL_DIR}/tunnel-server -c ${INSTALL_DIR}/server.json
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SEOF
    systemctl daemon-reload
    systemctl enable "$SERVICE_NAME" >/dev/null 2>&1
    info "系统服务已创建"
}

create_camouflage_page() {
    mkdir -p /var/www/html
    cat > /var/www/html/index.html <<'HEOF'
<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>Welcome</title></head>
<body><h1>It works!</h1><p>Server is running normally.</p></body>
</html>
HEOF
}

do_install() {
    local domain="" psk="" api_url="" node_id="" report_key=""

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --domain|-d)  domain="$2"; shift 2 ;;
            --psk|-p)     psk="$2"; shift 2 ;;
            --api-url|--api|-a) api_url="$2"; shift 2 ;;
            --node-id|--id|-n)  node_id="$2"; shift 2 ;;
            --report-key|--key|-k) report_key="$2"; shift 2 ;;
            *) shift ;;
        esac
    done

    if [ -z "$domain" ]; then
        echo -e "${CYAN}=== 节点安装向导 ===${NC}"
        echo ""
        read -rp "域名 (如 node1.example.com): " domain
        read -rp "PSK 密钥: " psk
        read -rp "API 地址 (如 https://admin.example.com): " api_url
        read -rp "节点 ID: " node_id
        read -rp "上报密钥: " report_key
    fi

    if [ -z "$domain" ] || [ -z "$psk" ] || [ -z "$node_id" ]; then
        error "缺少必填参数: 域名、PSK、节点ID"
        exit 1
    fi

    local arch
    arch=$(detect_arch)
    info "架构: linux/$arch"

    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    rm -f "$INSTALL_DIR/tunnel-server"

    local url="https://github.com/${GITHUB_REPO}/releases/latest/download/tunnel-server-linux-${arch}"
    info "正在下载节点程序..."
    mkdir -p "$INSTALL_DIR"
    if ! curl -fSL -o "$INSTALL_DIR/tunnel-server" "$url"; then
        error "下载失败，请检查网络"
        exit 1
    fi
    chmod +x "$INSTALL_DIR/tunnel-server"
    info "下载完成"

    install_caddy

    write_caddyfile "$domain"
    write_server_config "$psk" "$api_url" "$node_id" "$report_key"
    create_camouflage_page

    create_service
    systemctl restart caddy
    systemctl restart "$SERVICE_NAME"

    local script_url="https://raw.githubusercontent.com/${GITHUB_REPO}/master/scripts/tunnel-node.sh"
    curl -fsSL -o "$INSTALL_DIR/manage.sh" "$script_url" 2>/dev/null && chmod +x "$INSTALL_DIR/manage.sh"
    ln -sf "$INSTALL_DIR/manage.sh" /usr/local/bin/tunnel 2>/dev/null || true

    sleep 2
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        echo ""
        info "安装完成，节点已运行"
        echo ""
        echo -e "  管理命令: ${CYAN}tunnel${NC}"
        echo ""
    else
        error "服务启动失败，请查看日志: journalctl -u $SERVICE_NAME -n 20"
    fi
}

do_start() {
    systemctl start "$SERVICE_NAME"
    info "服务已启动"
}

do_stop() {
    systemctl stop "$SERVICE_NAME"
    info "服务已停止"
}

do_restart() {
    systemctl restart "$SERVICE_NAME"
    info "服务已重启"
}

do_status() {
    echo -e "${CYAN}── 节点状态 ──${NC}"
    systemctl status "$SERVICE_NAME" --no-pager -l 2>/dev/null || warn "服务未找到"
    echo ""
    echo -e "${CYAN}── Caddy 状态 ──${NC}"
    systemctl is-active caddy && echo -e "${GREEN}Caddy: 运行中${NC}" || echo -e "${RED}Caddy: 已停止${NC}"
    echo ""
    echo -e "${CYAN}── 最近日志 ──${NC}"
    journalctl -u "$SERVICE_NAME" --no-pager -n 20 -o short 2>/dev/null || true
}

do_update() {
    local arch
    arch=$(detect_arch)
    local url="https://github.com/${GITHUB_REPO}/releases/latest/download/tunnel-server-linux-${arch}"
    info "正在下载最新版本..."
    if ! curl -fSL -o "$INSTALL_DIR/tunnel-server.new" "$url"; then
        error "下载失败"
        return
    fi
    chmod +x "$INSTALL_DIR/tunnel-server.new"
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    mv "$INSTALL_DIR/tunnel-server.new" "$INSTALL_DIR/tunnel-server"
    systemctl start "$SERVICE_NAME"
    info "更新完成，服务已重启"

    local script_url="https://raw.githubusercontent.com/${GITHUB_REPO}/master/scripts/tunnel-node.sh"
    curl -fsSL -o "$INSTALL_DIR/manage.sh" "$script_url" 2>/dev/null && chmod +x "$INSTALL_DIR/manage.sh"
}

do_enable_bbr() {
    info "正在开启 BBR 拥塞控制..."
    if ! grep -q "net.core.default_qdisc=fq" /etc/sysctl.conf 2>/dev/null; then
        cat >> /etc/sysctl.conf <<'BEOF'
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
BEOF
        sysctl -p >/dev/null 2>&1
    fi
    local cc
    cc=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)
    if [ "$cc" = "bbr" ]; then
        info "BBR 已开启 (当前: $cc)"
    else
        warn "BBR 需要内核 >= 4.9，当前: $(uname -r)"
    fi
}

do_open_firewall() {
    info "正在放行 80/443 端口..."
    if command -v ufw &>/dev/null; then
        ufw allow 80/tcp >/dev/null 2>&1
        ufw allow 80/udp >/dev/null 2>&1
        ufw allow 443/tcp >/dev/null 2>&1
        ufw allow 443/udp >/dev/null 2>&1
        info "UFW 规则已添加"
    else
        iptables -I INPUT -p tcp --dport 80 -j ACCEPT 2>/dev/null
        iptables -I INPUT -p udp --dport 80 -j ACCEPT 2>/dev/null
        iptables -I INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null
        iptables -I INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null
        ip6tables -I INPUT -p tcp --dport 80 -j ACCEPT 2>/dev/null
        ip6tables -I INPUT -p udp --dport 80 -j ACCEPT 2>/dev/null
        ip6tables -I INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null
        ip6tables -I INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null
        info "防火墙规则已添加"
    fi
}

do_uninstall() {
    echo -e "${RED}即将完全卸载节点，此操作不可恢复！${NC}"
    read -rp "确认卸载？[y/N]: " confirm
    if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
        info "已取消"
        return
    fi

    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl disable "$SERVICE_NAME" 2>/dev/null || true
    rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
    systemctl daemon-reload
    rm -rf "$INSTALL_DIR"
    rm -f /usr/local/bin/tunnel
    info "节点已卸载"

    read -rp "同时卸载 Caddy？[y/N]: " remove_caddy
    if [[ "$remove_caddy" == "y" || "$remove_caddy" == "Y" ]]; then
        systemctl stop caddy 2>/dev/null || true
        apt-get remove -y caddy 2>/dev/null || true
        info "Caddy 已卸载"
    fi
}

get_node_version() {
    if [ -x "$INSTALL_DIR/tunnel-server" ]; then
        "$INSTALL_DIR/tunnel-server" -version 2>/dev/null || echo "未知"
    else
        echo "未安装"
    fi
}

show_menu() {
    while true; do
        local ver
        ver=$(get_node_version)
        echo ""
        echo -e "${CYAN}╔════════════════════════╗${NC}"
        echo -e "${CYAN}║     节点管理面板       ║${NC}"
        echo -e "${CYAN}╚════════════════════════╝${NC}"
        echo -e "  当前版本: ${GREEN}${ver}${NC}"
        echo ""
        echo "  1) 安装 / 重装"
        echo "  2) 启动"
        echo "  3) 停止"
        echo "  4) 重启"
        echo "  5) 状态 & 日志"
        echo "  6) 更新节点"
        echo "  7) 开启 BBR"
        echo "  8) 放行防火墙"
        echo "  9) 卸载"
        echo "  0) 退出"
        echo ""
        read -rp "请选择 [0-9]: " choice
        case "$choice" in
            1) do_install ;;
            2) do_start ;;
            3) do_stop ;;
            4) do_restart ;;
            5) do_status ;;
            6) do_update ;;
            7) do_enable_bbr ;;
            8) do_open_firewall ;;
            9) do_uninstall ;;
            0) echo "再见!"; exit 0 ;;
            *) warn "无效选项" ;;
        esac
    done
}

case "${1:-}" in
    install) shift; do_install "$@" ;;
    start)   do_start ;;
    stop)    do_stop ;;
    restart) do_restart ;;
    status)  do_status ;;
    update)  do_update ;;
    bbr)     do_enable_bbr ;;
    firewall) do_open_firewall ;;
    uninstall) do_uninstall ;;
    menu|"")  show_menu ;;
    *) echo "用法: $0 {install|start|stop|restart|status|update|bbr|firewall|uninstall|menu}"; exit 1 ;;
esac
