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

info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; }

detect_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) error "Unsupported architecture: $arch"; exit 1 ;;
    esac
}

install_caddy() {
    if command -v caddy &>/dev/null; then
        info "Caddy already installed, skipping"
        return
    fi
    info "Installing Caddy..."
    apt-get update -qq
    apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https curl ca-certificates
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg 2>/dev/null
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
    apt-get update -qq
    apt-get install -y -qq caddy
    info "Caddy installed"
}

write_caddyfile() {
    local domain="$1"
    cat > "$CADDY_CONFIG" <<CEOF
${domain} {
    reverse_proxy 127.0.0.1:8080
}
CEOF
    info "Caddyfile written for $domain"
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
    info "Server config written"
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
    info "Systemd service created"
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

    # Parse command-line args
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

    # Interactive fallback
    if [ -z "$domain" ]; then
        echo -e "${CYAN}=== Tunnel Node Installation ===${NC}"
        read -rp "Domain (e.g. node1.example.com): " domain
        read -rp "PSK (pre-shared key): " psk
        read -rp "API URL (e.g. https://admin.example.com): " api_url
        read -rp "Node ID: " node_id
        read -rp "Report Key: " report_key
    fi

    if [ -z "$domain" ] || [ -z "$psk" ] || [ -z "$node_id" ]; then
        error "Missing required parameters: domain, psk, node_id"
        exit 1
    fi

    local arch
    arch=$(detect_arch)
    info "Architecture: linux/$arch"

    # Stop existing service
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    rm -f "$INSTALL_DIR/tunnel-server"

    # Download binary
    local url="https://github.com/${GITHUB_REPO}/releases/latest/download/tunnel-server-linux-${arch}"
    info "Downloading from $url ..."
    mkdir -p "$INSTALL_DIR"
    if ! curl -fSL -o "$INSTALL_DIR/tunnel-server" "$url"; then
        error "Download failed! Check network and GitHub repo."
        exit 1
    fi
    chmod +x "$INSTALL_DIR/tunnel-server"
    info "Binary downloaded"

    # Install Caddy
    install_caddy

    # Write configs
    write_caddyfile "$domain"
    write_server_config "$psk" "$api_url" "$node_id" "$report_key"
    create_camouflage_page

    # Create and start services
    create_service
    systemctl restart caddy
    systemctl restart "$SERVICE_NAME"

    # Save this script locally for future management
    local script_url="https://raw.githubusercontent.com/${GITHUB_REPO}/master/scripts/tunnel-node.sh"
    curl -fsSL -o "$INSTALL_DIR/manage.sh" "$script_url" 2>/dev/null && chmod +x "$INSTALL_DIR/manage.sh"
    ln -sf "$INSTALL_DIR/manage.sh" /usr/local/bin/tunnel 2>/dev/null || true

    sleep 2
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        info "Installation complete! Service is running."
        echo ""
        info "Management: run ${CYAN}tunnel${NC} to open the menu"
    else
        error "Service failed to start. Check: journalctl -u $SERVICE_NAME -n 20"
    fi
}

do_start() {
    systemctl start "$SERVICE_NAME"
    info "Service started"
}

do_stop() {
    systemctl stop "$SERVICE_NAME"
    info "Service stopped"
}

do_restart() {
    systemctl restart "$SERVICE_NAME"
    info "Service restarted"
}

do_status() {
    echo -e "${CYAN}=== Service Status ===${NC}"
    systemctl status "$SERVICE_NAME" --no-pager -l 2>/dev/null || warn "Service not found"
    echo ""
    echo -e "${CYAN}=== Caddy Status ===${NC}"
    systemctl is-active caddy && echo -e "${GREEN}Caddy: running${NC}" || echo -e "${RED}Caddy: stopped${NC}"
    echo ""
    echo -e "${CYAN}=== Recent Logs (last 20 lines) ===${NC}"
    journalctl -u "$SERVICE_NAME" --no-pager -n 20 -o short 2>/dev/null || true
}

do_enable_bbr() {
    info "Enabling BBR congestion control + fq qdisc..."
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
        info "BBR enabled successfully (congestion control: $cc)"
    else
        warn "BBR may require a kernel >= 4.9. Current: $(uname -r)"
    fi
}

do_open_firewall() {
    info "Opening firewall ports 80/443 (TCP+UDP)..."
    if command -v ufw &>/dev/null; then
        ufw allow 80/tcp >/dev/null 2>&1
        ufw allow 80/udp >/dev/null 2>&1
        ufw allow 443/tcp >/dev/null 2>&1
        ufw allow 443/udp >/dev/null 2>&1
        info "UFW rules added"
    else
        iptables -I INPUT -p tcp --dport 80 -j ACCEPT 2>/dev/null
        iptables -I INPUT -p udp --dport 80 -j ACCEPT 2>/dev/null
        iptables -I INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null
        iptables -I INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null
        ip6tables -I INPUT -p tcp --dport 80 -j ACCEPT 2>/dev/null
        ip6tables -I INPUT -p udp --dport 80 -j ACCEPT 2>/dev/null
        ip6tables -I INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null
        ip6tables -I INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null
        info "iptables/ip6tables rules added"
    fi
}

do_uninstall() {
    echo -e "${RED}This will remove the tunnel node completely.${NC}"
    read -rp "Are you sure? [y/N]: " confirm
    if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
        info "Cancelled"
        return
    fi

    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl disable "$SERVICE_NAME" 2>/dev/null || true
    rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
    systemctl daemon-reload
    rm -rf "$INSTALL_DIR"
    info "Tunnel node removed"

    read -rp "Also remove Caddy? [y/N]: " remove_caddy
    if [[ "$remove_caddy" == "y" || "$remove_caddy" == "Y" ]]; then
        systemctl stop caddy 2>/dev/null || true
        apt-get remove -y caddy 2>/dev/null || true
        info "Caddy removed"
    fi
}

show_menu() {
    while true; do
        echo ""
        echo -e "${CYAN}╔══════════════════════════════╗${NC}"
        echo -e "${CYAN}║    Tunnel Node Management    ║${NC}"
        echo -e "${CYAN}╚══════════════════════════════╝${NC}"
        echo ""
        echo "  1) Install / Reinstall"
        echo "  2) Start"
        echo "  3) Stop"
        echo "  4) Restart"
        echo "  5) Status & Logs"
        echo "  6) Enable BBR + FQ"
        echo "  7) Open Firewall (80/443)"
        echo "  8) Uninstall"
        echo "  0) Exit"
        echo ""
        read -rp "Choose [0-8]: " choice
        case "$choice" in
            1) do_install ;;
            2) do_start ;;
            3) do_stop ;;
            4) do_restart ;;
            5) do_status ;;
            6) do_enable_bbr ;;
            7) do_open_firewall ;;
            8) do_uninstall ;;
            0) echo "Bye!"; exit 0 ;;
            *) warn "Invalid choice" ;;
        esac
    done
}

# Entry point: CLI arg mode or interactive menu
case "${1:-}" in
    install) shift; do_install "$@" ;;
    start)   do_start ;;
    stop)    do_stop ;;
    restart) do_restart ;;
    status)  do_status ;;
    bbr)     do_enable_bbr ;;
    firewall) do_open_firewall ;;
    uninstall) do_uninstall ;;
    menu|"")  show_menu ;;
    *) echo "Usage: $0 {install|start|stop|restart|status|bbr|firewall|uninstall|menu}"; exit 1 ;;
esac
