#!/usr/bin/env python3
"""Encrypted backups with an atomic, non-secret success/failure status."""
import datetime
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time


def write_status(parent, value):
    owner = parent.stat()
    fd, name = tempfile.mkstemp(prefix='.backup-status-', dir=parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(value, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, 0o600)
        if hasattr(os, 'chown'):
            os.chown(name, owner.st_uid, owner.st_gid)
        os.replace(name, parent / 'backup-status.json')
    finally:
        if os.path.exists(name):
            os.unlink(name)


def run_backup(config, key, directory, binary):
    settings = json.loads(config.read_text())
    data = pathlib.Path(settings['data_file'])
    parent = (data if data.is_absolute() else config.parent / data).parent
    try:
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(directory, 0o700)
        if not key.exists():
            fd = os.open(key, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, 'wb') as stream:
                stream.write(os.urandom(32))
                stream.flush()
                os.fsync(stream.fileno())
        # Microseconds avoid overwriting backups invoked twice in one second.
        target = directory / (datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ') + '.txbk')
        subprocess.run([str(binary), '-config', str(config), '-backup', str(target), '-backup-key', str(key)],
                       capture_output=True, text=True, timeout=120, check=True)
        if not target.is_file() or target.stat().st_size == 0:
            raise RuntimeError('backup output missing')
        files = sorted(directory.glob('*.txbk'), key=lambda path: (path.stat().st_mtime_ns, path.name))
        for old in files[:-336]:
            old.unlink()
        write_status(parent, dict(time=int(time.time()), bytes=target.stat().st_size,
                                  sha256=hashlib.sha256(target.read_bytes()).hexdigest()))
        return target.name
    except Exception:
        # Preserve the last success, but never copy subprocess output or secrets.
        previous = {}
        try:
            saved = json.loads((parent / 'backup-status.json').read_text())
            previous = {k: saved[k] for k in ('time', 'bytes', 'sha256') if k in saved}
        except (OSError, ValueError, TypeError):
            pass
        previous['failed_at'] = int(time.time())
        try:
            write_status(parent, previous)
        except OSError:
            pass
        raise RuntimeError('Encrypted control backup failed; inspect database, storage and private configuration') from None


if __name__ == '__main__':
    try:
        name = run_backup(pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else '/etc/tunnelx-control/control.json'),
                          pathlib.Path('/etc/tunnelx-control/backup.key'), pathlib.Path('/var/backups/tunnelx-control'),
                          pathlib.Path('/opt/tunnelx/bin/tunnelx-control'))
        print('Encrypted backup completed: ' + name)
    except Exception:
        raise SystemExit('Encrypted control backup failed; inspect database, storage and private configuration')
