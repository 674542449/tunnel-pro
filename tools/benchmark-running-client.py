import argparse
import datetime
import hashlib
import http.client
import json
import pathlib
import ssl
import time

root=pathlib.Path(__file__).resolve().parent.parent
p=argparse.ArgumentParser(); p.add_argument('--proxy-port',type=int,default=8088); p.add_argument('--web-port',type=int,default=9080); p.add_argument('--rounds',type=int,default=2); p.add_argument('--out',default='docs/performance-running-client.json'); args=p.parse_args()
def status():
 c=http.client.HTTPConnection('127.0.0.1',args.web_port,timeout=5)
 c.request('GET','/api/status'); r=c.getresponse(); data=json.loads(r.read()); c.close(); return data
results=[]
for i in range(args.rounds):
 before=status(); start=time.perf_counter()
 c=http.client.HTTPSConnection('127.0.0.1',args.proxy_port,timeout=90,context=ssl.create_default_context())
 c.set_tunnel('test.xiaguamail.com',443)
 c.request('GET','/tx-bench-20261001-7f936f/download.bin')
 r=c.getresponse(); h=hashlib.sha256(); n=0
 while b:=r.read(65536): n+=len(b); h.update(b)
 c.close(); seconds=time.perf_counter()-start; after=status()
 item=dict(round=i+1,bytes=n,seconds=seconds,MBps=n/seconds/1e6,Mbps=n*8/seconds/1e6,sha256=h.hexdigest(),h2_new=after['h2_streams']-before['h2_streams'],mode=before['mode'])
 item['passed']=r.status==200 and n==67108864 and h.hexdigest()=='f8dfeaaa09d17ea0e07bcd70bdbf38d55406a6099c6a263a0a27e8e313f12067'
 results.append(item); print(json.dumps(item),flush=True)
(root/args.out).write_text(json.dumps(dict(checked_at=datetime.datetime.now().astimezone().isoformat(),samples=results),indent=2)+'\n')
