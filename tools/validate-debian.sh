#!/bin/sh
set -eu
root=/opt/tunnelx/validation/debian13
install -d -m 755 "$root/usr/local/bin" "$root/etc/tunnelx" "$root/etc/ssl/certs"
install -m 755 /opt/tunnelx/bin/tunnelx-server /opt/tunnelx/bin/tunnelx-check "$root/usr/local/bin/"
for f in server.json ech-key.json inner-cert.pem inner-key.pem; do
    install -m 600 "/etc/tunnelx/$f" "$root/etc/tunnelx/$f"
done
install -m 600 /etc/tunnelx/public-cert/current/cert.pem "$root/etc/tunnelx/cert.pem"
install -m 600 /etc/tunnelx/public-cert/current/key.pem "$root/etc/tunnelx/key.pem"
install -m 644 /etc/ssl/certs/ca-certificates.crt "$root/etc/ssl/certs/ca-certificates.crt"
python3 - <<'PY'
import json
from pathlib import Path
base=Path('/opt/tunnelx/validation/debian13/etc/tunnelx')
p=base/'server.json'
c=json.loads(p.read_text())
c.update(listen='127.0.0.1:18443',cert_file='cert.pem',key_file='key.pem')
p.write_text(json.dumps(c,indent=2))
for name in ('client.json','client-strict.json'):
    p=base/name
    c=json.loads(p.read_text())
    c.update(server_ip='127.0.0.1',port=18443)
    p.write_text(json.dumps(c,indent=2))
PY
chroot "$root" /usr/local/bin/tunnelx-server -config /etc/tunnelx/server.json -check
systemd-run --unit=tunnelx-debian-validation "$(command -v chroot)" "$root" /usr/local/bin/tunnelx-server -config /etc/tunnelx/server.json
trap 'systemctl stop tunnelx-debian-validation.service' EXIT
sleep 1
chroot "$root" /usr/local/bin/tunnelx-check -config /etc/tunnelx/client.json -strict-config /etc/tunnelx/client-strict.json -fixtures -out /etc/tunnelx/acceptance-debian13.json
cp "$root/etc/tunnelx/acceptance-debian13.json" /opt/tunnelx/validation/acceptance-debian13.json
cat "$root/etc/debian_version"
systemd-analyze verify /etc/systemd/system/tunnelx.service /etc/systemd/system/tunnelx-certificate.service /etc/systemd/system/tunnelx-certificate.timer
