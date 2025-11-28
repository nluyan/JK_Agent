# 更新器构建脚本

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

if ($LASTEXITCODE -eq 0) {
    Write-Host "Updater.exe 构建成功!" -ForegroundColor Green
    
    # 移动到父目录
    Move-Item -Path "Updater.exe" -Destination ".." -Force
    Write-Host "Updater.exe 已移动到主目录" -ForegroundColor Green
} else {
    Write-Host "构建失败!" -ForegroundColor Red
    Set-Location -Path ".."
    exit 1
}

# 返回上级目录
Set-Location -Path ".."

Write-Host "构建完成!" -ForegroundColor Green
