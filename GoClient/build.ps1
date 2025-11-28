$env:CGO_ENABLED = "0"

$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -o output/win64/AgentClient.exe

$env:GOOS = "windows"
$env:GOARCH = "386"
go build -o output/win32/AgentClient.exe

$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -o output/linux/AgentClient

Write-Host "开始构建 Updater..." -ForegroundColor Green

# 进入updater目录
Set-Location -Path "updater"

# 下载依赖
Write-Host "下载依赖..." -ForegroundColor Yellow
go mod tidy

# 构建Windows可执行文件
Write-Host "构建 Updater.exe..." -ForegroundColor Yellow
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -ldflags="-s -w" -o "Updater.exe" .