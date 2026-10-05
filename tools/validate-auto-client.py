"""Check the actual Windows executable's default TCP/UDP routing without a browser."""
import argparse
from contextlib import closing
import datetime
import http.client
import json
import pathlib
import secrets
import socket
import ssl
import struct

p = argparse.ArgumentParser()
p.add_argument('--socks-port', type=int, required=True)
p.add_argument('--http-port', type=int, required=True)
p.add_argument('--web-port', type=int, required=True)
p.add_argument('--out', default='docs/acceptance-auto-client-v0.2.json')
p.add_argument('--expect-mode', choices=('h2',), default='h2')
p.add_argument('--expect-tcp', choices=('h2',), default='h2')
p.add_argument('--expect-udp', choices=('h2',), default='h2')
args = p.parse_args()
root = pathlib.Path(__file__).resolve().parent.parent


def status():
    with closing(http.client.HTTPConnection('127.0.0.1', args.web_port, timeout=5)) as c:
        c.request('GET', '/api/status')
        r = c.getresponse()
        assert r.status == 200
        return json.loads(r.read())


def recv_full(c, n):
    data = b''
    while len(data) < n:
        chunk = c.recv(n - len(data))
        if not chunk:
            raise IOError('SOCKS control connection closed')
        data += chunk
    return data


def skip_name(data, offset):
    while True:
        n = data[offset]
        if n & 0xc0 == 0xc0:
            return offset + 2
        if n == 0:
            return offset + 1
        assert n <= 63
        offset += 1 + n


def udp_dns():
    query_id = secrets.randbelow(65536)
    name = b''.join(bytes([len(label)]) + label for label in b'test.xiaguamail.com'.split(b'.')) + b'\0'
    query = struct.pack('!6H', query_id, 0x0100, 1, 0, 0, 0) + name + struct.pack('!2H', 1, 1)
    with socket.create_connection(('127.0.0.1', args.socks_port), timeout=10) as c:
        c.sendall(bytes([5, 1, 0]))
        assert recv_full(c, 2) == bytes([5, 0])
        c.sendall(bytes([5, 3, 0, 1, 0, 0, 0, 0, 0, 0]))
        r = recv_full(c, 10)
        assert r[:4] == bytes([5, 0, 0, 1])
        relay = (socket.inet_ntoa(r[4:8]), struct.unpack('!H', r[8:10])[0])
        header = bytes([0, 0, 0, 1, 8, 8, 8, 8]) + struct.pack('!H', 53)
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as u:
            u.settimeout(10)
            u.sendto(header + query, relay)
            reply, sender = u.recvfrom(4096)
            assert sender == relay and reply[:10] == header
    data = reply[10:]
    response_id, flags, questions, answers, _, _ = struct.unpack('!6H', data[:12])
    assert response_id == query_id and flags & 0x8000 and flags & 0x000f == 0
    offset = 12
    for _ in range(questions):
        offset = skip_name(data, offset) + 4
    addresses = []
    for _ in range(answers):
        offset = skip_name(data, offset)
        kind, family, _, length = struct.unpack('!2HIH', data[offset:offset + 10])
        offset += 10
        if kind == 1 and family == 1 and length == 4:
            addresses.append(socket.inet_ntoa(data[offset:offset + 4]))
        offset += length
    assert '146.56.99.255' in addresses
    return dict(resolver='8.8.8.8:53', domain='test.xiaguamail.com', ipv4=addresses, response_bytes=len(data))


before = status()
assert before['mode'] == args.expect_mode
with closing(http.client.HTTPSConnection('127.0.0.1', args.http_port, timeout=20, context=ssl.create_default_context())) as c:
    c.set_tunnel('test.xiaguamail.com', 443)
    c.request('GET', '/')
    r = c.getresponse()
    assert r.status == 200 and b'Web service is online.' in r.read()
tcp = status()
for carrier in ('h2',): assert tcp[carrier+'_streams'] == before[carrier+'_streams'] + int(carrier==args.expect_tcp)
assert tcp['tcp_carrier'] == int(args.expect_tcp[-1]) and tcp['ech'] and tcp['privacy'] == 'strict'
dns = udp_dns()
udp = status()
for carrier in ('h2',): assert udp[carrier+'_streams'] == tcp[carrier+'_streams'] + int(carrier==args.expect_udp)
assert udp['ech'] and udp['privacy'] == 'strict' and not udp['system_proxy']
report = dict(checked_at=datetime.datetime.now().astimezone().isoformat(), passed=True,
              tcp_carrier=args.expect_tcp, udp_carrier=args.expect_udp, tcp_new_streams=1, udp_new_streams=1,
              strict_ech_accepted=True, application_tls_verified=True, dns=dns,
              system_proxy_enabled=False, listeners=dict(socks=args.socks_port, http=args.http_port, web=args.web_port))
(root / args.out).write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
print(json.dumps(report))
