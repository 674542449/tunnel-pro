param([ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+$')][string]$Version='', [string]$Python='python')
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
if (-not $Version) { $Version = (Get-Content -LiteralPath (Join-Path $root 'VERSION') -Raw).Trim() }
& $Python (Join-Path $root 'tools/versioning.py') --expect $Version
if ($LASTEXITCODE -ne 0) { throw 'Console and desktop versions must match VERSION before building.' }
& $Python (Join-Path $root 'tools/verify-tun-runtime.py')
if ($LASTEXITCODE -ne 0) { throw 'TUN runtime validation failed.' }
& $Python (Join-Path $root 'tools/build-desktop-logo.py')
if ($LASTEXITCODE -ne 0) { throw 'Desktop logo generation failed.' }
Push-Location (Join-Path $root 'desktop')
try {
    go mod vendor
    if ($LASTEXITCODE -ne 0) { throw 'Desktop vendoring failed' }
    & $Python ../tools/patch-vendor.py
    if ($LASTEXITCODE -ne 0) { throw 'Desktop H2 patches failed' }
    & $Python ../tools/test-desktop-binding.py
    if ($LASTEXITCODE -ne 0) { throw 'Actual Wails native binding regression failed' }
    node --check ui/app.js
    if ($LASTEXITCODE -ne 0) { throw 'Desktop JavaScript invalid' }
    wails build -m -nosyncgomod -s -skipbindings -tags http2legacy -webview2 error -o tunnelx-desktop.exe
    if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed' }
} finally { Pop-Location }
New-Item -ItemType Directory -Force (Join-Path $root "dist/desktop-$Version") | Out-Null
Copy-Item -LiteralPath (Join-Path $root 'desktop/build/bin/tunnelx-desktop.exe') -Destination (Join-Path $root "dist/desktop-$Version/tunnelx-desktop.exe") -Force
& $Python (Join-Path $root 'tools/verify-desktop-logo.py')
if ($LASTEXITCODE -ne 0) { throw 'Packaged Windows logo verification failed.' }

$runtimeDestination = Join-Path $root "dist/desktop-$Version/runtime"
New-Item -ItemType Directory -Force -Path $runtimeDestination | Out-Null
Get-ChildItem -LiteralPath (Join-Path $root 'desktop/runtime') | ForEach-Object {
    Copy-Item -LiteralPath $_.FullName -Destination $runtimeDestination -Recurse -Force
}
