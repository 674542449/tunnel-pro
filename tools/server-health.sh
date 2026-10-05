#!/bin/sh
set -eu
python3 - <<'PY'
import datetime,hashlib,json,pathlib,subprocess
def command(*args): return subprocess.check_output(args,text=True).strip()
def state(unit):
    output=command('systemctl','show',unit,'-p','ActiveState','-p','SubState','-p','NRestarts','-p','User','-p','MemoryCurrent','-p','MemoryPeak','-p','ExecMainStartTimestamp')
    return dict(line.split('=',1) for line in output.splitlines())
c=json.loads(pathlib.Path('/etc/tunnelx/server.json').read_text())
listeners=command('ss','-H','-lntup').splitlines()
fixtures=[s for s in listeners if any(':'+str(p)+' ' in s for p in (18480,18481,18482,18443,18543))]
report=dict(
 checked_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),
 os_release=command('lsb_release','-ds'),architecture=command('uname','-m'),
 service=state('tunnelx.service'),caddy=state('caddy.service'),certificate_timer=state('tunnelx-certificate.timer'),
 certificate=command('openssl','x509','-in','/etc/tunnelx/public-cert/current/cert.pem','-noout','-subject','-issuer','-dates').splitlines(),
 server_sha256=hashlib.sha256(pathlib.Path('/opt/tunnelx/bin/tunnelx-server').read_bytes()).hexdigest(),
 private_target_allowlist_count=len(c.get('allowed_private_targets',[])),acceptance_listeners=fixtures,
 preferred_target_ips=c.get('preferred_target_ips',{}),
 main_listeners=[s for s in listeners if ':8443 ' in s],
 transport='h2',
 udp_listener_removed=not any(s.startswith('udp ') and ':8443 ' in s for s in listeners),
)
report['passed']=all(report[key]['ActiveState']=='active' for key in ('service','caddy','certificate_timer')) and not fixtures and report['private_target_allowlist_count']==0 and report['service']['User']=='tunnelx' and report['udp_listener_removed'] and any(s.startswith('tcp ') for s in report['main_listeners'])
pathlib.Path('/opt/tunnelx/server-health.json').write_text(json.dumps(report,indent=2)+'\n')
print('Server health passed='+str(report['passed']))
if not report['passed']: raise SystemExit(1)
PY
