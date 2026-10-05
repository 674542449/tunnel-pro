"""Rebuild the pinned, unmodified tun2socks helper shipped with tunnelX.

Requires Go >= 1.26.3. This intentionally does not replace the signed Wintun DLL.
Run the desktop build afterwards to embed the regenerated executable hash.
"""
import hashlib,json,os,pathlib,shutil,subprocess,tempfile,zipfile
root=pathlib.Path(__file__).resolve().parents[1]
runtime=root/'desktop/runtime'
pin=json.loads((runtime/'tun-build.json').read_bytes())
archive=runtime/'tun2socks-source.zip'
assert hashlib.sha256(archive.read_bytes()).hexdigest()==pin['source_sha256'],'Source archive hash mismatch'
parent=root/'.local/tun-runtime-build';parent.mkdir(parents=True,exist_ok=True)
stage=pathlib.Path(tempfile.mkdtemp(dir=parent))
with zipfile.ZipFile(archive) as z:
    for name in z.namelist():
        destination=(stage/name).resolve()
        assert destination.is_relative_to(stage.resolve()),'Unsafe source archive path'
    z.extractall(stage)
source=next(p.parent for p in stage.rglob('go.mod') if p.parent.name.startswith('tun2socks-'))
env=os.environ.copy();env.update(GOOS='windows',GOARCH='amd64',CGO_ENABLED='0',GOCACHE=str(root/'.local/go-cache'),GOMODCACHE=str(root/'.local/tun-go-mod'),GOPATH=str(root/'.local/tun-go'))
exe=stage/'tun2socks.exe'
subprocess.run(['go','build','-trimpath','-ldflags=-s -w','-o',str(exe),'.'],cwd=source,env=env,check=True)
shutil.copy2(exe,runtime/'tun2socks.exe')
manifest=root/'internal/tunmode/assets.json';hashes=json.loads(manifest.read_bytes());hashes['tun2socks.exe']=hashlib.sha256(exe.read_bytes()).hexdigest();manifest.write_text(json.dumps(hashes,indent=2)+'\n',encoding='utf-8')
print('Pinned TUN helper rebuilt; rebuild and accept the desktop before publishing.')
