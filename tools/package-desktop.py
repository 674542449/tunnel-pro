"""Build a public desktop ZIP from an accepted EXE without repackaging the server."""
import datetime
import hashlib
import json
import os
import pathlib
import re
import subprocess
import zipfile
from versioning import check

root = pathlib.Path(__file__).resolve().parents[1]
version = check()
binary = root / 'dist' / ('desktop-' + version) / 'tunnelx-desktop.exe'
acceptance = json.loads((root / ('docs/admin-desktop-acceptance-' + version + '.json')).read_bytes())
ui_acceptance = json.loads((root / ('docs/desktop-product-ui-' + version + '.json')).read_bytes())
runtime_acceptance = json.loads((root / 'docs/desktop-runtime-startup-regression.json').read_bytes())
binding_acceptance = json.loads((root / ('docs/desktop-binding-' + version + '.json')).read_bytes())
logo_acceptance = json.loads((root / ('docs/desktop-logo-acceptance-' + version + '.json')).read_bytes())
tun_acceptance = json.loads((root / ('docs/tun-product-acceptance-' + version + '.json')).read_bytes())
assert acceptance['passed'] and ui_acceptance['passed'], 'Complete native and product interaction acceptance before packaging'
assert acceptance['desktop_version'] == version and ui_acceptance['desktop_version'] == version, 'Native and UI acceptance must match VERSION'
assert runtime_acceptance['passed'] and runtime_acceptance['desktop_version'] == version, 'Complete Wails runtime regression for this version before packaging'
assert binding_acceptance['passed'] and binding_acceptance['desktop_version'] == version, 'Complete actual Wails native binding regression before packaging'
assert binding_acceptance.get('source_hashes'), 'Binding acceptance must identify tested sources'
for name, digest in binding_acceptance['source_hashes'].items():
    assert hashlib.sha256((root / name).read_bytes()).hexdigest() == digest, 'Wails binding acceptance is stale: ' + name
assert acceptance['desktop_sha256'] == hashlib.sha256(binary.read_bytes()).hexdigest(), 'Acceptance does not match the EXE'
assert logo_acceptance['passed'] and logo_acceptance['version'] == version and logo_acceptance['desktop_sha256'] == acceptance['desktop_sha256'], 'Verify Windows logo resources on this EXE before packaging'
assert tun_acceptance['passed'] and tun_acceptance['version'] == version and tun_acceptance['actual_default_routes_and_DNS_tested'], 'Complete production TUN routing and DNS acceptance before packaging'
assert tun_acceptance['desktop_sha256'] == acceptance['desktop_sha256'], 'TUN acceptance does not match the EXE'
assert {'independent_gateway_dns_https_checks_are_healthy','simulated_resume_rebuilds_full_TUN_and_restores_google','cancelled_automatic_recovery_does_not_reconnect'}.issubset(tun_acceptance['cases']), 'Complete TUN recovery acceptance before packaging'
metadata = subprocess.check_output(['go', 'version', '-m', str(binary)], text=True)
assert 'quic-go' not in metadata and 'qpack' not in metadata, 'Unexpected HTTP/3 dependency'
private_fields = {'password', 'admin_password', 'beta_password', 'token', 'tunnel_token', 'agent_key', 'security_key', 'private_key', 'database_url', 'admin_token', 'beta_token', 'recovery_codes'}
private_values = []

def collect(value, key=''):
    if isinstance(value, dict):
        for k, v in value.items():
            collect(v, k)
    elif isinstance(value, list):
        for v in value:
            collect(v, key)
    elif isinstance(value, str) and key in private_fields and len(value) >= 8:
        private_values.append(value.encode())

for private in (root / '.local/commercial-v0.5.0').glob('*.json'):
    collect(json.loads(private.read_bytes()))
if os.environ.get('TUNNELX_SSH_PASSWORD'):
    private_values.append(os.environ['TUNNELX_SSH_PASSWORD'].encode())

def add(archive, name, data):
    assert not any(secret and secret in data for secret in private_values), 'Private deployment value detected in public artifact'
    assert not any(part in {'state', 'logs', '.local'} for part in pathlib.PurePosixPath(name).parts)
    info = zipfile.ZipInfo('tunnelX-desktop/' + name)
    info.create_system = 3
    info.external_attr = 0o100644 << 16
    info.compress_type = zipfile.ZIP_DEFLATED
    archive.writestr(info, data)

archive = root / 'dist' / ('tunnelX-desktop-windows-x64-' + version + '.zip')
with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=6) as z:
    add(z, 'tunnelx-desktop.exe', binary.read_bytes())
    runtime_dir = binary.parent / 'runtime'
    for name, digest in json.loads((root / 'internal/tunmode/assets.json').read_bytes()).items():
        assert hashlib.sha256((runtime_dir / name).read_bytes()).hexdigest() == digest, 'TUN asset hash mismatch'
    for asset in sorted(runtime_dir.rglob('*')):
        if asset.is_file():
            add(z, 'runtime/' + asset.relative_to(runtime_dir).as_posix(), asset.read_bytes())

    settings = dict(api_url='https://test.xiaguamail.com/control', socks_listen='127.0.0.1:1080', http_listen='127.0.0.1:8088', web_listen='127.0.0.1:9080')
    add(z, 'settings.json', (json.dumps(settings, indent=2) + '\n').encode())
    for relative, target in [('docs/WINDOWS-' + version + '.md', 'README.md'), ('THIRD_PARTY_PATCHES.md', 'THIRD_PARTY_PATCHES.md'), ('docs/desktop-product-ui-' + version + '.json', 'acceptance/ui.json'), ('docs/admin-desktop-acceptance-' + version + '.json', 'acceptance/native.json')]:
        add(z, target, (root / relative).read_bytes())
    add(z, 'acceptance/runtime.json', (root / 'docs/desktop-runtime-startup-regression.json').read_bytes())
    add(z, 'acceptance/binding.json', (root / ('docs/desktop-binding-' + version + '.json')).read_bytes())
    add(z, 'acceptance/logo.json', (root / ('docs/desktop-logo-acceptance-' + version + '.json')).read_bytes())
    add(z, 'acceptance/tun.json', (root / ('docs/tun-product-acceptance-' + version + '.json')).read_bytes())
    for file in sorted((root / 'desktop/vendor').rglob('*')):
        if file.is_file() and file.name.upper().startswith(('LICENSE', 'COPYING', 'NOTICE', 'PATENTS')):
            add(z, 'licenses/' + file.relative_to(root / 'desktop/vendor').as_posix(), file.read_bytes())
    goroot = pathlib.Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
    for name in ['LICENSE', 'PATENTS']:
        if (goroot / name).exists():
            add(z, 'licenses/go/' + name, (goroot / name).read_bytes())
with zipfile.ZipFile(archive) as z:
    assert z.testzip() is None and len(z.namelist()) == len(set(z.namelist()))
    assert z.read('tunnelX-desktop/tunnelx-desktop.exe') == binary.read_bytes()
    assert not any(n.endswith(('.dpapi', '.log', '.jsonl', '.key')) for n in z.namelist())
manifest = [dict(path=f.relative_to(root / 'dist').as_posix(), bytes=f.stat().st_size, sha256=hashlib.sha256(f.read_bytes()).hexdigest()) for f in [binary, archive]]
(root / 'dist' / ('SHA256-desktop-' + version + '.json')).write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')
report = dict(passed=True, checked_at=datetime.datetime.now(datetime.timezone.utc).isoformat(), desktop_version=version, archive=archive.name, archive_sha256=manifest[1]['sha256'], zip_integrity=True, exact_executable_accepted=True, product_interactions_accepted=True, credentials_excluded=True, h2_only=True, server_repackaged=False)
(root / ('docs/desktop-package-' + version + '.json')).write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
print(json.dumps(report))
