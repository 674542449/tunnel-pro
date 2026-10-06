#!/usr/bin/env bash
# Per-node installation on Ubuntu/Debian. Keys are generated on this server.
set -Eeuo pipefail
umask 077
test "$(id -u)" = 0 || { echo '请使用 root 执行安装命令。' >&2; exit 1; }
api=${1:?缺少管理地址}; token=${2:?缺少限时安装凭据}
[[ "$token" =~ ^[A-Za-z0-9_-]{43}$ ]] || { echo '安装凭据格式无效。' >&2; exit 1; }
. /etc/os-release
case "$ID" in ubuntu|debian) ;; *) echo '仅支持 Ubuntu / Debian。' >&2; exit 1;; esac
case "$(uname -m)" in aarch64|arm64) arch=linux-arm64;; x86_64) arch=linux-amd64;; *) echo '不支持此 CPU 架构。' >&2; exit 1;; esac
work=$(mktemp -d); phase=dependencies; unit=''; started=0; completed=0
printf 'Authorization: Bearer %s\n' "$token" >"$work/auth.headers"
cleanup(){ rm -rf -- "$work"; }
fail_install(){
 code=$?
 echo "安装失败（阶段：$phase）。请检查上方输出，后台可重新生成安装命令。" >&2
 if command -v curl >/dev/null 2>&1; then
  printf '{"phase":"%s"}' "$phase" >"$work/error.json"
  curl -fsS --max-time 10 -H "@$work/auth.headers" -H 'Content-Type: application/json' --data-binary "@$work/error.json" "$api/api/node/setup/error" >/dev/null 2>&1 || true
 fi
 if test "$started" = 1 && test "$completed" != 1; then systemctl stop "$unit" || true; fi
 exit "$code"
}
trap cleanup EXIT
trap fail_install ERR
missing=()
for pair in 'curl:curl' 'python3:python3' 'openssl:openssl'; do
 command -v "${pair%%:*}" >/dev/null 2>&1 || missing+=("${pair##*:}")
done
test -s /etc/ssl/certs/ca-certificates.crt || missing+=(ca-certificates)
if test "${#missing[@]}" -gt 0; then
 echo '安装所需系统软件包……'
 apt-get update
 DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates "${missing[@]}"
fi
command -v systemctl >/dev/null
systemctl is-system-running >/dev/null 2>&1 || test -d /run/systemd/system
# BBR + fq is best effort: containers and old kernels keep their defaults without failing the install.
enable_bbr(){
 conf=/etc/sysctl.d/99-tunnelx-bbr.conf
 modprobe tcp_bbr >/dev/null 2>&1 || true
 if ! grep -qw bbr /proc/sys/net/ipv4/tcp_available_congestion_control 2>/dev/null; then
  echo '提示：当前内核不支持 BBR，保持系统默认拥塞控制。'; return 0
 fi
 { printf 'net.core.default_qdisc = fq\nnet.ipv4.tcp_congestion_control = bbr\n' >"$conf" && chmod 644 "$conf"; } 2>/dev/null || true
 if modinfo tcp_bbr >/dev/null 2>&1 && test -d /etc/modules-load.d; then
  { echo tcp_bbr >/etc/modules-load.d/tunnelx-bbr.conf && chmod 644 /etc/modules-load.d/tunnelx-bbr.conf; } 2>/dev/null || true
 fi
 sysctl -q -w net.core.default_qdisc=fq >/dev/null 2>&1 || true
 sysctl -q -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1 || true
 if test "$(cat /proc/sys/net/ipv4/tcp_congestion_control 2>/dev/null)" = bbr; then
  echo '已开启 BBR + fq（BBR 立即生效，fq 在重启后覆盖全部网卡，重启后设置保持）。'
 else
  echo '提示：当前环境不允许修改拥塞控制（例如容器），保持系统默认。'
 fi
}
enable_bbr
python3 - "$api" <<'PY'
import sys,urllib.parse,ipaddress
u=urllib.parse.urlsplit(sys.argv[1])
assert u.hostname and not u.username and not u.password and not u.query and not u.fragment
assert u.scheme=='https' or (u.scheme=='http' and ipaddress.ip_address(u.hostname).is_loopback)
PY
phase=download
echo '下载并校验 H2 内核……'
curl -fsS --max-time 30 --proto '=https,http' --tlsv1.2 -H "@$work/auth.headers" "$api/api/node/setup?arch=$arch" -o "$work/metadata.json"
python3 - "$work/metadata.json" "$api" "$arch" <<'PY'
import sys,json,re,ipaddress
d=json.load(open(sys.argv[1]));base=sys.argv[2];arch=sys.argv[3]
assert re.fullmatch('[a-f0-9]{32}',d['node_id'])
assert 1024<=d['port']<=65535 and isinstance(d['port'],int)
assert d.get('certificate_mode','') in ('','public','private')
for name in (d['domain'],d['inner_name']):
 assert len(name)<=253 and '.' in name and all(re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?',label) for label in name.split('.'))
assert re.fullmatch('[a-z0-9.-]+',d['domain']) and '..' not in d['domain']
ipaddress.ip_address(d['ip'])
assert d['agent']['api_url']==base and d['agent']['node_id']==d['node_id']
assert d['agent']['state_file']=='/var/lib/tunnelx-nodes/'+d['node_id']+'/agent-state.json'
assert re.fullmatch('[A-Za-z0-9_-]{43}',d['agent']['agent_key'])
assert {f['name'] for f in d['artifacts']}=={'tunnelx-server','tunnelx-admin'} and len(d['artifacts'])==2
for f in d['artifacts']:
 assert f['url']==base+'/api/node/setup/file/'+arch+'/'+f['name']
 assert re.fullmatch('[a-f0-9]{64}',f['sha256']) and 0<f['bytes']<=32*1024*1024
PY
meta(){ python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$work/metadata.json" "$1"; }
node_id=$(meta node_id); domain=$(meta domain); ip=$(meta ip); port=$(meta port); inner=$(meta inner_name)
certificate_mode=$(meta certificate_mode); certificate_mode=${certificate_mode:-public}
if test "$certificate_mode" = public && ! command -v caddy >/dev/null 2>&1; then apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y caddy; fi
unit="tunnelx-node-$node_id.service"; group="txnode-${node_id:0:12}"
config_dir="/etc/tunnelx-nodes/$node_id"; bin_dir="/opt/tunnelx/nodes/$node_id"; state_dir="/var/lib/tunnelx-nodes/$node_id"
for name in tunnelx-server tunnelx-admin; do
 curl -fsS --max-time 120 --proto '=https,http' --tlsv1.2 -H "@$work/auth.headers" "$api/api/node/setup/file/$arch/$name" -o "$work/$name"
 python3 - "$work/metadata.json" "$work/$name" "$name" <<'PY'
import sys,json,hashlib,pathlib
f=next(f for f in json.load(open(sys.argv[1]))['artifacts'] if f['name']==sys.argv[3]);p=pathlib.Path(sys.argv[2])
assert p.stat().st_size==f['bytes'] and hashlib.sha256(p.read_bytes()).hexdigest()==f['sha256'],'内核校验失败'
PY
 chmod 755 "$work/$name"
done
phase=config
echo '检查域名、端口并生成节点密钥……'
if test "$certificate_mode" = public; then
python3 - "$domain" "$ip" <<'PY'
import sys,socket,ipaddress
addresses={str(ipaddress.ip_address(info[4][0])) for info in socket.getaddrinfo(sys.argv[1],None,type=socket.SOCK_STREAM)}
assert str(ipaddress.ip_address(sys.argv[2])) in addresses,'域名没有直接指向登记的节点 IP，请检查 DNS，勿开启 CDN 代理'
PY
fi
if systemctl is-active --quiet "$unit"; then systemctl stop "$unit"; fi
python3 - "$port" <<'PY'
import socket,sys
s=socket.socket();s.bind(('0.0.0.0',int(sys.argv[1])));s.close()
PY
id "$group" >/dev/null 2>&1 || useradd --system --home /nonexistent --shell /usr/sbin/nologin "$group"
install -d -m 755 /etc/tunnelx-nodes /opt/tunnelx/nodes /var/lib/tunnelx-nodes "$bin_dir"
install -d -m 750 -o root -g "$group" "$config_dir"
install -d -m 700 -o "$group" -g "$group" "$state_dir"
install -m 755 "$work/tunnelx-server" "$bin_dir/tunnelx-server"
if ! test -f "$config_dir/server.json"; then
 "$work/tunnelx-admin" -out "$work/config" -domain "$domain" -ip "$ip" -port "$port" -inner "$inner"
 for name in server.json ech-key.json inner-cert.pem inner-key.pem client-strict.json origin-ca.pem; do install -m 640 -o root -g "$group" "$work/config/$name" "$config_dir/$name"; done
else
 python3 - "$config_dir/server.json" "$inner" "$port" "$certificate_mode" "$domain" <<'PY'
import json,sys
c=json.load(open(sys.argv[1]));assert c['inner_name']==sys.argv[2] and c['listen']==':'+sys.argv[3],'已有节点配置不一致，拒绝覆盖密钥'
from pathlib import Path
p=Path(sys.argv[1]).parent
if (p/'public-domain').exists():
 assert (p/'public-domain').read_text().strip()==sys.argv[5], '证书名称不一致，拒绝覆盖'
 assert c['cert_file'].startswith('private-cert/')==(sys.argv[4]=='private'), '证书模式不一致，拒绝覆盖'
PY
fi
python3 - "$work/metadata.json" "$config_dir" <<'PY'
import sys,json,pathlib,os
d=json.load(open(sys.argv[1]));p=pathlib.Path(sys.argv[2]);c=json.loads((p/'server.json').read_text())
c['cert_file']='public-cert/current/cert.pem';c['key_file']='public-cert/current/key.pem'
c['managed_only']=True;c['tokens']=[]
(p/'server.json').write_text(json.dumps(c,indent=2)+'\n')
(p/'agent.json').write_text(json.dumps(d['agent'],indent=2)+'\n')
(p/'public-domain').write_text(d['domain']+'\n')
if d.get('certificate_mode')=='private':
 c['cert_file']=c['inner_cert_file']='private-cert/current/cert.pem'
 c['key_file']=c['inner_key_file']='private-cert/current/key.pem'
 (p/'server.json').write_text(json.dumps(c,indent=2)+'\n')
PY
chown root:"$group" "$config_dir/agent.json" "$config_dir/public-domain"
chmod 640 "$config_dir/agent.json" "$config_dir/public-domain" "$config_dir/server.json"
if test "$certificate_mode" = public; then
cat >"$bin_dir/renew-cert.py" <<'PY'
import pathlib,sys,subprocess,hashlib,os,tempfile,grp
p=pathlib.Path(sys.argv[1]);group=sys.argv[2];domain=(p/'public-domain').read_text().strip()
def run(*args):return subprocess.check_output(args,stderr=subprocess.DEVNULL)
sources=pathlib.Path('/var/lib/caddy/.local/share/caddy/certificates')
candidates=sorted(sources.glob('*/'+domain+'/'+domain+'.crt'),key=lambda f:f.stat().st_mtime,reverse=True)
for cert in candidates:
 key=cert.with_suffix('.key')
 if not key.exists():continue
 try:
  run('openssl','x509','-in',str(cert),'-noout','-checkhost',domain,'-checkend','3600')
  run('openssl','verify','-purpose','sslserver','-verify_hostname',domain,'-untrusted',str(cert),str(cert))
  public=subprocess.run(['openssl','x509','-in',str(cert),'-pubkey','-noout'],check=True,capture_output=True).stdout
  cp=subprocess.run(['openssl','pkey','-pubin','-outform','DER'],input=public,check=True,capture_output=True).stdout
  kp=run('openssl','pkey','-in',str(key),'-pubout','-outform','DER')
  assert cp==kp
 except Exception:continue
 dest=p/'public-cert';dest.mkdir(mode=0o750,exist_ok=True);gid=grp.getgrnam(group).gr_gid;os.chown(dest,0,gid);os.chmod(dest,0o750)
 old=dest/'current'
 if (old/'cert.pem').exists() and (old/'key.pem').exists() and (old/'cert.pem').read_bytes()==cert.read_bytes() and (old/'key.pem').read_bytes()==key.read_bytes():sys.exit(0)
 generation=pathlib.Path(tempfile.mkdtemp(prefix='cert-',dir=dest));os.chmod(generation,0o750);os.chown(generation,0,gid)
 for src,name in [(cert,'cert.pem'),(key,'key.pem')]:
  out=generation/name;out.write_bytes(src.read_bytes());os.chmod(out,0o640);os.chown(out,0,gid)
 link=dest/('.next-'+str(os.getpid()));link.symlink_to(generation);os.replace(link,old);sys.exit(0)
raise SystemExit('没有找到此域名的有效公开证书')
PY
chmod 755 "$bin_dir/renew-cert.py"
phase=certificate
echo '准备公开证书（需要域名解析，以及 80/443 入站可达）……'
if ! python3 "$bin_dir/renew-cert.py" "$config_dir" "$group" 2>/dev/null; then
 python3 - "$domain" "$node_id" <<'PY'
import sys,pathlib,subprocess,json,os
domain,node_id=sys.argv[1:];p=pathlib.Path('/etc/caddy/Caddyfile');original=p.read_text()
adapted=subprocess.run(['caddy','adapt','--config',str(p),'--adapter','caddyfile'],capture_output=True,text=True,check=True)
def hosts(obj):
 if isinstance(obj,dict):
  if domain in obj.get('host',[]):return True
  return any(hosts(v) for v in obj.values())
 if isinstance(obj,list):return any(hosts(v) for v in obj)
 return False
if not hosts(json.loads(adapted.stdout)):
 folder=pathlib.Path('/etc/caddy/tunnelx-nodes');folder.mkdir(mode=0o755,exist_ok=True);os.chmod(folder,0o755)
 site=folder/(node_id+'.caddy');assert not site.exists(),'已存在此节点的 Caddy 配置，需先检查证书申请状态'
 site.write_text(domain+' {\n    respond "Web service is online." 200\n}\n')
 os.chmod(site,0o644)
 marker='import /etc/caddy/tunnelx-nodes/*.caddy'
 backup=p.with_name('Caddyfile.tunnelx-'+node_id+'.bak')
 if not backup.exists():backup.write_text(original);os.chmod(backup,0o600)
 if marker not in original:p.write_text(original+'\n'+marker+'\n')
 valid=subprocess.run(['caddy','validate','--config',str(p),'--adapter','caddyfile'],capture_output=True,text=True)
 if valid.returncode:
  p.write_text(original);site.unlink();raise SystemExit('Caddy 配置校验失败，原配置已恢复')
PY
 systemctl enable --now caddy
 systemctl reload caddy
 found=0
 for attempt in $(seq 1 90); do
  if python3 "$bin_dir/renew-cert.py" "$config_dir" "$group" 2>/dev/null; then found=1;break;fi
  sleep 2
 done
 test "$found" = 1 || { echo '证书申请未完成，请检查 DNS、云安全组 80/443 端口和 journalctl -u caddy。' >&2; false; }
fi
else
cat >"$bin_dir/renew-cert.py" <<'PY'
# TUNNELX_PRIVATE_CERT_IMPLEMENTATION
PY
chmod 755 "$bin_dir/renew-cert.py"
phase=certificate
echo '生成并验证节点私有证书（无需 DNS 解析或公开证书申请）……'
python3 "$bin_dir/renew-cert.py" "$config_dir" "$group" --initialize
fi
phase=service
echo '校验并启动独立节点服务……'
runuser -u "$group" -- "$bin_dir/tunnelx-server" -config "$config_dir/server.json" -agent "$config_dir/agent.json" -check
cat >"/etc/systemd/system/$unit" <<UNIT
[Unit]
Description=tunnelX managed node $node_id
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=$group
Group=$group
ExecStart=$bin_dir/tunnelx-server -config $config_dir/server.json -agent $config_dir/agent.json
Restart=on-failure
RestartSec=3
UMask=0077
StateDirectory=tunnelx-nodes/$node_id
StateDirectoryMode=0700
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
CapabilityBoundingSet=
LimitNOFILE=16384
TasksMax=512
MemoryMax=512M
[Install]
WantedBy=multi-user.target
UNIT
cert_unit="tunnelx-node-$node_id-cert"
cat >"/etc/systemd/system/$cert_unit.service" <<UNIT
[Unit]
Description=Renew certificate for tunnelX node $node_id
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 $bin_dir/renew-cert.py $config_dir $group
UMask=0077
NoNewPrivileges=true
ProtectHome=true
UNIT
cat >"/etc/systemd/system/$cert_unit.timer" <<UNIT
[Unit]
Description=Certificate refresh for tunnelX node $node_id
[Timer]
OnBootSec=5min
OnUnitActiveSec=1h
Persistent=true
[Install]
WantedBy=timers.target
UNIT
chmod 644 "/etc/systemd/system/$unit" "/etc/systemd/system/$cert_unit.service" "/etc/systemd/system/$cert_unit.timer"
systemctl daemon-reload
started=1
systemctl enable --now "$unit" "$cert_unit.timer"
for attempt in $(seq 1 20); do
 if systemctl is-active --quiet "$unit"; then break;fi
 sleep 1
done
systemctl is-active --quiet "$unit"
python3 - "$port" <<'PY'
import socket,sys,time
for _ in range(30):
 try:
  with socket.create_connection(('127.0.0.1',int(sys.argv[1])),timeout=1):break
 except OSError:time.sleep(.2)
else:raise SystemExit('节点端口没有开始监听')
PY
python3 - "$config_dir" "$work/complete.json" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1]);c=json.loads((p/'client-strict.json').read_text())
c['token']='';c.pop('device_id',None);c['ca_file']=''
pathlib.Path(sys.argv[2]).write_text(json.dumps({'client':c,'ca_pem':(p/'origin-ca.pem').read_text()}))
PY
phase=complete
curl -fsS --retry 2 --retry-all-errors --retry-delay 2 --max-time 20 --proto '=https,http' --tlsv1.2 -H "@$work/auth.headers" -H 'Content-Type: application/json' --data-binary "@$work/complete.json" "$api/api/node/setup/complete" >/dev/null
completed=1
echo '节点已安装并对接后台。稍等数秒即可看到在线心跳。'
printf '节点服务：%s\n监听端口：%s/TCP\n' "$unit" "$port"
echo '请在云安全组放行上述节点端口；私有密钥仅保存在此服务器。'
