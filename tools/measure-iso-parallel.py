"""Compare a fixed ISO range with bounded parallel requests; never log signed URLs."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime
import hashlib
import http.client
import json
from pathlib import Path
import ssl
import time
from urllib.parse import urlsplit

p = argparse.ArgumentParser()
p.add_argument('--profile', required=True)
p.add_argument('--proxy-port', type=int)
p.add_argument('--connections', type=int, default=4, choices=[1, 4, 8])
p.add_argument('--route', required=True)
p.add_argument('--out', required=True)
args = p.parse_args()
profile = json.loads(Path(args.profile).read_text(encoding='utf-8-sig'))
u = urlsplit(profile['url'])
assert u.scheme == 'https' and u.hostname == 'tempdelivery.13376767.xyz' and not u.username and not u.password
total = 32 << 20
piece = total // args.connections


def download(index):
    start = time.perf_counter()
    body = bytearray()
    connection = None
    item = dict(index=index, offset=index * piece, requested_bytes=piece)
    try:
        connection = http.client.HTTPSConnection('127.0.0.1' if args.proxy_port else u.hostname,
                       args.proxy_port or u.port or 443, timeout=15, context=ssl.create_default_context())
        if args.proxy_port:
            connection.set_tunnel(u.hostname, u.port or 443)
        path = u.path + ('?' + u.query if u.query else '')
        connection.request('GET', path, headers={'Range': f'bytes={index * piece}-{(index + 1) * piece - 1}', 'Accept-Encoding': 'identity'})
        r = connection.getresponse()
        item.update(status=r.status, content_range=r.getheader('Content-Range'),
                    ttfb_seconds=time.perf_counter() - start,
                    cdn_location=r.getheader('CF-Ray', '').split('-')[-1] or None)
        assert r.status == 206 and (r.getheader('Content-Range') or '').startswith(f'bytes {index * piece}-{(index + 1) * piece - 1}/')
        while len(body) < piece and time.perf_counter() - start < 30:
            if connection.sock is not None:
                connection.sock.settimeout(max(0.1, min(10, 30 - (time.perf_counter() - start))))
            data = r.read(min(65536, piece - len(body)))
            if not data:
                break
            body.extend(data)
    except Exception as error:
        item['error_type'] = type(error).__name__
    finally:
        if connection is not None:
            connection.close()
    elapsed = time.perf_counter() - start
    item.update(bytes=len(body), seconds=elapsed, MBps=len(body) / elapsed / 1e6,
                passed=len(body) == piece and 'error_type' not in item)
    return item, body


start = time.perf_counter()
with ThreadPoolExecutor(max_workers=args.connections) as pool:
    samples = list(pool.map(download, range(args.connections)))
elapsed = time.perf_counter() - start
n = sum(len(body) for _, body in samples)
digest = hashlib.sha256()
for _, body in samples:
    digest.update(body)
complete = all(item['passed'] for item, _ in samples)
sha256 = digest.hexdigest()
report = dict(checked_at=datetime.datetime.now().astimezone().isoformat(), route=args.route,
              filename=profile.get('filename'), connections=args.connections, bytes=n,
              seconds=elapsed, MBps=n / elapsed / 1e6, Mbps=n * 8 / elapsed / 1e6,
              sha256=sha256, passed=complete and sha256 == '6a240ee4a7c6faf52b15a871a68d89f49923a737f1ccfa068b73cb5ee5cf8b8f',
              bounded_partial_iso=True, samples=[item for item, _ in samples])
Path(args.out).write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
print(json.dumps(report), flush=True)
