param([string]$Python='python', [switch]$SkipTests)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
Push-Location $root
try {
    & $Python tools/versioning.py
    if ($LASTEXITCODE -ne 0) { throw 'Product version validation failed' }
    & $Python tools/test-versioning.py
    if ($LASTEXITCODE -ne 0) { throw 'Product version regression failed' }
    & $Python tools/test-release-gate.py
    if ($LASTEXITCODE -ne 0) { throw 'Published release validation regression failed' }
    & "$PSScriptRoot/build-control.ps1" -Python $Python -SkipTests:$SkipTests
    & "$PSScriptRoot/build-desktop.ps1" -Python $Python
    Write-Output 'Both products built. Run acceptance before package-control.py --with-desktop and verify-release.py.'
} finally { Pop-Location }
