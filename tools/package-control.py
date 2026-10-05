"""Package management-only binaries and public source without deployment credentials."""
import argparse
import datetime
import hashlib
import json
import os
import pathlib
import re
import subprocess
import zipfile
from versioning import check

root = pathlib.Path(__file__).resolve().parent.parent
dist = root / 'dist'
parser = argparse.ArgumentParser()
source_version = check()
parser.add_argument('--version', default=source_version)
parser.add_argument('--with-desktop', action='store_true', help='also package the accepted desktop build of this same release')
desktop_source_version = source_version
parser.add_argument('--desktop-version', default=desktop_source_version)
args = parser.parse_args()
version = args.version
desktop_version = args.desktop_version
check(expected=version)
if desktop_version != version:
    parser.error("Console and desktop versions must match VERSION")
assert re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', desktop_version), 'Invalid desktop version'
assert re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', version), 'Invalid management release version'
assert f'const ConsoleVersion = "{version}"' in (root / 'internal/control/model.go').read_text(encoding='utf-8'), 'Release version differs from source'
build_dir = dist / ('control-' + version)
excluded = {'.local', 'dist', '.git', '__pycache__', 'logs', 'state', 'node_modules'}
secret_names = {'ech-key.json', 'inner-key.pem', 'server.json', 'agent.json', 'control.json', 'PRIVATE-ACCESS.txt', 'system-proxy.json', 'database.json', 'staging-access.json', 'production-config.json', 'staging-config.json', 'backup.key'}
secret_values = []
for relative, key in [('.local/platform/control.json', 'admin_password'), ('.local/platform/agent.json', 'agent_key'), ('.local/deployment/client.json', 'token'), ('.local/deployment/ech-key.json', 'private_key'), ('.local/node-install-v0.4.3/client.json', 'token')]:
    file = root / relative
    if file.exists():
        secret_values.append(json.loads(file.read_text(encoding='utf-8'))[key].encode())
private_key = root / '.local/deployment/inner-key.pem'
if private_key.exists():
    secret_values.append(private_key.read_bytes().strip())
if os.environ.get('TUNNELX_SSH_PASSWORD'):
    secret_values.append(os.environ['TUNNELX_SSH_PASSWORD'].encode())
setup_file = root / '.local/node-install-v0.4.3/private-setup.json'
if setup_file.exists():
    command = json.loads(setup_file.read_text(encoding='utf-8'))['command']
    secret_values.append(re.findall(r'[A-Za-z0-9_-]{43}', command)[-1].encode())

# Include commercial deployment credentials without exposing any in diagnostics.
private_fields = {'password', 'admin_password', 'beta_password', 'token', 'tunnel_token', 'agent_key', 'security_key', 'private_key', 'database_url', 'admin_token', 'beta_token', 'recovery_codes'}
def collect_private(value, key=''):
    if isinstance(value, dict):
        for k, v in value.items():
            collect_private(v, k)
    elif isinstance(value, list):
        for v in value:
            collect_private(v, key)
    elif isinstance(value, str) and key in private_fields and len(value) >= 8:
        secret_values.append(value.encode())

commercial_private = root / '.local/commercial-v0.5.0'
for file in commercial_private.glob('*.json'):
    collect_private(json.loads(file.read_bytes()))
for file in commercial_private.glob('*.key'):
    secret_values.append(file.read_bytes())

def safe(name, data):
    parts = pathlib.PurePosixPath(name).parts
    assert not any(p in excluded or p in secret_names for p in parts), 'Private or runtime file in package'
    assert not any(v and v in data for v in secret_values), 'Deployment credential in public package'

def add(z, file, name, executable=False):
    data = file.read_bytes()
    safe(name, data)
    info = zipfile.ZipInfo(name)
    info.create_system = 3
    info.external_attr = ((0o100755 if executable else 0o100644) << 16)
    info.compress_type = zipfile.ZIP_DEFLATED
    z.writestr(info, data)

binaries = [build_dir / arch / name for arch, name in [('windows-amd64', 'tunnelx-control.exe'), ('linux-arm64', 'tunnelx-control'), ('linux-amd64', 'tunnelx-control')]]
for file in binaries:
    assert file.is_file(), 'Build management binaries first'
    metadata = subprocess.check_output(['go', 'version', '-m', str(file)], text=True)
    assert 'quic-go' not in metadata and 'qpack' not in metadata
health_candidates = [root / ('docs/' + prefix + version + '.json') for prefix in ['admin-alignment-deployment-', 'admin-payment-deployment-']]
health_file = next((file for file in health_candidates if file.exists()), health_candidates[0])
if health_file.exists():
    health = json.loads(health_file.read_text(encoding='utf-8'))
    assert health['passed'] and health['console_version'] == version and health['controller_sha256'] == hashlib.sha256(binaries[1].read_bytes()).hexdigest()

control_archive = dist / ('tunnelX-control-' + version + '.zip')
with zipfile.ZipFile(control_archive, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=6) as z:
    for file in binaries:
        add(z, file, file.relative_to(build_dir).as_posix(), executable=file.suffix != '.exe')
    for file in sorted(build_dir.glob('*/node-artifacts/*/*')):
        assert file.name in {'tunnelx-server', 'tunnelx-admin'}
        add(z, file, file.relative_to(build_dir).as_posix(), executable=True)
    for relative in ['deploy/install-control.sh', 'deploy/tunnelx-control.service', 'deploy/backup-control.py', 'deploy/tunnelx-control-backup.service', 'deploy/tunnelx-control-backup.timer', 'THIRD_PARTY_PATCHES.md']:
        add(z, root / relative, relative, executable=relative.endswith('.sh'))
    readme_path = root / 'docs/CONTROL-DEPLOYMENT.md'
    readme = readme_path.read_text(encoding='utf-8').replace('(ADMIN-FIXES-v0.4.1.md)', '(docs/ADMIN-FIXES-v0.4.1.md)').encode()
    safe('README.md', readme)
    z.writestr('README.md', readme)
    add(z, readme_path, 'docs/' + readme_path.name)
    add(z, root / 'docs/ADMIN-FIXES-v0.4.1.md', 'docs/ADMIN-FIXES-v0.4.1.md')
    for file in sorted((root / 'docs').glob('admin-*-' + version + '.*')):
        if file.name not in {readme_path.name, 'admin-package-verification-' + version + '.json'}:
            add(z, file, 'docs/' + file.name)
    for folder in ['vendor', 'desktop/vendor']:
        for file in sorted((root / folder).rglob('*')):
            if file.is_file() and file.name.upper().startswith(('LICENSE', 'COPYING', 'NOTICE', 'PATENTS')):
                add(z, file, 'licenses/' + file.relative_to(root).as_posix())
    goroot = pathlib.Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
    for name in ['LICENSE', 'PATENTS']:
        if (goroot / name).exists():
            add(z, goroot / name, 'licenses/go/' + name)

source_archive = dist / ('tunnelX-source-' + version + '.zip')
with zipfile.ZipFile(source_archive, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=6) as z:
    for directory, folders, files in os.walk(root):
        folders[:] = sorted(f for f in folders if f not in excluded and not (pathlib.Path(directory).relative_to(root).as_posix() == 'desktop/build' and f == 'bin'))
        for name in sorted(files):
            file = pathlib.Path(directory) / name
            if file.is_symlink() or name in secret_names or file.suffix in {'.log', '.test', '.pyc', '.jsonl', '.exe', '.syso', '.dpapi'}:
                continue
            if name == 'admin-package-verification-' + version + '.json':
                continue
            add(z, file, 'tunnelX/' + file.relative_to(root).as_posix())

desktop_binary = desktop_archive = None
if args.with_desktop:
    # Use the same strict native/UI/runtime acceptance and secret scan as desktop-only packaging.
    import sys
    subprocess.run([sys.executable, str(root / 'tools/package-desktop.py')], check=True)
    desktop_binary = dist / ('desktop-' + version) / 'tunnelx-desktop.exe'
    desktop_archive = dist / ('tunnelX-desktop-windows-x64-' + version + '.zip')
archives = [control_archive, source_archive] + ([desktop_archive] if desktop_archive else [])
for archive in archives:
    with zipfile.ZipFile(archive) as z:
        assert z.testzip() is None, 'ZIP integrity check failed'
        assert len(z.namelist()) == len(set(z.namelist())), 'Duplicate file names in ZIP'
        for entry in z.infolist():
            safe(entry.filename, z.read(entry))
with zipfile.ZipFile(control_archive) as z:
    for file in binaries:
        assert z.read(file.relative_to(build_dir).as_posix()) == file.read_bytes()
with zipfile.ZipFile(source_archive) as z:
    assert 'tunnelX/tools/admin-ui-test/package-lock.json' in z.namelist()
    assert not any(n.endswith(('.exe', '.dpapi', '.syso')) for n in z.namelist())
if desktop_archive:
    with zipfile.ZipFile(desktop_archive) as z:
        assert z.read('tunnelX-desktop/tunnelx-desktop.exe') == desktop_binary.read_bytes()
        assert not any('state' in pathlib.PurePosixPath(n).parts or n.endswith(('.dpapi', '.jsonl', '.log')) for n in z.namelist())
manifest = [dict(path=f.relative_to(dist).as_posix(), bytes=f.stat().st_size, sha256=hashlib.sha256(f.read_bytes()).hexdigest()) for f in binaries + ([desktop_binary] if desktop_binary else []) + archives]
(dist / ('SHA256-control-' + version + '.json')).write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')
report = dict(passed=True, checked_at=datetime.datetime.now(datetime.timezone.utc).isoformat(), console_version=version, archives=[a.name for a in archives], desktop_packaged=bool(desktop_archive), desktop_version=desktop_version if desktop_archive else None, zip_integrity=True, packaged_binaries_match=True, deployed_controller_match=health_file.exists(), native_desktop_acceptance_match=bool(desktop_archive), deployment_credentials_excluded=True, source_has_reproducible_dom_tests=True, h2_only_dependencies=True, manifest='SHA256-control-' + version + '.json')
(root / ('docs/admin-package-verification-' + version + '.json')).write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
print(json.dumps(report))
