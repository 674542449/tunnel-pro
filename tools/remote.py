"""SSH deployment helper. Password is read only from TUNNELX_SSH_PASSWORD."""
import argparse
import base64
import hashlib
import os
import pathlib
import sys
import paramiko

p = argparse.ArgumentParser()
p.add_argument('--host', required=True)
p.add_argument('--user', default='root')
p.add_argument('--known-hosts', default='.local/known_hosts')
p.add_argument('--timeout', type=int, default=45)
s = p.add_subparsers(dest='action', required=True)
r = s.add_parser('run')
r.add_argument('command', nargs='?')
r.add_argument('--script')
for action in ('upload', 'download'):
    a = s.add_parser(action)
    a.add_argument('source')
    a.add_argument('destination')
args = p.parse_args()
known = pathlib.Path(args.known_hosts)
known.parent.mkdir(parents=True, exist_ok=True)
c = paramiko.SSHClient()
c.load_system_host_keys()
if known.exists():
    c.load_host_keys(str(known))
class TrustFirst(paramiko.MissingHostKeyPolicy):
    def missing_host_key(self, client, hostname, key):
        digest = base64.b64encode(hashlib.sha256(key.asbytes()).digest()).decode().rstrip('=')
        print('First SSH host key pinned: SHA256:' + digest, file=sys.stderr)
        client.get_host_keys().add(hostname, key.get_name(), key)
        client.save_host_keys(str(known))
c.set_missing_host_key_policy(TrustFirst())
try:
    c.connect(args.host, username=args.user, password=os.environ['TUNNELX_SSH_PASSWORD'],
              timeout=args.timeout, auth_timeout=args.timeout, banner_timeout=args.timeout,
              look_for_keys=False, allow_agent=False)
    if args.action == 'run':
        command = pathlib.Path(args.script).read_text(encoding='utf-8') if args.script else args.command
        if not command:
            raise ValueError('command or --script required')
        _, stdout, stderr = c.exec_command(command, timeout=args.timeout)
        sys.stdout.buffer.write(stdout.read())
        sys.stderr.buffer.write(stderr.read())
        code = stdout.channel.recv_exit_status()
    else:
        with c.open_sftp() as ftp:
            if args.action == 'upload':
                ftp.put(args.source, args.destination)
            else:
                ftp.get(args.source, args.destination)
        code = 0
finally:
    c.close()
sys.exit(code)
