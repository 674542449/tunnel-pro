"""Elevated, bounded acceptance of the real EXE and production TUN routing.

Use only the private beta account and a separate state directory. Does not open
a GUI, read the installed user's credentials, or change Windows proxy values.
Temporarily installs the actual TUN routes/DNS policy; always disconnects.
"""
import argparse,ctypes,datetime,hashlib,http.client,json,os,pathlib,socket,struct,subprocess,time,urllib.request,uuid,winreg
from contextlib import closing
from versioning import check

root=pathlib.Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser();parser.add_argument('--private-fixture',required=True);parser.add_argument('--resilience',action='store_true');args=parser.parse_args()
private=pathlib.Path(args.private_fixture).resolve();assert private.is_relative_to(root/'.local')
assert ctypes.windll.shell32.IsUserAnAdmin(),'Full TUN acceptance needs administrator authorization'
version=check();exe=root/'dist'/('desktop-'+version)/'tunnelx-desktop.exe'
access=json.loads((private/'staging-access.json').read_bytes());config=json.loads((private/'staging-config.json').read_bytes())
stage=private/('tun-product-'+uuid.uuid4().hex);stage.mkdir()
process=None;cases=[]
def free_port():
 with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
ports=[free_port() for _ in range(3)];assert len(set(ports))==3;addr='127.0.0.1:'+str(ports[2])
settings=dict(api_url=config['public_url'],socks_listen='127.0.0.1:'+str(ports[0]),http_listen='127.0.0.1:'+str(ports[1]),web_listen=addr)
(stage/'settings.json').write_text(json.dumps(settings),encoding='utf-8')
def registry():
 with winreg.OpenKey(winreg.HKEY_CURRENT_USER,r'Software\Microsoft\Windows\CurrentVersion\Internet Settings') as key:
  out={}
  for name in ['ProxyEnable','ProxyServer','ProxyOverride','AutoConfigURL']:
   try:out[name]=winreg.QueryValueEx(key,name)
   except FileNotFoundError:out[name]=None
  return out
def network():
 script="@{adapters=@(Get-NetAdapter -IncludeHidden | Where-Object Name -Like 'tunnelX-*' | Select-Object -ExpandProperty Name);routes=@(Get-NetRoute | Where-Object {$_.InterfaceAlias -like 'tunnelX-*' -or $_.RouteMetric -eq 4276} | Select-Object DestinationPrefix,InterfaceAlias,RouteMetric);dns=@(Get-DnsClientNrptRule | Where-Object Comment -Like 'tunnelX-*' | Select-Object -ExpandProperty Comment)}|ConvertTo-Json -Depth 5 -Compress"
 result=subprocess.run([os.environ['SystemRoot']+'/System32/WindowsPowerShell/v1.0/powershell.exe','-NoProfile','-NonInteractive','-Command',script],capture_output=True,text=True,encoding='utf-8',errors='replace',creationflags=subprocess.CREATE_NO_WINDOW,timeout=20,check=True)
 return json.loads(result.stdout)
before=registry();baseline=network();assert not any(baseline.values()),'An existing TUN is active; do not modify its network'
def local(path='/api/status',body=None):
 with closing(http.client.HTTPConnection('127.0.0.1',ports[2],timeout=55)) as c:
  c.request('GET','/api/status');resp=c.getresponse();import re
  nonce=re.search("script-src 'nonce-([^']+)'",resp.getheader('Content-Security-Policy')).group(1);data=resp.read()
  if body is None:return json.loads(data)
  c.request('POST',path,json.dumps(body),{'Origin':'http://'+addr,'X-CSRF-Token':nonce,'Content-Type':'application/json'});resp=c.getresponse();data=resp.read();assert resp.status==200,'Native TUN action failed: '+path+' '+data.decode(errors='replace')[:300];return json.loads(data)
def dns_query(kind):return struct.pack('!6H',4311,0x100,1,0,0,0)+b'\x03www\x06google\x03com\x00'+struct.pack('!HH',kind,1)
def dns_check(kind,tcp=False):
 query=dns_query(kind)
 if tcp:
  with socket.create_connection(('8.8.8.8',53),timeout=10) as s:
   s.sendall(struct.pack('!H',len(query))+query);reader=s.makefile('rb');size=struct.unpack('!H',reader.read(2))[0];answer=reader.read(size)
 else:
  with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as s:
   s.settimeout(10);s.sendto(query,('223.5.5.5',53));answer,_=s.recvfrom(65535)
 fields=struct.unpack('!6H',answer[:12]);assert fields[0]==4311 and fields[1]&0x8000 and fields[1]&15==0
 return fields[3]
def fetch(url):
 with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(url,timeout=20) as response:return response.status,response.read(2<<20)

def pause_owned_watcher():
 # Freeze only this isolated EXE's watcher, not the desktop/OS or user processes.
 # A 17-second gap exercises the production resume-detection and cleanup branch.
 script=f"@(Get-CimInstance Win32_Process -Filter 'ParentProcessId = {process.pid}' | Where-Object {{$_.Name -eq 'powershell.exe' -and $_.CommandLine -like '*EncodedCommand*'}} | Select-Object -ExpandProperty ProcessId)|ConvertTo-Json -Compress"
 found=subprocess.check_output(['powershell.exe','-NoProfile','-NonInteractive','-Command',script],creationflags=subprocess.CREATE_NO_WINDOW,text=True).strip()
 ids=json.loads(found);ids=ids if isinstance(ids,list) else [ids];assert len(ids)==1,'Expected exactly one isolated TUN watcher'
 from ctypes import wintypes
 kernel=ctypes.WinDLL('kernel32',use_last_error=True);native=ctypes.WinDLL('ntdll')
 kernel.OpenProcess.argtypes=[wintypes.DWORD,wintypes.BOOL,wintypes.DWORD];kernel.OpenProcess.restype=wintypes.HANDLE
 kernel.CloseHandle.argtypes=[wintypes.HANDLE]
 native.NtSuspendProcess.argtypes=[wintypes.HANDLE];native.NtSuspendProcess.restype=ctypes.c_long
 native.NtResumeProcess.argtypes=[wintypes.HANDLE];native.NtResumeProcess.restype=ctypes.c_long
 handle=kernel.OpenProcess(0x0800,False,ids[0]);assert handle,'Cannot open isolated watcher'
 suspended=False
 try:
  assert native.NtSuspendProcess(handle)==0;suspended=True;time.sleep(17)
 finally:
  if suspended:assert native.NtResumeProcess(handle)==0,'Could not resume test watcher'
  kernel.CloseHandle(handle)

def await_state(predicate,timeout=75):
 deadline=time.monotonic()+timeout
 while time.monotonic()<deadline:
  state=local()
  if predicate(state):return state
  time.sleep(.25)
 raise AssertionError('Expected recovery state was not reached')
try:
 process=subprocess.Popen([str(exe),'-headless','-config',str(stage/'settings.json'),'-state-dir',str(stage)],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,creationflags=subprocess.CREATE_NO_WINDOW)
 for _ in range(100):
  assert process.poll() is None,'Native TUN fixture exited'
  try:state=local();break
  except OSError:time.sleep(.1)
 else:raise AssertionError('Native fixture startup timeout')
 assert state['tun']['elevated'] and state['tun']['assets_ready']
 local('/api/login',dict(email=access['beta_email'],password=access['beta_password']))
 local('/api/proxy-mode',dict(proxy_mode='tun',direct_domains=''))
 local('/api/connect',dict(node_id=access['node_id'],system_proxy=False))
 state=local();assert state['connected'] and state['ech'] and state['tun']['dns_transport']=='h2_tcp';cases.append('real_exe_tun_with_production_default_routes_and_dns')
 assert dns_check(1)>0;cases.append('captured_udp_dns_to_existing_resolver_returns_google_A')
 if not state['tun']['ipv6_available']:
  assert dns_check(28)==0 and dns_check(65)==0;cases.append('unavailable_node_ipv6_filters_AAAA_and_HTTPS_hints')
  started=time.monotonic()
  try:
   # tun2socks accepts the local TCP handshake before SOCKS target dialing.
   # A rejected target must close/reset the stream, not merely finish connect().
   with socket.create_connection(('2404:6800:400b:c015::bc',443),timeout=3) as s:
    s.sendall(b'probe');assert s.recv(1)==b'', 'Unexpected IPv6 payload'
  except socket.timeout:raise AssertionError('Unavailable IPv6 did not fail promptly')
  except OSError:pass
  assert time.monotonic()-started<2;cases.append('literal_ipv6_rejected_without_connect_timeout')
 assert dns_check(1,True)>0;cases.append('captured_TCP_DNS_returns_google_A')
 status,body=fetch('https://www.google.com/generate_204');assert status==204;cases.append('google_https_204_without_http_or_socks_proxy')
 status,body=fetch('https://www.google.com/');assert status==200 and len(body)>1000;cases.append('google_homepage_TLS_and_body_through_TUN')
 status,body=fetch('https://proof.ovh.net/files/1Mb.dat');assert status==200 and len(body)==1048576;cases.append('one_mib_https_download_without_explicit_proxy')
 if args.resilience:
  await_state(lambda s:all(s['health'].get(k,{}).get('checked_at') and not s['health'][k]['error_kind'] for k in ['gateway','dns','egress']),20);cases.append('independent_gateway_dns_https_checks_are_healthy')
  old_connected=local()['connected_at'];pause_owned_watcher()
  await_state(lambda s:s['connected'] and not s.get('recovering') and s['connected_at']>old_connected)
  assert fetch('https://www.google.com/generate_204')[0]==204;cases.append('simulated_resume_rebuilds_full_TUN_and_restores_google')
  pause_owned_watcher();await_state(lambda s:s.get('recovering'),30)
  local('/api/cancel',{});await_state(lambda s:not s['connected'] and s['connection_state']=='disconnected',30)
  time.sleep(5);assert not local()['connected'] and not local().get('recovering');cases.append('cancelled_automatic_recovery_does_not_reconnect')
  assert network()==baseline;cases.append('cancelled_recovery_restores_owned_network_objects')
 local('/api/disconnect',{});assert not local()['connected'];cases.append('normal_disconnect')
 assert registry()==before and network()==baseline;cases.append('original_proxy_and_owned_network_objects_restored')
 local('/api/connect',dict(node_id=access['node_id'],system_proxy=False));assert fetch('https://www.google.com/generate_204')[0]==204;local('/api/disconnect',{});cases.append('second_full_TUN_connection_also_reaches_google')
 result={'passed':True,'version':version,'checked_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'desktop_sha256':hashlib.sha256(exe.read_bytes()).hexdigest(),'native_executable_used':True,'gui_rendered':False,'actual_default_routes_and_DNS_tested':True,'real_payment':False,'cases':cases}
finally:
 if process is not None and process.poll() is None:
  try:local('/api/disconnect',{});local('/api/exit',{});process.wait(timeout=20)
  except Exception:process.terminate();process.wait(timeout=10)
 for _ in range(10):
  final=network()
  if final==baseline:break
  time.sleep(1)
 assert final==baseline,'TUN acceptance left owned routes, adapter or DNS policy'
 assert registry()==before,'Windows proxy values changed'
(root/('docs/tun-product-acceptance-'+version+'.json')).write_text(json.dumps(result,indent=2)+'\n',encoding='utf-8');print(json.dumps(result),flush=True)
