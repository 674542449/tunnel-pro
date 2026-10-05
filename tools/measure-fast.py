"""Bounded byte-transfer samples to observed FAST targets; not FAST's browser algorithm."""
import argparse
import datetime
import http.client
import json
import pathlib
import ssl
import time
import urllib.parse

p=argparse.ArgumentParser(); p.add_argument('--targets',required=True); p.add_argument('--out',required=True); p.add_argument('--direct',action='store_true'); args=p.parse_args()
data=json.loads(pathlib.Path(args.targets).read_text())
samples=[]
for target in data.get('targets',[])[:3]:
 u=urllib.parse.urlsplit(target['url'])
 if u.scheme!='https' or not u.hostname.endswith('.oca.nflxvideo.net') or u.port not in (None,443) or u.username: raise ValueError('Unexpected FAST target')
 started=time.perf_counter(); item=dict(host=u.hostname)
 c=None
 try:
  host=u.hostname if args.direct else '127.0.0.1'; port=443 if args.direct else 8088
  c=http.client.HTTPSConnection(host,port,timeout=30,context=ssl.create_default_context())
  if not args.direct: c.set_tunnel(u.hostname,443)
  c.request('GET',u.path+'?'+u.query,headers={'Range':'bytes=0-16777215','Accept-Encoding':'identity'})
  r=c.getresponse(); n=0; first=time.perf_counter()-started
  if r.status not in (200,206): raise RuntimeError('HTTP '+str(r.status))
  while n<16<<20:
   b=r.read(min(65536,(16<<20)-n))
   if not b: break
   n+=len(b)
  seconds=time.perf_counter()-started
  item.update(passed=n>0,status=r.status,bytes=n,seconds=seconds,MBps=n/seconds/1e6,Mbps=n*8/seconds/1e6,ttfb_seconds=first,bounded_partial_sample=True)
 except Exception as e: item.update(passed=False,error=str(e))
 finally:
  if c: c.close()
 samples.append(item); print(json.dumps(item),flush=True)
report=dict(checked_at=datetime.datetime.now().astimezone().isoformat(),route='server-direct' if args.direct else 'Windows-running-client-H2',samples=samples,limitations=['Bounded 16MiB samples, not the FAST.com multi-connection/browser algorithm.','Discovery and sample times can differ from the user test.'])
pathlib.Path(args.out).write_text(json.dumps(report,indent=2)+'\n')
raise SystemExit(0 if all(s['passed'] for s in samples) else 1)
