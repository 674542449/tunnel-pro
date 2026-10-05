"""Run the built desktop against an explicitly supplied private beta fixture, without GUI.

The wrapper must provide a loopback-only remote download fixture. No production
accounts, Windows proxy values or real payments are changed by this test.
"""
import argparse
from contextlib import closing
import hashlib
import http.client
import json
import pathlib
import re
import socket
import subprocess
import time
import uuid
import winreg
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from versioning import check

root = pathlib.Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--private-fixture', required=True)
args = parser.parse_args()
private = pathlib.Path(args.private_fixture).resolve()
assert private.is_relative_to(root / '.local'), 'Use an isolated private workspace fixture'
access = json.loads((private / 'staging-access.json').read_bytes())
config = json.loads((private / 'staging-config.json').read_bytes())
version = check()
exe = root / 'dist' / ('desktop-' + version) / 'tunnelx-desktop.exe'
stage = private / ('desktop-product-' + uuid.uuid4().hex)
stage.mkdir()
cases = []

def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]

ports = [free_port() for _ in range(3)]
assert len(set(ports)) == 3
addr = '127.0.0.1:' + str(ports[2])
settings = dict(api_url=config['public_url'], socks_listen='127.0.0.1:' + str(ports[0]), http_listen='127.0.0.1:' + str(ports[1]), web_listen=addr)
(stage / 'settings.json').write_text(json.dumps(settings), encoding='utf-8')

def registry():
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER, r'Software\Microsoft\Windows\CurrentVersion\Internet Settings') as key:
        values = {}
        for name in ['ProxyEnable', 'ProxyServer', 'ProxyOverride', 'AutoConfigURL']:
            try:
                values[name] = winreg.QueryValueEx(key, name)
            except FileNotFoundError:
                values[name] = None
        return values

before = registry()
process = None

def local(path='/api/status', body=None, expected=200):
    with closing(http.client.HTTPConnection('127.0.0.1', ports[2], timeout=40)) as client:
        if body is None:
            client.request('GET', path)
        else:
            client.request('GET', '/api/status')
            response = client.getresponse()
            nonce = re.search("script-src 'nonce-([^']+)'", response.getheader('Content-Security-Policy')).group(1)
            response.read()
            client.request('POST', path, json.dumps(body), {'Origin': 'http://' + addr, 'X-CSRF-Token': nonce, 'Content-Type': 'application/json'})
        response = client.getresponse()
        data = response.read()
        assert response.status == expected, f'Native API {path} returned {response.status}, expected {expected}'
        return json.loads(data) if expected == 200 else None

def start():
    global process
    process = subprocess.Popen([str(exe), '-headless', '-config', str(stage / 'settings.json'), '-state-dir', str(stage)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, creationflags=subprocess.CREATE_NO_WINDOW)
    for _ in range(100):
        assert process.poll() is None, 'Native test process exited unexpectedly'
        try:
            return local()
        except OSError:
            time.sleep(.1)
    raise AssertionError('Native test listener did not start')

def stop():
    if process is not None and process.poll() is None:
        local('/api/exit', {})
        process.wait(timeout=15)

try:
    state = start()
    assert state['version'] == version and not state['logged_in']
    assert pathlib.Path(state['data_directory']).resolve() == stage
    cases.append('native_first_launch_uses_isolated_data_directory')
    public = local('/api/query', dict(path='/api/public/settings'))
    commerce = local('/api/query', dict(path='/api/public/commerce'))
    assert isinstance(public['registration'], bool) and isinstance(commerce['test_mode'], bool)
    local('/api/query', dict(path='/api/public/settings', body={'registration': True}), expected=400)
    cases.append('public_registration_commerce_are_read_only_before_login')
    with closing(http.client.HTTPConnection('127.0.0.1', ports[2], timeout=5)) as client:
        client.request('POST', '/api/preferences', '{}', {'Content-Type': 'application/json'})
        response = client.getresponse()
        response.read()
        assert response.status == 403
    cases.append('native_mutations_require_origin_and_csrf')
    local('/api/login', dict(email=access['beta_email'], password=access['beta_password']))
    assert local()['logged_in']
    cases.append('native_secure_login')
    local('/api/settings', dict(api_url=config['public_url'].rstrip('/') + '/'))
    assert local()['logged_in'] and local()['api_url'] == config['public_url'].rstrip('/')
    local('/api/settings', dict(api_url='https://invalid.example.test/?token=invalid'), expected=400)
    assert local()['logged_in']
    cases.append('native_normalized_same_service_and_invalid_edits_preserve_session')
    nodes = local('/api/query', dict(path='/api/nodes'))
    assert any(n['id'] == access['node_id'] for n in nodes)
    selected_node = next(n for n in nodes if n['id'] == access['node_id'])
    assert selected_node['access']['allowed'] is True
    assert all(isinstance(n['access']['allowed'], bool) for n in nodes)
    cases.append('controller_per_node_access_contract_is_available_to_native_client')
    latency = local('/api/probe', dict(node_id=access['node_id']))
    assert 0 <= latency['latency_ms'] < 25000
    local('/api/connect', dict(node_id='invalid-node-id', system_proxy=False), expected=400)
    assert not local()['connected']
    cases.append('native_node_probe_and_invalid_node_rejection')
    local('/api/preferences', dict(node_id=access['node_id'], system_proxy=False))
    stop()
    state = start()
    assert state['logged_in'] and state['preferences']['selected_node_id'] == access['node_id'] and state['preferences']['system_proxy'] is False
    assert state['preferences']['proxy_mode'] == 'global' and state['tun']['assets_ready'] is True
    cases.append('login_and_selected_node_proxy_preferences_survive_restart')
    with socket.socket() as occupied:
        occupied.bind(('127.0.0.1', ports[1]))
        occupied.listen()
        local('/api/connect', dict(node_id=access['node_id'], system_proxy=False), expected=400)
        assert local()['connection_state'] == 'disconnected'
        with socket.socket() as released:
            released.bind(('127.0.0.1', ports[0]))
    cases.append('occupied_http_port_rolls_back_socks_listener_and_allows_retry')
    local('/api/connect', dict(node_id=access['node_id'], system_proxy=False))
    state = local()
    assert state['connected'] and state['ech'] and state['mode'] == 'h2'
    assert state['connection_state'] == 'connected'
    cases.append('strict_h2_ech_connect_has_truthful_connection_state')
    local('/api/proxy-mode', dict(proxy_mode='bypass_cn',direct_domains='qq.com'),expected=400)
    assert local()['preferences']['proxy_mode']=='global'
    cases.append('native_mode_changes_blocked_while_connected')
    for _ in range(100):
        state = local()
        if state.get('health', {}).get('last_success', 0):
            break
        time.sleep(.1)
    assert state['health']['last_success'] > 0 and state['health']['consecutive_failures'] == 0
    cases.append('independent_node_entry_health_is_reported')
    size = 8 << 20
    with closing(http.client.HTTPConnection('127.0.0.1', ports[1], timeout=30)) as client:
        client.request('GET', 'http://127.0.0.1:18480/download?size=' + str(size))
        response = client.getresponse()
        data = response.read()
        assert response.status == 200 and len(data) == size
    block = bytes(i % 251 for i in range(32 << 10))
    assert data == block * (size // len(block))
    cases.append('eight_mib_proxy_download_matches_every_byte')
    small_size = 256 << 10
    with closing(http.client.HTTPConnection('127.0.0.1', ports[1], timeout=30)) as client:
        client.set_tunnel('127.0.0.1', 18480)
        client.request('GET', '/download?size=' + str(small_size))
        response = client.getresponse()
        assert response.status == 200 and response.read() == block * (small_size // len(block))
    cases.append('http_connect_tunnel_download_matches_every_byte')
    with socket.create_connection(('127.0.0.1', ports[0]), timeout=30) as sock:
        def exact(count):
            result = b''
            while len(result) < count:
                part = sock.recv(count - len(result))
                assert part, 'Unexpected SOCKS EOF'
                result += part
            return result
        sock.sendall(b'\x05\x01\x00')
        assert exact(2) == b'\x05\x00'
        sock.sendall(b'\x05\x01\x00\x01' + socket.inet_aton('127.0.0.1') + (18480).to_bytes(2, 'big'))
        header = exact(4)
        assert header[:2] == b'\x05\x00'
        if header[3] == 1:
            exact(4)
        elif header[3] == 4:
            exact(16)
        else:
            assert header[3] == 3
            exact(exact(1)[0])
        exact(2)
        sock.sendall(('GET /download?size=' + str(small_size) + ' HTTP/1.1\r\nHost: 127.0.0.1:18480\r\nConnection: close\r\n\r\n').encode())
        response = http.client.HTTPResponse(sock)
        response.begin()
        assert response.status == 200 and response.read() == block * (small_size // len(block))
    cases.append('socks5_tunnel_download_matches_every_byte')
    report_text = local('/api/diagnostics', {})['report']
    for forbidden in [access['beta_email'], access['beta_password'], config['admin_password'], config['public_url'], str(stage), '127.0.0.1:18480']:
        assert forbidden not in report_text, 'Support summary contains private data'
    assert version in report_text
    cases.append('exportable_diagnostics_exclude_identity_credentials_and_targets')
    sealed = (stage / 'state/auth.dpapi').read_bytes()
    assert access['beta_password'].encode() not in sealed and access['beta_email'].encode() not in sealed
    cases.append('saved_session_is_protected_with_dpapi')
    local('/api/disconnect', {})
    assert local()['connection_state'] == 'disconnected'
    local('/api/cancel', {})
    cases.append('disconnect_and_idle_cancel_leave_clean_state')
    local('/api/proxy-mode',dict(proxy_mode='tun',direct_domains='qq.com'))
    assert local()['preferences']['proxy_mode']=='tun'
    if not local()['tun']['elevated']:
        local('/api/connect',dict(node_id=access['node_id'],system_proxy=False),expected=400)
        assert not local()['connected']
        cases.append('native_tun_requires_admin_without_changing_network')
    local('/api/proxy-mode',dict(proxy_mode='bypass_cn',direct_domains='qq.com'))
    stop();state=start()
    assert state['preferences']['proxy_mode']=='bypass_cn' and state['preferences']['direct_domains']==['qq.com']
    cases.append('native_three_mode_preference_and_exceptions_survive_restart')
    direct_payload=b'local-bypass-fixture'*16384
    class DirectFixture(BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200);self.send_header('Content-Length',str(len(direct_payload)));self.end_headers();self.wfile.write(direct_payload)
        def log_message(self,*args): pass
    direct_server=ThreadingHTTPServer(('127.0.0.1',0),DirectFixture)
    direct_worker=threading.Thread(target=direct_server.serve_forever,daemon=True);direct_worker.start()
    try:
        local('/api/connect',dict(node_id=access['node_id'],system_proxy=False))
        with closing(http.client.HTTPConnection('127.0.0.1',ports[1],timeout=15)) as client:
            client.request('GET','http://127.0.0.1:'+str(direct_server.server_port)+'/')
            response=client.getresponse();assert response.status==200 and response.read()==direct_payload
        local('/api/disconnect',{})
    finally:
        direct_server.shutdown();direct_server.server_close();direct_worker.join(timeout=5)
    cases.append('native_bypass_download_reaches_local_origin_without_remote_tunnel')
    local('/api/logout', {})
    local('/api/login', dict(email=config['admin_email'], password=config['admin_password']), expected=400)
    cases.append('mfa_account_cannot_login_without_second_factor')
    # Leave the private fixture's finite recovery-code inventory intact; code
    # consumption/replay has dedicated engine/API coverage and prior native tests.
    stop()
    auth_file = stage / 'state/auth.dpapi'
    auth_file.write_bytes(b'isolated-corrupt-dpapi-fixture')
    state = start()
    assert not state['logged_in'] and state.get('startup_warning')
    assert any(p.is_file() and p.read_bytes() == b'isolated-corrupt-dpapi-fixture' for p in (stage / 'state').iterdir())
    cases.append('corrupt_local_credentials_show_relogin_warning_and_are_preserved')
    stop()
    assert registry() == before
    cases.append('existing_windows_proxy_values_are_unchanged')
    logs = [json.loads(line) for file in (stage / 'logs').glob('events-*.jsonl') for line in file.read_bytes().splitlines()]
    assert logs and not any(item.get('transport') == 'h3' for item in logs)
    cases.append('native_h2_diagnostics_are_persisted')
    result = dict(passed=True, checked_at=time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), desktop_version=version, desktop_sha256=hashlib.sha256(exe.read_bytes()).hexdigest(), native_executable_used=True, window_rendered=False, scope='isolated_beta_account_and_node', download_bytes=size, real_charge=False, cases=cases)
    (root / ('docs/admin-desktop-acceptance-' + version + '.json')).write_text(json.dumps(result, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(result))
finally:
    if process is not None and process.poll() is None:
        try:
            stop()
        except Exception:
            process.terminate()
            process.wait(timeout=10)
    assert registry() == before, 'Native acceptance changed the existing Windows proxy'
