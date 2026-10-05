#!/bin/sh
set -eu
python3 - <<'PY'
from pathlib import Path
import hashlib,json,os
p=Path('/etc/caddy/Caddyfile')
original=p.read_text()
expected='test.xiaguamail.com {\n    respond "Web service is online." 200\n}'
if original.count(expected)!=1: raise SystemExit('Test domain configuration changed; refusing blind replacement.')
base=Path('/opt/tunnelx/benchmark'); base.mkdir(mode=0o755,exist_ok=True)
file=base/'download.bin'
block=bytes(i%251 for i in range(32768))
with file.open('wb') as f:
 for _ in range(2048): f.write(block)
os.chmod(file,0o644)
replacement='''test.xiaguamail.com {
    handle /tx-bench-20261001-7f936f/download.bin {
        root * /opt/tunnelx/benchmark
        rewrite * /download.bin
        file_server
    }
    handle {
        respond "Web service is online." 200
    }
}'''
backup=Path('/opt/tunnelx/benchmark/caddy-before.conf')
if backup.exists(): raise SystemExit('Benchmark backup already exists; inspect before restarting.')
backup.write_text(original); os.chmod(backup,0o600)
changed=original.replace(expected,replacement)
p.write_text(changed)
(base/'caddy-benchmark.sha256').write_text(hashlib.sha256(changed.encode()).hexdigest())
print('Controlled HTTPS benchmark file prepared: 64MiB, SHA256 '+hashlib.sha256(file.read_bytes()).hexdigest())
PY
if ! caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile; then
    cp /opt/tunnelx/benchmark/caddy-before.conf /etc/caddy/Caddyfile
    exit 1
fi
systemctl reload caddy
curl -fsSI --resolve test.xiaguamail.com:443:127.0.0.1 https://test.xiaguamail.com/tx-bench-20261001-7f936f/download.bin
