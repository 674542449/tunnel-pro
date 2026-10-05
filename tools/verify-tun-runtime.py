"""Verify shipped TUN executables and their redistribution inputs."""
import hashlib,json,pathlib
root=pathlib.Path(__file__).resolve().parents[1]
runtime=root/'desktop/runtime'
expected=json.loads((root/'internal/tunmode/assets.json').read_bytes())
assert set(expected)=={'tun2socks.exe','wintun.dll'}
for name,digest in expected.items():
    assert hashlib.sha256((runtime/name).read_bytes()).hexdigest()==digest,'TUN component hash mismatch: '+name
for name in ['tun2socks-LICENSE.txt','wintun-LICENSE.txt','routing-domains-LICENSE.txt','routing-ip-LICENSE.txt','tun2socks-source.zip','tun2socks-commit.txt','wintun-source.json']:
    assert (runtime/name).stat().st_size>0,'Missing TUN redistribution input: '+name
print('TUN runtime hashes and redistribution inputs verified')
