"""Exercise the Windows executable through its local application proxies, without a browser."""
import datetime
import hashlib
import http.client
import json
import pathlib
import socket
import ssl
import struct
import time

root=pathlib.Path(__file__).resolve().parent.parent
checks=[]

def check(name,fn):
 started=time.perf_counter()
 try:
  metrics=fn(); checks.append(dict(name=name,passed=True,seconds=time.perf_counter()-started,metrics=metrics))
 except Exception as e:
  checks.append(dict(name=name,passed=False,seconds=time.perf_counter()-started,error=str(e)))

def video():
 c=http.client.HTTPConnection('127.0.0.1',8088,timeout=30)
 try:
  c.request('GET','http://127.0.0.1:18480/video')
  r=c.getresponse(); b=r.read(); digest=hashlib.sha256(b).hexdigest()
  assert r.status==200 and len(b)==10188735 and digest=='4d59a668f2f0c01a233ef7a47179dc898017fa90ef2a412dedbc1fc5cab0b8b1'
  return dict(bytes=len(b),sha256=digest)
 finally: c.close()

def https():
 c=http.client.HTTPSConnection('127.0.0.1',8088,timeout=20,context=ssl.create_default_context())
 try:
  c.set_tunnel('test.xiaguamail.com',443)
  c.request('GET','/')
  r=c.getresponse(); b=r.read()
  assert r.status==200 and b'Web service is online.' in b
  return dict(http_status=r.status,application_tls_verified=True)
 finally: c.close()

def recv_full(s,n):
 b=b''
 while len(b)<n:
  part=s.recv(n-len(b))
  if not part: raise IOError('SOCKS connection closed')
  b+=part
 return b

def socks_udp():
 with socket.create_connection(('127.0.0.1',1080),timeout=10) as c:
  c.sendall(bytes([5,1,0])); assert recv_full(c,2)==bytes([5,0])
  c.sendall(bytes([5,3,0,1,0,0,0,0,0,0])); r=recv_full(c,10); assert r[1]==0 and r[3]==1
  address=(socket.inet_ntoa(r[4:8]),struct.unpack('!H',r[8:10])[0])
  packet=bytes([0,0,0,1,127,0,0,1])+struct.pack('!H',18481)+bytes([79])*1000
  with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as u:
   u.settimeout(5); u.sendto(packet,address); reply,_=u.recvfrom(2048); assert reply==packet
  return dict(payload_bytes=1000,echo_verified=True)

def status():
 c=http.client.HTTPConnection('127.0.0.1',9080,timeout=5)
 try:
  c.request('GET','/api/status'); r=c.getresponse(); data=json.loads(r.read())
  assert r.status==200 and data['privacy']=='strict' and data['ech'] and not data['system_proxy']
  assert data['downloaded']>=10188735 and data['uploaded']>0
  return data
 finally: c.close()

check('HTTP-proxy-MP4-hash',video)
check('HTTPS-CONNECT-real-domain',https)
check('SOCKS5-UDP-associate',socks_udp)
check('strict-ECH-live-status',status)
report=dict(finished_at=datetime.datetime.now().astimezone().isoformat(),platform='windows/amd64',passed=all(c['passed'] for c in checks),checks=checks)
(root/'docs/acceptance-client-exe.json').write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8')
for c in checks: print(c['name'],'passed='+str(c['passed']),c.get('error',''))
raise SystemExit(0 if report['passed'] else 1)
