#!/bin/sh
# Run from the extracted release directory; reverse proxy HTTPS separately.
set -eu
test "$(id -u)" = 0 || { echo 'Run as root' >&2; exit 1; }
. /etc/os-release
case "$ID" in ubuntu|debian) ;; *) echo 'Supported: Ubuntu or Debian' >&2; exit 1;; esac
case "$(uname -m)" in aarch64|arm64) arch=arm64;; x86_64) arch=amd64;; *) echo 'Unsupported CPU' >&2; exit 1;; esac
url=${1:?Usage: install-control.sh HTTPS_PUBLIC_BASE_URL}
command -v python3 >/dev/null
command -v systemctl >/dev/null
test -x "linux-$arch/tunnelx-control"
id tunnelx-control >/dev/null 2>&1 || useradd --system --home /nonexistent --shell /usr/sbin/nologin tunnelx-control
install -d -m 755 /opt/tunnelx/bin
install -d -m 750 -o root -g tunnelx-control /etc/tunnelx-control
install -d -m 700 -o tunnelx-control -g tunnelx-control /var/lib/tunnelx-control
if test ! -f /etc/tunnelx-control/control.json; then
 "linux-$arch/tunnelx-control" -init -config /etc/tunnelx-control/control.json -public-url "$url"
 python3 - <<'PY'
import json,pathlib
p=pathlib.Path('/etc/tunnelx-control/control.json');c=json.loads(p.read_text());c['data_file']='/var/lib/tunnelx-control/control.json';p.write_text(json.dumps(c,indent=2)+'\n')
PY
 chown root:tunnelx-control /etc/tunnelx-control/control.json
 chmod 640 /etc/tunnelx-control/control.json
else
 python3 - "$url" <<'PY'
import json,pathlib,sys
c=json.loads(pathlib.Path('/etc/tunnelx-control/control.json').read_text())
assert c['public_url'].rstrip('/')==sys.argv[1].rstrip('/'),'Existing public URL differs; update private configuration deliberately'
PY
fi
install -m 755 "linux-$arch/tunnelx-control" /opt/tunnelx/bin/tunnelx-control.installing
for node_arch in linux-arm64 linux-amd64; do
 install -d -m 755 "/opt/tunnelx/bin/node-artifacts/$node_arch"
 for name in tunnelx-server tunnelx-admin; do
  install -m 644 "linux-$arch/node-artifacts/$node_arch/$name" "/opt/tunnelx/bin/node-artifacts/$node_arch/$name"
 done
done
runuser -u tunnelx-control -- /opt/tunnelx/bin/tunnelx-control.installing -config /etc/tunnelx-control/control.json -check
if systemctl is-active --quiet tunnelx-control; then systemctl stop tunnelx-control; fi
mv /opt/tunnelx/bin/tunnelx-control.installing /opt/tunnelx/bin/tunnelx-control
install -m 644 deploy/tunnelx-control.service /etc/systemd/system/tunnelx-control.service
systemctl daemon-reload
systemctl enable --now tunnelx-control.service
install -m 755 deploy/backup-control.py /opt/tunnelx/bin/backup-control.py
install -m 644 deploy/tunnelx-control-backup.service deploy/tunnelx-control-backup.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now tunnelx-control-backup.timer
systemctl start tunnelx-control-backup.service
echo 'Control listens only on 127.0.0.1:18081. Configure the HTTPS reverse proxy next.'
echo 'Encrypted backups are enabled hourly; initial backup completed. Preserve /etc/tunnelx-control/backup.key separately.'
echo 'Private bootstrap admin credentials: /etc/tunnelx-control/control.json'
