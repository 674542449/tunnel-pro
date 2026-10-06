#!/bin/sh
# tunnelX 管理面板一键安装（交互式）
# 用法: curl -fsSL https://raw.githubusercontent.com/674542449/tunnel-pro/master/deploy/quick-install-control.sh | bash
set -eu

# ── 基础检查 ──
test "$(id -u)" = 0 || { echo '请使用 root 执行' >&2; exit 1; }
. /etc/os-release
case "$ID" in ubuntu|debian) ;; *) echo '仅支持 Ubuntu / Debian' >&2; exit 1;; esac
case "$(uname -m)" in aarch64|arm64) arch=arm64;; x86_64) arch=amd64;; *) echo '不支持的 CPU 架构' >&2; exit 1;; esac

# ── 交互输入（兼容 curl | bash）──
ask() { printf '%s' "$1" >&2; read -r reply </dev/tty; echo "$reply"; }

echo ""
echo "╔══════════════════════════════════════════╗"
echo "║     tunnelX 管理面板一键安装              ║"
echo "║     架构: linux-$arch                      ║"
echo "╚══════════════════════════════════════════╝"
echo ""
echo "  请选择部署方式:"
echo ""
echo "  1) Cloudflare 隧道  ← 推荐，零端口暴露，零证书"
echo "  2) Nginx 反向代理     传统方式，需开放 443 端口"
echo "  3) 仅安装面板         不配置反代，手动处理"
echo ""
mode=$(ask "  请输入 [1/2/3]: ")
case "$mode" in
  1) mode=cf;;
  2) mode=nginx;;
  3) mode=direct;;
  *) echo "无效选择" >&2; exit 1;;
esac

# ── 根据模式收集信息 ──
domain=""
cf_token=""
path_prefix="/control"

if [ "$mode" = "cf" ]; then
  echo ""
  echo "  ── Cloudflare 隧道配置 ──"
  echo ""
  domain=$(ask "  输入域名（已托管在 Cloudflare，例: control.example.com）: ")
  echo ""
  echo "  请先在 Cloudflare 创建隧道："
  echo "  1. 登录 https://one.dash.cloudflare.com/"
  echo "  2. Networks → Tunnels → Create a tunnel"
  echo "  3. 选择 Cloudflared → 给隧道起个名字"
  echo "  4. 复制 Install connector 页面显示的 Token（ey... 开头的长字符串）"
  echo "  5. Public Hostname 配置："
  echo "     Domain: $domain"
  echo "     Service: http://localhost:18081"
  echo ""
  cf_token=$(ask "  粘贴 Tunnel Token: ")
  if [ -z "$cf_token" ]; then echo "Token 不能为空" >&2; exit 1; fi
  public_url="https://$domain"

elif [ "$mode" = "nginx" ]; then
  echo ""
  echo "  ── Nginx 反代配置 ──"
  echo ""
  domain=$(ask "  输入域名（已解析到本机 IP，例: example.com）: ")
  echo ""
  echo "  管理面板默认访问路径: https://$domain/control/"
  custom=$(ask "  要自定义路径吗？直接回车使用默认 /control，或输入新路径（如 /admin）: ")
  if [ -n "$custom" ]; then
    path_prefix=$(echo "$custom" | sed 's|^/*|/|;s|/*$||')
  fi
  public_url="https://$domain$path_prefix"

else
  echo ""
  echo "  面板将监听 127.0.0.1:18081，请自行配置反向代理。"
  echo ""
  public_url=$(ask "  输入 public_url（例: https://example.com/control，测试可用 http://127.0.0.1:18081）: ")
fi

repo=${TUNNELX_REPO:-674542449/tunnel-pro}

# ── 安装依赖 ──
echo ""
echo "  [1/5] 安装依赖 ..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq python3 curl ca-certificates >/dev/null

# BBR + fq 尽力开启：容器或旧内核不支持时保持系统默认，不影响安装。
enable_bbr() {
  conf=/etc/sysctl.d/99-tunnelx-bbr.conf
  modprobe tcp_bbr >/dev/null 2>&1 || true
  if ! grep -qw bbr /proc/sys/net/ipv4/tcp_available_congestion_control 2>/dev/null; then
    echo "        提示：当前内核不支持 BBR，保持系统默认拥塞控制"
    return 0
  fi
  { printf 'net.core.default_qdisc = fq\nnet.ipv4.tcp_congestion_control = bbr\n' >"$conf" && chmod 644 "$conf"; } 2>/dev/null || true
  if modinfo tcp_bbr >/dev/null 2>&1 && test -d /etc/modules-load.d; then
    { echo tcp_bbr >/etc/modules-load.d/tunnelx-bbr.conf && chmod 644 /etc/modules-load.d/tunnelx-bbr.conf; } 2>/dev/null || true
  fi
  sysctl -q -w net.core.default_qdisc=fq >/dev/null 2>&1 || true
  sysctl -q -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1 || true
  if test "$(cat /proc/sys/net/ipv4/tcp_congestion_control 2>/dev/null)" = bbr; then
    echo "        已开启 BBR + fq（重启后保持）"
  else
    echo "        提示：当前环境不允许修改拥塞控制（例如容器），保持系统默认"
  fi
}
enable_bbr

# ── 下载二进制 ──
echo "  [2/5] 下载最新版本 ..."
version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | python3 -c "import sys,json;print(json.load(sys.stdin)['tag_name'])")
base="https://github.com/$repo/releases/download/$version"
echo "        版本: $version"

dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cd "$dir"

curl -fSL --progress-bar -o tunnelx-control "$base/tunnelx-control-linux-$arch"
chmod +x tunnelx-control

for node_arch in arm64 amd64; do
  mkdir -p "node-artifacts/linux-$node_arch"
  for name in tunnelx-server tunnelx-admin; do
    curl -fSL --progress-bar -o "node-artifacts/linux-$node_arch/$name" "$base/$name-linux-$node_arch" 2>/dev/null || echo "        跳过 $name-linux-$node_arch（未在 Release 中找到）"
  done
done

# ── 安装管理面板 ──
echo "  [3/5] 安装管理面板 ..."
id tunnelx-control >/dev/null 2>&1 || useradd --system --home /nonexistent --shell /usr/sbin/nologin tunnelx-control
install -d -m 755 /opt/tunnelx/bin
install -d -m 750 -o root -g tunnelx-control /etc/tunnelx-control
install -d -m 700 -o tunnelx-control -g tunnelx-control /var/lib/tunnelx-control

if [ ! -f /etc/tunnelx-control/control.json ]; then
  ./tunnelx-control -init -config /etc/tunnelx-control/control.json -public-url "$public_url"
  python3 -c "
import json,pathlib
p=pathlib.Path('/etc/tunnelx-control/control.json')
c=json.loads(p.read_text())
c['data_file']='/var/lib/tunnelx-control/control.json'
p.write_text(json.dumps(c,indent=2)+'\n')
"
  chown root:tunnelx-control /etc/tunnelx-control/control.json
  chmod 640 /etc/tunnelx-control/control.json
  first_install=1
else
  python3 -c "
import json,pathlib,sys
c=json.loads(pathlib.Path('/etc/tunnelx-control/control.json').read_text())
stored=c['public_url'].rstrip('/')
given=sys.argv[1].rstrip('/')
if stored!=given:
    print('  检测到 public_url 变更: '+stored+' → '+given)
    c['public_url']=given
    pathlib.Path('/etc/tunnelx-control/control.json').write_text(json.dumps(c,indent=2)+'\n')
    print('  已更新 public_url')
" "$public_url"
  first_install=0
fi

install -m 755 tunnelx-control /opt/tunnelx/bin/tunnelx-control.installing
for node_arch in linux-arm64 linux-amd64; do
  install -d -m 755 "/opt/tunnelx/bin/node-artifacts/$node_arch"
  for name in tunnelx-server tunnelx-admin; do
    test -f "node-artifacts/$node_arch/$name" && install -m 644 "node-artifacts/$node_arch/$name" "/opt/tunnelx/bin/node-artifacts/$node_arch/$name"
  done
done

runuser -u tunnelx-control -- /opt/tunnelx/bin/tunnelx-control.installing -config /etc/tunnelx-control/control.json -check
if systemctl is-active --quiet tunnelx-control 2>/dev/null; then systemctl stop tunnelx-control; fi
mv /opt/tunnelx/bin/tunnelx-control.installing /opt/tunnelx/bin/tunnelx-control

# systemd 服务
cat >/etc/systemd/system/tunnelx-control.service <<'EOF'
[Unit]
Description=tunnelX Control Plane
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=tunnelx-control
Group=tunnelx-control
ExecStart=/opt/tunnelx/bin/tunnelx-control -config /etc/tunnelx-control/control.json
StateDirectory=tunnelx-control
StateDirectoryMode=0700
Restart=on-failure
RestartSec=3
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
LimitNOFILE=8192
MemoryMax=512M
[Install]
WantedBy=multi-user.target
EOF

# 备份
cat >/opt/tunnelx/bin/backup-control.py <<'PYEOF'
#!/usr/bin/env python3
import datetime,hashlib,json,os,pathlib,subprocess,sys,tempfile,time
def ws(parent,value):
    fd,name=tempfile.mkstemp(prefix='.bs-',dir=parent)
    try:
        with os.fdopen(fd,'w') as f: json.dump(value,f);f.flush();os.fsync(f.fileno())
        os.chmod(name,0o600);os.replace(name,parent/'backup-status.json')
    finally:
        if os.path.exists(name): os.unlink(name)
def run(cfg,key,d,b):
    s=json.loads(cfg.read_text());dp=pathlib.Path(s['data_file']);parent=(dp if dp.is_absolute() else cfg.parent/dp).parent
    d.mkdir(mode=0o700,parents=True,exist_ok=True)
    if not key.exists():
        fd=os.open(key,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        with os.fdopen(fd,'wb') as f: f.write(os.urandom(32));f.flush();os.fsync(f.fileno())
    t=d/(datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')+'.txbk')
    try:
        subprocess.run([str(b),'-config',str(cfg),'-backup',str(t),'-backup-key',str(key)],capture_output=True,timeout=120,check=True)
        for old in sorted(d.glob('*.txbk'),key=lambda p:p.stat().st_mtime_ns)[:-336]: old.unlink()
        ws(parent,dict(time=int(time.time()),bytes=t.stat().st_size,sha256=hashlib.sha256(t.read_bytes()).hexdigest()))
    except Exception:
        prev={};
        try: sv=json.loads((parent/'backup-status.json').read_text());prev={k:sv[k] for k in('time','bytes','sha256') if k in sv}
        except: pass
        prev['failed_at']=int(time.time())
        try: ws(parent,prev)
        except: pass
        raise SystemExit('备份失败')
if __name__=='__main__':
    run(pathlib.Path(sys.argv[1] if len(sys.argv)>1 else'/etc/tunnelx-control/control.json'),pathlib.Path('/etc/tunnelx-control/backup.key'),pathlib.Path('/var/backups/tunnelx-control'),pathlib.Path('/opt/tunnelx/bin/tunnelx-control'))
    print('备份完成')
PYEOF
chmod 755 /opt/tunnelx/bin/backup-control.py

cat >/etc/systemd/system/tunnelx-control-backup.service <<'EOF'
[Unit]
Description=tunnelX encrypted backup
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /opt/tunnelx/bin/backup-control.py
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
EOF

cat >/etc/systemd/system/tunnelx-control-backup.timer <<'EOF'
[Unit]
Description=tunnelX hourly backup
[Timer]
OnCalendar=hourly
Persistent=true
RandomizedDelaySec=120
[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now tunnelx-control.service
systemctl enable --now tunnelx-control-backup.timer

# ── 配置反向代理 ──
echo "  [4/5] 配置反向代理 ..."

if [ "$mode" = "cf" ]; then
  # ── Cloudflare 隧道 ──
  if ! command -v cloudflared >/dev/null 2>&1; then
    echo "        安装 cloudflared ..."
    curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | gpg --dearmor -o /usr/share/keyrings/cloudflare-main.gpg 2>/dev/null
    echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" > /etc/apt/sources.list.d/cloudflared.list
    apt-get update -qq
    apt-get install -y -qq cloudflared >/dev/null
  fi
  echo "        配置 Cloudflare 隧道 ..."
  cloudflared service install "$cf_token" 2>/dev/null || true
  systemctl enable --now cloudflared 2>/dev/null || true

elif [ "$mode" = "nginx" ]; then
  # ── Nginx + Let's Encrypt ──
  echo "        安装 Nginx + Certbot ..."
  apt-get install -y -qq nginx certbot python3-certbot-nginx >/dev/null

  echo "        申请 SSL 证书 ..."
  certbot --nginx -d "$domain" --non-interactive --agree-tos --register-unsolicited -m "admin@$domain" 2>&1 || {
    echo ""
    echo "  ⚠ 自动申请证书失败，请手动执行: certbot --nginx -d $domain"
    echo ""
  }

  echo "        配置 Nginx 反代 ..."
  # 找到 certbot 生成的 nginx 配置或默认配置
  nginx_conf="/etc/nginx/sites-available/$domain"
  if [ ! -f "$nginx_conf" ]; then
    nginx_conf="/etc/nginx/sites-enabled/default"
  fi

  # 写入独立的反代配置片段
  cat >"/etc/nginx/conf.d/tunnelx-control.conf" <<NGEOF
# tunnelX 管理面板反代 — 由安装脚本生成
# 在已有的 server{} 块中添加以下 location
# 如果 certbot 已创建了 server 块，请将 location 移入其中

upstream tunnelx_control {
    server 127.0.0.1:18081;
}
NGEOF

  # 尝试在 certbot 的 server 块中注入 location
  if [ -f "/etc/nginx/sites-available/$domain" ]; then
    # 检查是否已有 tunnelx location
    if ! grep -q "tunnelx_control" "/etc/nginx/sites-available/$domain" 2>/dev/null; then
      # 在最后一个 } 前插入 location 块
      python3 -c "
import pathlib,re
p=pathlib.Path('/etc/nginx/sites-available/$domain')
content=p.read_text()
location_block='''
    # tunnelX 管理面板
    location $path_prefix/ {
        proxy_pass http://tunnelx_control$path_prefix/;
        proxy_set_header Host \\\$host;
        proxy_set_header X-Real-IP \\\$remote_addr;
        proxy_set_header X-Forwarded-For \\\$remote_addr;
        proxy_set_header X-Forwarded-Proto https;
    }
'''
# 在 server 块的最后一个 } 前插入
parts=content.rsplit('}',1)
if len(parts)==2:
    content=parts[0]+location_block+'}'+parts[1]
    p.write_text(content)
    print('  已注入 location 到 Nginx 配置')
" 2>/dev/null || echo "        请手动添加 location 到 Nginx 配置"
    fi
  fi

  nginx -t 2>/dev/null && systemctl reload nginx || echo "  ⚠ Nginx 配置检查失败，请手动修复"

else
  echo "        跳过（直连模式）"
fi

# ── 完成 ──
echo "  [5/5] 验证服务状态 ..."
sleep 1
status=$(systemctl is-active tunnelx-control 2>/dev/null || echo "unknown")

admin_email=$(python3 -c "import json;print(json.load(open('/etc/tunnelx-control/control.json'))['admin_email'])" 2>/dev/null || echo "见配置文件")
admin_pass=$(python3 -c "import json;print(json.load(open('/etc/tunnelx-control/control.json'))['admin_password'])" 2>/dev/null || echo "见配置文件")

echo ""
echo "╔══════════════════════════════════════════╗"
echo "║           安装完成!                       ║"
echo "╚══════════════════════════════════════════╝"
echo ""
echo "  版本:       $version"
echo "  服务状态:   $status"
echo "  监听地址:   127.0.0.1:18081"
echo ""

if [ "${first_install:-0}" = "1" ]; then
  echo "  ┌─ 管理员账号 ─────────────────────────┐"
  echo "  │ 邮箱:  $admin_email"
  echo "  │ 密码:  $admin_pass"
  echo "  └───────────────────────────────────────┘"
  echo ""
fi

if [ "$mode" = "cf" ]; then
  echo "  部署方式: Cloudflare 隧道"
  echo "  访问地址: https://$domain/"
  echo ""
  echo "  确认 Cloudflare 隧道面板中 Public Hostname 已配置:"
  echo "    Domain:  $domain"
  echo "    Service: http://localhost:18081"
  echo ""

elif [ "$mode" = "nginx" ]; then
  echo "  部署方式: Nginx 反向代理"
  echo "  访问地址: https://$domain$path_prefix/"
  echo ""
  echo "  如果页面无法访问，检查:"
  echo "    1. 域名 DNS 是否已指向本机 IP"
  echo "    2. 防火墙是否放行 443 端口: ufw allow 443/tcp"
  echo "    3. Nginx 配置: cat /etc/nginx/sites-available/$domain"
  echo ""

else
  echo "  部署方式: 直连（无反代）"
  echo "  配置文件: /etc/tunnelx-control/control.json"
  echo "  请自行配置 HTTPS 反向代理后访问管理面板。"
  echo ""
fi

echo "  后续操作:"
echo "    查看日志:   journalctl -u tunnelx-control -f"
echo "    查看密码:   cat /etc/tunnelx-control/control.json"
echo "    升级面板:   重新执行本脚本即可"
echo ""
