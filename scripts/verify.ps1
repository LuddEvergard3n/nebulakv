param([switch]$Benchmarks)
$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        $portable = Join-Path (Get-Location) '..\.tools\go\bin'
        if (-not (Test-Path (Join-Path $portable 'go.exe'))) { throw 'Install Go 1.27+ and add it to PATH.' }
        $env:PATH = "$portable;$env:PATH"
        $env:GOCACHE = Join-Path (Get-Location) '..\.tools\gocache'
    }
    $format = gofmt -l cmd internal
    if ($LASTEXITCODE -ne 0 -or $format) { throw "Formatting failed: $format" }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Static analysis failed.' }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }
    go build -trimpath -o bin/nebulakv.exe ./cmd/nebulakv
    if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
    if ($Benchmarks) {
        go test ./internal/storage -run '^$' -bench . -benchmem -count 3
        if ($LASTEXITCODE -ne 0) { throw 'Benchmarks failed.' }
    }
} finally { Pop-Location }
