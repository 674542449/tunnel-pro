"""Node-local private CA and atomic leaf renewal. Never publish the CA private key."""
import fcntl
import grp
import json
import os
import pathlib
import re
import secrets
import shutil
import subprocess
import sys
import tempfile

os.umask(0o077)
root = pathlib.Path(sys.argv[1]).resolve()
gid = grp.getgrnam(sys.argv[2]).gr_gid
initialize = len(sys.argv) == 4 and sys.argv[3] == '--initialize'


def run(*args, data=None):
    return subprocess.run(args, input=data, check=True, capture_output=True, timeout=30).stdout


def mode(path, permissions):
    os.chown(path, 0, gid)
    os.chmod(path, permissions)


def valid_name(name):
    return len(name) <= 253 and '.' in name and all(re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', x) for x in name.split('.'))


def key_matches(cert, key):
    pub = run('openssl', 'x509', '-in', str(cert), '-pubkey', '-noout')
    assert run('openssl', 'pkey', '-pubin', '-outform', 'DER', data=pub) == run('openssl', 'pkey', '-in', str(key), '-pubout', '-outform', 'DER'), 'Certificate/key mismatch'


with (root / '.private-cert.lock').open('a') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    names = [(root / 'public-domain').read_text().strip(), json.loads((root / 'server.json').read_text())['inner_name']]
    assert all(valid_name(n) for n in names), 'Invalid certificate name'
    dest = root / 'private-cert'
    dest.mkdir(mode=0o750, exist_ok=True)
    mode(dest, 0o750)
    ca_dir = dest / 'ca'
    ca, ca_key = ca_dir / 'cert.pem', ca_dir / 'key.pem'
    current = dest / 'current'
    if not ca_dir.exists():
        assert initialize and not current.exists(), 'CA missing; restore the original CA instead of changing client trust'
        temp = pathlib.Path(tempfile.mkdtemp(prefix='.ca-', dir=dest))
        try:
            run('openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-days', '3650', '-sha256', '-subj', '/CN=tunnelX node private CA', '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign', '-keyout', str(temp / 'key.pem'), '-out', str(temp / 'cert.pem'))
            os.replace(temp, ca_dir)
        finally:
            if temp.exists(): shutil.rmtree(temp)
    assert ca.is_file() and ca_key.is_file(), 'Incomplete CA; restore node backup'
    mode(ca_dir, 0o700)
    mode(ca_key, 0o600)
    key_matches(ca, ca_key)
    run('openssl', 'x509', '-in', str(ca), '-checkend', str(120 * 86400), '-noout')
    trusted = root / 'origin-ca.pem'
    if initialize:
        if current.exists(): assert trusted.read_bytes() == ca.read_bytes(), 'Published CA changed'
        temporary = root / '.origin-ca.new'
        temporary.write_bytes(ca.read_bytes()); mode(temporary, 0o640); os.replace(temporary, trusted)
    else:
        assert trusted.read_bytes() == ca.read_bytes(), 'Published CA changed'
    try:
        key_matches(current / 'cert.pem', current / 'key.pem')
        for name in names:
            run('openssl', 'verify', '-CAfile', str(ca), '-purpose', 'sslserver', '-verify_hostname', name, str(current / 'cert.pem'))
        run('openssl', 'x509', '-in', str(current / 'cert.pem'), '-checkend', str(30 * 86400), '-noout')
    except (AssertionError, subprocess.CalledProcessError):
        generation = pathlib.Path(tempfile.mkdtemp(prefix='cert-', dir=dest))
        committed = False
        try:
            mode(generation, 0o750)
            key, cert, csr, ext = (generation / n for n in ('key.pem', 'cert.pem', 'request.pem', 'extensions'))
            ext.write_text('basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=serverAuth\nsubjectAltName=' + ','.join('DNS:' + n for n in dict.fromkeys(names)) + '\n')
            # DNS identity lives in SAN; CN has a shorter limit than a valid DNS name.
            run('openssl', 'req', '-new', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-subj', '/CN=tunnelX node', '-keyout', str(key), '-out', str(csr))
            run('openssl', 'x509', '-req', '-in', str(csr), '-CA', str(ca), '-CAkey', str(ca_key), '-set_serial', '0x' + secrets.token_hex(16), '-days', '90', '-sha256', '-extfile', str(ext), '-out', str(cert))
            key_matches(cert, key)
            for name in names:
                run('openssl', 'verify', '-CAfile', str(ca), '-purpose', 'sslserver', '-verify_hostname', name, str(cert))
            mode(cert, 0o640); mode(key, 0o640)
            csr.unlink(); ext.unlink()
            link = dest / ('.next-' + secrets.token_hex(8))
            link.symlink_to(generation.name)
            os.replace(link, current)
            committed = True
            # Keep one previous generation for in-flight certificate loads/rollback.
            old = sorted((p for p in dest.glob('cert-*') if p.is_dir() and not p.is_symlink()), key=lambda p: p.stat().st_mtime, reverse=True)
            for p in old[2:]: shutil.rmtree(p)
        finally:
            if not committed: shutil.rmtree(generation)
    print('Private certificate ready; CA unchanged; names verified.')
