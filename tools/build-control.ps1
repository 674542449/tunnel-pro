param([switch]$SkipTests, [ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+$')][string]$Version = '', [string]$Python = 'python')
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
if (-not $Version) { $Version = (Get-Content -LiteralPath VERSION -Raw).Trim() }
& $Python tools/versioning.py --expect $Version
if ($LASTEXITCODE -ne 0) { throw 'Console and desktop versions must match VERSION before building.' }
if (-not $SkipTests) {
    go test -tags=http2legacy -race ./internal/control ./internal/nodeagent ./internal/integration -count=1 -timeout 180s
    if ($LASTEXITCODE -ne 0) { throw 'Management regression tests failed' }
    go vet -tags=http2legacy ./...
    if ($LASTEXITCODE -ne 0) { throw 'Vet failed' }
}
$previousOS = $env:GOOS
$previousArch = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
try {
    $env:CGO_ENABLED = '0'
    foreach ($target in @(@('windows', 'amd64'), @('linux', 'arm64'), @('linux', 'amd64'))) {
        $env:GOOS = $target[0]
        $env:GOARCH = $target[1]
        $outputDir = "dist/control-$Version/$($target[0])-$($target[1])"
        New-Item -ItemType Directory -Force $outputDir | Out-Null
        $suffix = if ($target[0] -eq 'windows') { '.exe' } else { '' }
        go build -tags=http2legacy -trimpath -ldflags='-s -w' -o "$outputDir/tunnelx-control$suffix" ./cmd/tunnelx-control
        if ($LASTEXITCODE -ne 0) { throw "Controller build failed: $outputDir" }
        foreach ($nodeArch in @('linux-arm64','linux-amd64')) {
            $artifactDir = "$outputDir/node-artifacts/$nodeArch"
            New-Item -ItemType Directory -Force $artifactDir | Out-Null
            foreach ($name in @('tunnelx-server','tunnelx-admin')) {
                $env:GOOS='linux';$env:GOARCH=$nodeArch.Substring(6)
                go build -tags=http2legacy -trimpath -ldflags='-s -w' -o "$artifactDir/$name" "./cmd/$name"
                if ($LASTEXITCODE -ne 0) {throw 'Managed node artifact build failed'}
            }
        }
    }
} finally {
    $env:GOOS = $previousOS
    $env:GOARCH = $previousArch
    $env:CGO_ENABLED = $previousCGO
}
Write-Output "Management binaries built in dist/control-$Version."
