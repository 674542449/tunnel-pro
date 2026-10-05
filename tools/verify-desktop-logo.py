"""Read Windows EXE resources without launching the client; verify packaged branding."""
import ctypes
from ctypes import wintypes as W
import hashlib
import io
import json
import struct
from PIL import Image
from versioning import ROOT, check

version = check()
exe = ROOT / ('dist/desktop-' + version + '/tunnelx-desktop.exe')
k = ctypes.WinDLL('kernel32', use_last_error=True)
k.LoadLibraryExW.argtypes = [W.LPCWSTR, W.HANDLE, W.DWORD]
k.LoadLibraryExW.restype = W.HMODULE
k.FindResourceW.argtypes = [W.HMODULE, ctypes.c_void_p, ctypes.c_void_p]
k.FindResourceW.restype = W.HANDLE
k.SizeofResource.argtypes = [W.HMODULE, W.HANDLE]
k.SizeofResource.restype = W.DWORD
k.LoadResource.argtypes = [W.HMODULE, W.HANDLE]
k.LoadResource.restype = W.HANDLE
k.LockResource.argtypes = [W.HANDLE]
k.LockResource.restype = ctypes.c_void_p
k.FreeLibrary.argtypes = [W.HMODULE]
h = k.LoadLibraryExW(str(exe), None, 2)  # LOAD_LIBRARY_AS_DATAFILE: never execute the EXE.
assert h, ctypes.get_last_error()


def resource(kind, name):
    r = k.FindResourceW(h, name, kind)
    assert r, (kind, name, ctypes.get_last_error())
    p = k.LockResource(k.LoadResource(h, r))
    assert p
    return ctypes.string_at(p, k.SizeofResource(h, r))


try:
    group = resource(14, 3)  # Wails' native window loads icon group 3.
    assert struct.unpack_from('<HHH', group) == (0, 1, 9)
    sizes = []
    with Image.open(ROOT / 'desktop/build/windows/icon.ico') as ico:
        for i in range(9):
            w, height, colors, reserved, planes, depth, size, rid = struct.unpack_from('<BBBBHHIH', group, 6+i*14)
            blob = resource(3, rid)
            assert len(blob) == size
            rebuilt = struct.pack('<HHHBBBBHHII', 0, 1, 1, w, height, colors, reserved, planes, depth, size, 22) + blob
            with Image.open(io.BytesIO(rebuilt)) as image:
                side = w or 256
                assert image.size == (side, side)
                assert image.convert('RGBA').tobytes() == ico.ico.getimage((side, side)).convert('RGBA').tobytes()
                sizes.append(side)
    assert sorted(sizes) == [16, 20, 24, 32, 40, 48, 64, 128, 256]
    manifest = resource(24, 1).decode('utf-8')
    assert 'asInvoker' in manifest and 'requireAdministrator' not in manifest
    assert version[1:] + '.0' in manifest
    version_info = resource(16, 1)
    assert 'tunnelX'.encode('utf-16le') in version_info
    assert version[1:].encode('utf-16le') in version_info
    assert (ROOT / 'desktop/ui/logo.svg').read_bytes() in exe.read_bytes()
    assert (ROOT / 'desktop/build/windows/tray.ico').read_bytes() == (ROOT / 'desktop/build/windows/icon.ico').read_bytes()
    report = dict(passed=True, version=version, desktop_sha256=hashlib.sha256(exe.read_bytes()).hexdigest(),
                  windows_icon_group_id=3, icon_sizes=sorted(sizes), resource_pixels_match=True,
                  svg_embedded=True, tray_matches=True, product_version_embedded=True,
                  execution_level='asInvoker', gui_visual_acceptance=False)
    (ROOT / ('docs/desktop-logo-acceptance-' + version + '.json')).write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(report))
finally:
    k.FreeLibrary(h)
