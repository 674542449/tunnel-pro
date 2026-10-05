import argparse
import json
import re
import urllib.request
import urllib.error
p=argparse.ArgumentParser();p.add_argument('action',choices=['probe','exit']);p.add_argument('--url',default='http://127.0.0.1:9080');args=p.parse_args()
html=urllib.request.urlopen(args.url+'/').read().decode()
csrf=re.search("const csrf='([^']+)'",html).group(1)
body=b'{}'
req=urllib.request.Request(args.url+'/api/'+args.action,data=body,method='POST',headers={'Origin':args.url,'X-CSRF-Token':csrf,'Content-Type':'application/json'})
try:
 with urllib.request.urlopen(req,timeout=30) as res: print(res.read().decode())
except urllib.error.HTTPError as e:
 print(e.read().decode());raise SystemExit(1)
