"""Final release gate: source, accepted EXE, published ZIP and every live console agree."""
import argparse
import datetime
import hashlib
import json
import pathlib
import urllib.parse
import urllib.request
from versioning import ROOT, check


def read_json(path):
    return json.loads(path.read_bytes())


def get_json(url):
    with urllib.request.urlopen(url, timeout=30) as response:
        return json.load(response)


def verify(sites, root=ROOT):
    version = check(root)
    exe = root / ('dist/desktop-' + version + '/tunnelx-desktop.exe')
    archive = root / ('dist/tunnelX-desktop-windows-x64-' + version + '.zip')
    sha = hashlib.sha256(exe.read_bytes()).hexdigest()
    zip_sha = hashlib.sha256(archive.read_bytes()).hexdigest()
    native = read_json(root / ('docs/admin-desktop-acceptance-' + version + '.json'))
    package = read_json(root / ('docs/desktop-package-' + version + '.json'))
    if not native.get('passed') or native.get('desktop_sha256') != sha:
        raise ValueError('EXE differs from native acceptance')
    if not package.get('passed') or package.get('desktop_version') != version or package.get('archive_sha256') != zip_sha:
        raise ValueError('ZIP differs from accepted release')
    verified = []
    downloaded = set()
    for site in sites:
        site = site.rstrip('/')
        parsed = urllib.parse.urlsplit(site)
        if parsed.scheme != 'https' or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError('Site must be a public HTTPS base URL')
        health = get_json(site + '/health')
        release = get_json(site + '/api/public/commerce')['release']
        if not health.get('ready') or health.get('version') != version or release.get('version') != version:
            raise ValueError('Live console and published EXE version differ at ' + site)
        url = release.get('url', '')
        target = urllib.parse.urlsplit(url)
        if target.scheme != 'https' or not target.hostname or target.username or target.password or release.get('sha256') != zip_sha:
            raise ValueError('Invalid or mismatched public archive at ' + site)
        if url not in downloaded:
            digest = hashlib.sha256()
            with urllib.request.urlopen(url, timeout=60) as response:
                if urllib.parse.urlsplit(response.url).scheme != 'https':
                    raise ValueError('Public download redirected away from HTTPS')
                while chunk := response.read(1024 * 1024):
                    digest.update(chunk)
            if digest.hexdigest() != zip_sha:
                raise ValueError('Public download differs from accepted ZIP')
            downloaded.add(url)
        verified.append({'site': site, 'console_version': health['version'], 'desktop_version': release['version'], 'download_url': url})
    return dict(passed=True, version=version, checked_at=datetime.datetime.now(datetime.timezone.utc).isoformat(), desktop_sha256=sha, archive_sha256=zip_sha, sites=verified, installed_clients_auto_updated=False)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--site', action='append', required=True, help='Each deployed HTTPS console base; repeat for beta')
    args = parser.parse_args()
    report = verify(args.site)
    target = ROOT / ('docs/unified-release-' + report['version'] + '.json')
    target.write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(report))
