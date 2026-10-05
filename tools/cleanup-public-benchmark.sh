#!/bin/sh
set -eu
python3 - <<'PY'
from pathlib import Path
import hashlib,os
base=Path('/opt/tunnelx/benchmark')
live=Path('/etc/caddy/Caddyfile')
assert hashlib.sha256(live.read_bytes()).hexdigest()==(base/'caddy-benchmark.sha256').read_text().strip(), 'Caddy configuration changed; refusing blind restore.'
assert (base/'caddy-before.conf').is_file()
(base/'caddy-finished.conf').write_bytes(live.read_bytes())
os.chmod(base/'caddy-finished.conf',0o600)
PY
caddy validate --config /opt/tunnelx/benchmark/caddy-before.conf --adapter caddyfile
cp /opt/tunnelx/benchmark/caddy-before.conf /etc/caddy/Caddyfile
if ! systemctl reload caddy; then
    cp /opt/tunnelx/benchmark/caddy-finished.conf /etc/caddy/Caddyfile
    systemctl reload caddy
    exit 1
fi
curl -fsS --resolve test.xiaguamail.com:443:127.0.0.1 https://test.xiaguamail.com/ | python3 -c 'import sys; assert sys.stdin.read()=="Web service is online."'
systemctl is-active --quiet tunnelx
systemctl is-active --quiet tunnelx-certificate.timer
python3 - <<'PY'
from pathlib import Path
import datetime,hashlib,json
base=Path('/opt/tunnelx/benchmark')
expected=hashlib.sha256((base/'caddy-before.conf').read_bytes()).hexdigest()
assert hashlib.sha256(Path('/etc/caddy/Caddyfile').read_bytes()).hexdigest()==expected
for name in ('download.bin','fast-targets.json','measure-fast.py','performance-fast-server.json','caddy-before.conf','caddy-benchmark.sha256','caddy-finished.conf'):
 (base/name).unlink(missing_ok=True)
report=dict(checked_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),passed=True,caddy_configuration_restored=True,caddy_sha256=expected,public_benchmark_file_removed=True,temporary_fast_urls_removed=True,tunnelx_active=True,certificate_timer_active=True,production_server_restarted=False)
(base/'cleanup-report.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report))
PY
