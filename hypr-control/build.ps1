# hypr-control 构建脚本：build + vet + test
$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
Set-Location $root

Write-Host '==> go build' -ForegroundColor Cyan
go build -o hctrl.exe ./cmd/hctrl
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host '==> go vet' -ForegroundColor Cyan
# -unsafeptr=false：win32 封装需把系统调用返回的内存句柄（uintptr）转为指针
# 读写（GlobalLock/剪贴板），属预期用法，非指针算术误用。
go vet -unsafeptr=false ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host '==> go test' -ForegroundColor Cyan
go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "`n构建完成：$root\hctrl.exe" -ForegroundColor Green
