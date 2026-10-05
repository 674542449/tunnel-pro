import argparse
import json
import pathlib
import tarfile
root=pathlib.Path(__file__).resolve().parent.parent
p=argparse.ArgumentParser()
p.add_argument('--acceptance-fixtures',action='store_true')
args=p.parse_args()
out=root/'.local/deploy.tar.gz'
c=json.loads((root/'.local/deployment/server.json').read_text())
c.pop('allowed_private_targets',None)
if args.acceptance_fixtures:
 c['allowed_private_targets']=['127.0.0.1:18480','127.0.0.1:18481','127.0.0.1:18482']
temporary=root/'.local/deployment/server-bundle.json'
temporary.write_text(json.dumps(c,indent=2))
with tarfile.open(out,'w:gz') as tar:
 for arch in ('amd64','arm64'):
  for name in ('server','fixture','check','admin'):
   tar.add(root/f'dist/linux-{arch}/tunnelx-{name}',arcname=f'linux-{arch}/tunnelx-{name}')
 for p in sorted((root/'deploy').glob('*')): tar.add(p,arcname='deploy/'+p.name)
 for name in ('ech-key.json','inner-cert.pem','inner-key.pem'):
  tar.add(root/'.local/deployment'/name,arcname='config/'+name)
 tar.add(temporary,arcname='config/server.json')
 for name in ('cert.pem','key.pem'):
  file=root/'.local/deployment'/name
  if file.exists(): tar.add(file,arcname='config/'+name)
print('Private deployment bundle created.')
