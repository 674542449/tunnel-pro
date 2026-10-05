"""Package the account-based desktop without credentials or runtime state."""
import hashlib,json,pathlib,shutil,subprocess,zipfile
root=pathlib.Path(__file__).resolve().parent.parent;dist=root/'dist';stage=dist/'desktop-v0.4.0'
assert (stage/'tunnelx-desktop.exe').is_file()
settings=dict(api_url='https://test.xiaguamail.com/control',socks_listen='127.0.0.1:1080',http_listen='127.0.0.1:8088',web_listen='127.0.0.1:9080')
(stage/'settings.json').write_text(json.dumps(settings,indent=2)+'\n',encoding='utf-8')
shutil.copy2(root/'docs/PLATFORM-v0.4.0.md',stage/'README.md')
shutil.copy2(root/'THIRD_PARTY_PATCHES.md',stage/'THIRD_PARTY_PATCHES.md')
licenses=stage/'licenses';licenses.mkdir(exist_ok=True)
for file in (root/'desktop/vendor').rglob('*'):
 if file.is_file() and file.name.upper().startswith(('LICENSE','COPYING','NOTICE','PATENTS')):
  target=licenses/file.relative_to(root/'desktop/vendor');target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(file,target)
goroot=pathlib.Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
for name in ('LICENSE','PATENTS'):
 if (goroot/name).exists():
  (licenses/'go').mkdir(exist_ok=True);shutil.copy2(goroot/name,licenses/'go'/name)
(stage/'QUICKSTART.txt').write_text('tunnelX Desktop v0.4.0 / Windows x64\n\n1. Exit the previous tunnelX client to free ports 1080 and 8088.\n2. Extract this package into a writable directory.\n3. Start tunnelx-desktop.exe (Microsoft WebView2 Runtime required).\n4. Log in, select the H2 node, and connect.\n5. Closing the window keeps the tray app running; use Exit to stop and restore the proxy.\n\nAPI: https://test.xiaguamail.com/control\nNo account credentials are included in this package.\nAuthentication is encrypted for the current Windows user in state/auth.dpapi.\nLogs: logs/events-*.jsonl (recorded while connected).\nFull Chinese instructions: README.md.\n',encoding='utf-8')
files=['tunnelx-desktop.exe','settings.json','README.md','THIRD_PARTY_PATCHES.md','QUICKSTART.txt']
with zipfile.ZipFile(dist/'tunnelX-desktop-windows-x64-v0.4.0.zip','w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
 for name in files:z.write(stage/name,'tunnelX-desktop/'+name)
 for file in sorted(licenses.rglob('*')):
  if file.is_file():z.write(file,'tunnelX-desktop/'+file.relative_to(stage).as_posix())
print('Account-based desktop package created without login state, logs, or node credentials.')
