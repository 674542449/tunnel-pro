param([switch]$SkipTests, [switch]$ClientOnly)
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
$env:GOPROXY = 'https://goproxy.cn'
python tools/patch-vendor.py
if ($LASTEXITCODE -ne 0) { throw 'Vendor validation failed' }
if (-not $SkipTests) {
    go test -tags=http2legacy ./... -count=1 -timeout 120s
    if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
    go vet -tags=http2legacy ./...
    if ($LASTEXITCODE -ne 0) { throw 'Vet failed' }
}
New-Item -ItemType Directory -Force dist/windows-amd64,dist/linux-amd64,dist/linux-arm64 | Out-Null
$oldOS = $env:GOOS; $oldArch = $env:GOARCH; $oldCGO = $env:CGO_ENABLED
try {
    $env:CGO_ENABLED='0'; $env:GOOS='windows'; $env:GOARCH='amd64'
    go build -tags=http2legacy -trimpath -ldflags='-s -w -H windowsgui' -o dist/windows-amd64/tunnelx-client.exe ./cmd/tunnelx-client
    if ($LASTEXITCODE -ne 0) { throw 'Windows client build failed' }
    foreach ($app in 'admin','check','control') {
        if (Test-Path "cmd/tunnelx-$app") {
            go build -tags=http2legacy -trimpath -ldflags='-s -w' -o "dist/windows-amd64/tunnelx-$app.exe" "./cmd/tunnelx-$app"
            if ($LASTEXITCODE -ne 0) { throw "Windows $app build failed" }
        }
    }
    if (-not $ClientOnly) {
    $env:GOOS='linux'
    foreach ($arch in 'amd64','arm64') {
        $env:GOARCH=$arch
        foreach ($app in 'server','fixture','check','admin','control') {
            go build -tags=http2legacy -trimpath -ldflags='-s -w' -o "dist/linux-$arch/tunnelx-$app" "./cmd/tunnelx-$app"
            if ($LASTEXITCODE -ne 0) { throw "Linux $arch $app build failed" }
        }
    }
    }
} finally { $env:GOOS=$oldOS; $env:GOARCH=$oldArch; $env:CGO_ENABLED=$oldCGO }
Get-ChildItem dist -Recurse -File | Where-Object { $_.Name -ne 'SHA256.json' -and $_.Extension -notin '.log','.jsonl' -and $_.FullName -notmatch '[\\/](logs|state)[\\/]' } | Get-FileHash -Algorithm SHA256 | Select-Object Path,Hash | ConvertTo-Json | Set-Content -Encoding utf8 dist/SHA256.json
Write-Output 'Build complete.'
