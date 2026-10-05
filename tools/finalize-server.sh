#!/bin/sh
set -eu
systemctl stop tunnelx-acceptance-fixture.service
python3 - <<'PY'
import json
p='/etc/tunnelx/server.json'
with open(p) as f: c=json.load(f)
c.pop('allowed_private_targets',None)
with open(p,'w') as f: json.dump(c,f,indent=2); f.write('\n')
print('Production private-target allowlist removed.')
PY
chmod 755 /opt/tunnelx/bin/sync-certificate.sh.new
mv /opt/tunnelx/bin/sync-certificate.sh.new /opt/tunnelx/bin/sync-certificate.sh
/opt/tunnelx/bin/tunnelx-server -config /etc/tunnelx/server.json -check
systemctl restart tunnelx.service
systemctl start tunnelx-certificate.service
systemctl is-active tunnelx.service caddy.service tunnelx-certificate.timer
systemctl show tunnelx.service -p User -p ActiveState -p NRestarts -p MemoryCurrent -p MemoryPeak -p ExecMainStartTimestamp
openssl x509 -in /etc/tunnelx/public-cert/current/cert.pem -noout -subject -issuer -dates
if ss -lntup | grep -E ':(18480|18481|18482|18443) '; then
    echo 'Acceptance test listener remains active' >&2
    exit 1
fi
printf '%s\n' 'Acceptance listeners stopped; main service and certificate timer active.'
