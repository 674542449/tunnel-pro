"""Read the public FAST frontend through the running local proxy; never execute its JS."""
import http.client
import json
import pathlib
import re
import ssl
import urllib.parse

root=pathlib.Path(__file__).resolve().parent.parent
def get(url):
 u=urllib.parse.urlsplit(url)
 c=http.client.HTTPSConnection('127.0.0.1',8088,timeout=25,context=ssl.create_default_context())
 try:
  c.set_tunnel(u.hostname,u.port or 443)
  c.request('GET',u.path+('?' + u.query if u.query else ''))
  r=c.getresponse(); b=r.read()
  if r.status!=200: raise RuntimeError('FAST HTTP '+str(r.status))
  return b.decode()
 finally: c.close()
html=get('https://fast.com/')
scripts=[urllib.parse.urljoin('https://fast.com/',s) for s in re.findall(r'<script[^>]+src=["\x27]([^"\x27]+)',html)]
for i,url in enumerate(scripts):
 if urllib.parse.urlsplit(url).hostname!='fast.com': continue
 data=get(url); (root/f'.local/fast-script-{i}.js').write_text(data,encoding='utf-8')
 print(url,'length='+str(len(data)))
 token=re.search(r'getTestOcasParams:\{[^}]*token:"([A-Za-z0-9_=+-]+)"',data)
 if token:
  response=json.loads(get('https://api.fast.com/netflix/speedtest/v2?https=true&token='+token.group(1)+'&urlCount=3'))
  (root/'.local/fast-targets.json').write_text(json.dumps(response),encoding='utf-8')
  print('FAST target metadata saved; hostnames:',[urllib.parse.urlsplit(t['url']).hostname for t in response.get('targets',[])])
