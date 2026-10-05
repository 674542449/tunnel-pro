"""Measure a bounded NTriver ISO sample; keep temporary signed URLs out of reports."""
import argparse
import datetime
import hashlib
import http.client
import json
import pathlib
import ssl
import time
import urllib.parse

p = argparse.ArgumentParser()
p.add_argument('--profile', required=True)
p.add_argument('--proxy-port', type=int)
p.add_argument('--route', required=True)
p.add_argument('--bytes', type=int, default=32 << 20)
p.add_argument('--offset', type=int, default=0)
p.add_argument('--seconds', type=int, default=30)
p.add_argument('--out', required=True)
args = p.parse_args()
assert 1 <= args.bytes <= 128 << 20 and 1 <= args.seconds <= 60 and args.offset >= 0
profile = json.loads(pathlib.Path(args.profile).read_text(encoding='utf-8-sig'))
url = profile['url']
start = time.perf_counter()
digest = hashlib.sha256()
n = 0
item = dict(checked_at=datetime.datetime.now().astimezone().isoformat(), route=args.route,
            filename=profile.get('filename'), requested_bytes=args.bytes, offset=args.offset, limit_seconds=args.seconds)
connection = None
try:
    for hop in range(5):
        u = urllib.parse.urlsplit(url)
        assert u.scheme == 'https' and u.hostname == 'tempdelivery.13376767.xyz' and not u.username and not u.password
        item['download_host'] = u.hostname
        port = u.port or 443
        connection = http.client.HTTPSConnection('127.0.0.1' if args.proxy_port else u.hostname,
                    args.proxy_port or port, timeout=min(20, args.seconds), context=ssl.create_default_context())
        if args.proxy_port:
            connection.set_tunnel(u.hostname, port)
        path = u.path or '/'
        if u.query:
            path += '?' + u.query
        connection.request('GET', path, headers={'Range': f'bytes={args.offset}-{args.offset + args.bytes - 1}', 'Accept-Encoding': 'identity'})
        response = connection.getresponse()
        if response.status in (301, 302, 303, 307, 308):
            url = urllib.parse.urljoin(url, response.getheader('Location', ''))
            connection.close()
            continue
        break
    item.update(status=response.status, content_range=response.getheader('Content-Range'),
                range_honored=response.status == 206, cache_status=response.getheader('CF-Cache-Status'),
                cdn_location=(response.getheader('CF-Ray', '').split('-')[-1] or None),
                ttfb_seconds=time.perf_counter() - start)
    if response.status not in (200, 206):
        raise RuntimeError('Origin rejected sample')
    if response.status == 206 and not (response.getheader('Content-Range') or '').startswith(f'bytes {args.offset}-{args.offset + args.bytes - 1}/'):
        raise RuntimeError('Origin returned a different range')
    if response.status == 200 and args.offset:
        raise RuntimeError('Origin ignored the requested offset')
    while n < args.bytes and time.perf_counter() - start < args.seconds:
        if connection.sock is not None:
            connection.sock.settimeout(max(0.1, min(10, args.seconds - (time.perf_counter() - start))))
        chunk = response.read(min(65536, args.bytes - n))
        if not chunk:
            break
        n += len(chunk)
        digest.update(chunk)
except Exception as error:
    # Exception strings can contain signed request URLs; report only the type.
    item['error_type'] = type(error).__name__
finally:
    if connection is not None:
        connection.close()
seconds = time.perf_counter() - start
item.update(bytes=n, seconds=seconds, MBps=n / seconds / 1e6, Mbps=n * 8 / seconds / 1e6,
            sha256=digest.hexdigest(), completed_sample=n == args.bytes,
            passed=n == args.bytes and 'error_type' not in item, bounded_partial_iso=True)
pathlib.Path(args.out).write_text(json.dumps(item, indent=2) + '\n', encoding='utf-8')
print(json.dumps(item), flush=True)
