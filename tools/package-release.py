"""Package public source/server artifacts separately from the private Windows profile."""
import argparse
import hashlib
import json
import pathlib
import shutil
import subprocess
import tarfile
import zipfile

root=pathlib.Path(__file__).resolve().parent.parent
p=argparse.ArgumentParser(); p.add_argument('--version',default=''); p.add_argument('--client-only',action='store_true'); p.add_argument('--strategy-report'); p.add_argument('--stage-only',action='store_true'); args=p.parse_args()
if args.version and (not args.version.startswith('v') or not all(c.isalnum() or c in '.-' for c in args.version)): raise ValueError('Invalid release version')
suffix='-'+args.version if args.version else ''
strategy=None
if args.strategy_report:
 report=json.loads(pathlib.Path(args.strategy_report).read_text(encoding='utf-8'))
 assert report['completed'] and report['selected'], 'Strategy testing incomplete'
 strategy=next(item['settings'] for item in report['plans'] if item['id']==report['selected'])
 assert set(strategy)=={'transport','auto_tcp_preference','h2_connections','h2_read_idle_seconds','h2_ping_timeout_seconds','h2_max_age_seconds','h2_retry_new_connection','connect_timeout_seconds'}
dist=root/'dist'
stage=dist/('windows-client'+suffix)
stage.mkdir(exist_ok=True)
for name in ('client','check','admin'):
 source=dist/'windows-amd64'/f'tunnelx-{name}.exe'; target=stage/source.name
 if not target.exists() or target.read_bytes()!=source.read_bytes(): shutil.copy2(source,target)
for source,target in (('client-strict.json','client.json'),('client.json','client-normal.json')):
 profile=json.loads((root/'.local/deployment'/source).read_text(encoding='utf-8'))
 profile['transport']='h2'
 profile.pop('auto_tcp_preference',None)
 if strategy:
  profile.update({k:v for k,v in strategy.items() if k not in ('transport','auto_tcp_preference')})
 profile['h2_retry_new_connection']=True
 (stage/target).write_text(json.dumps(profile,indent=2)+'\n',encoding='utf-8')
shutil.copy2(root/'.local/deployment/origin-ca.pem',stage/'origin-ca.pem')
shutil.copy2(root/'README.md',stage/'README.md')
shutil.copy2(root/'THIRD_PARTY_PATCHES.md',stage/'THIRD_PARTY_PATCHES.md')
shutil.copytree(root/'docs',stage/'docs',dirs_exist_ok=True)
(stage/'start-normal.cmd').write_text('@echo off\ncd /d "%~dp0"\nstart "" "%~dp0tunnelx-client.exe" -config "%~dp0client-normal.json"\n',encoding='ascii')
(stage/'restore-proxy.cmd').write_text('@echo off\ncd /d "%~dp0"\n"%~dp0tunnelx-client.exe" -restore-proxy\n',encoding='ascii')
(stage/'QUICKSTART.txt').write_text('tunnelX Windows x64\n\n1. Exit the previous tunnelX from its control panel before upgrading.\n2. Double-click tunnelx-client.exe.\n3. Click Detect connection, then Enable Windows system proxy if needed.\n4. Exit from the local control panel to restore your previous proxy.\n\nDefault: strict privacy; transport settings are in client.json.\nSOCKS5: 127.0.0.1:1080\nHTTP: 127.0.0.1:8088\nPanel: http://127.0.0.1:9080\n\nThis package contains private access credentials. Keep client*.json private.\nFull Chinese instructions: README.md.\n',encoding='utf-8')
licenses=stage/'licenses'
licenses.mkdir(exist_ok=True)
for file in (root/'vendor').rglob('*'):
 if file.is_file() and (file.name.upper().startswith(('LICENSE','COPYING','NOTICE','PATENTS'))):
  target=licenses/file.relative_to(root/'vendor')
  target.parent.mkdir(parents=True,exist_ok=True); shutil.copy2(file,target)
goroot=pathlib.Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
for name in ('LICENSE','PATENTS'):
 file=goroot/name
 if file.exists():
  (licenses/'go').mkdir(exist_ok=True); shutil.copy2(file,licenses/'go'/name)

if args.stage_only:
 print('Windows release staged; archives not yet sealed.')
 raise SystemExit(0)

with zipfile.ZipFile(dist/('tunnelX-windows-x64'+suffix+'-private.zip'),'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
 for file in sorted(stage.rglob('*')):
  if file.is_file() and not any(part in ('state','logs') for part in file.relative_to(stage).parts) and file.suffix not in ('.log','.jsonl'): z.write(file,arcname='tunnelX-client/'+file.relative_to(stage).as_posix())
if not args.client_only:
 with tarfile.open(dist/('tunnelX-linux-servers'+suffix+'.tar.gz'),'w:gz') as t:
  for arch in ('amd64','arm64'):
   for name in ('server','fixture','check','admin','control'):
    file=dist/f'linux-{arch}'/f'tunnelx-{name}'
    info=t.gettarinfo(str(file),arcname=f'linux-{arch}/tunnelx-{name}'); info.mode=0o755
    with file.open('rb') as f: t.addfile(info,f)
  for folder in ('deploy','docs'):
   for file in sorted((root/folder).rglob('*')):
    if file.is_file(): t.add(file,arcname=folder+'/'+file.relative_to(root/folder).as_posix())
  for name in ('README.md','THIRD_PARTY_PATCHES.md'): t.add(root/name,arcname=name)
  t.add(licenses,arcname='licenses')
with zipfile.ZipFile(dist/('tunnelX-source'+suffix+'.zip'),'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
 for file in sorted(root.rglob('*')):
  if not file.is_file(): continue
  relative=file.relative_to(root)
  if any(part in ('.local','dist','.git','__pycache__','logs','state','node_modules') for part in relative.parts): continue
  if relative.parts[:3]==('desktop','build','bin'): continue
  if file.suffix in ('.log','.test','.pyc','.jsonl','.exe','.syso','.dpapi'): continue
  z.write(file,arcname='tunnelX/'+relative.as_posix())

manifest=[]
for file in sorted(dist.rglob('*')):
 if file.is_file() and file.name!='SHA256.json' and not any(part in ('state','logs') for part in file.relative_to(dist).parts) and file.suffix not in ('.log','.jsonl'):
  manifest.append({'path':file.relative_to(dist).as_posix(),'bytes':file.stat().st_size,'sha256':hashlib.sha256(file.read_bytes()).hexdigest()})
(dist/'SHA256.json').write_text(json.dumps(manifest,indent=2)+'\n',encoding='utf-8')
print('Release packages created; private keys and SSH credentials excluded from Windows/source packages.')
