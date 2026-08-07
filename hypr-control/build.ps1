# hypr-control 构建脚本：build + vet + test
$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
Set-Location $root

Write-Host '==> go build' -ForegroundColor Cyan
go build -o hctrl.exe ./cmd/hctrl
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host '==> go vet' -ForegroundColor Cyan
go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host '==> go test' -ForegroundColor Cyan
go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "`n构建完成：$root\hctrl.exe" -ForegroundColor Green
