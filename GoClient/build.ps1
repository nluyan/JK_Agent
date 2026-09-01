# Build Script - JikeAgent GoClient
# Supports Windows (32/64-bit), Linux, and Updater

Write-Host "====================================" -ForegroundColor Cyan
Write-Host "  JikeAgent GoClient Build Script" -ForegroundColor Cyan
Write-Host "====================================" -ForegroundColor Cyan
Write-Host ""

# Read version number
$version = "1.0.0"
if (Test-Path "config/settings.go") {
    $content = Get-Content "config/settings.go" -Raw
    if ($content -match 'Version\s*[:=]\s*"([^"]+)"') {
        $version = $matches[1]
    }
}
Write-Host "Version: $version" -ForegroundColor Green

# Set build parameters
$env:CGO_ENABLED = "0"
$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"

# Clean old output directory
Write-Host "`nCleaning output directory..." -ForegroundColor Yellow
if (Test-Path "output") {
    Remove-Item -Path "output" -Recurse -Force
}
New-Item -ItemType Directory -Path "output" -Force | Out-Null

# ========== Build Main Program ==========
Write-Host "`n====================================" -ForegroundColor Cyan
Write-Host "  Building Main Program (AgentClient)" -ForegroundColor Cyan
Write-Host "====================================" -ForegroundColor Cyan

# Windows 64-bit
Write-Host "`n[1/4] Building Windows 64-bit..." -ForegroundColor Yellow
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
New-Item -ItemType Directory -Path "output/win64" -Force | Out-Null
go build -ldflags="-s -w -extldflags '-static'" -o output/win64/AgentClient.exe .
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Windows 64-bit build successful" -ForegroundColor Green
} else {
    Write-Host "  ✗ Windows 64-bit build failed" -ForegroundColor Red
    exit 1
}

# Windows 32-bit
Write-Host "`n[2/4] Building Windows 32-bit..." -ForegroundColor Yellow
$env:GOOS = "windows"
$env:GOARCH = "386"
$env:CGO_ENABLED = "0"
New-Item -ItemType Directory -Path "output/win32" -Force | Out-Null
go build -ldflags="-s -w -extldflags '-static'" -o output/win32/AgentClient.exe .
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Windows 32-bit build successful" -ForegroundColor Green
} else {
    Write-Host "  ✗ Windows 32-bit build failed" -ForegroundColor Red
    exit 1
}

# Linux 64-bit
Write-Host "`n[3/4] Building Linux 64-bit..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "amd64"
New-Item -ItemType Directory -Path "output/linux" -Force | Out-Null
go build $buildFlags -o output/linux/AgentClient .
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Linux 64-bit build successful" -ForegroundColor Green
} else {
    Write-Host "  ✗ Linux 64-bit build failed" -ForegroundColor Red
    exit 1
}

# ========== Build Updater ==========
Write-Host "`n====================================" -ForegroundColor Cyan
Write-Host "  Building Updater (Windows Only)" -ForegroundColor Cyan
Write-Host "====================================" -ForegroundColor Cyan

# Windows 64-bit Updater
Write-Host "`n[4/4] Building Updater (Windows 64-bit)..." -ForegroundColor Yellow
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
New-Item -ItemType Directory -Path "output/updater" -Force | Out-Null
Set-Location -Path "updater"
go build -ldflags="-s -w -extldflags '-static'" -o ../output/updater/Updater.exe .
Set-Location -Path ".."
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Updater build successful" -ForegroundColor Green
} else {
    Write-Host "  ✗ Updater build failed" -ForegroundColor Red
    exit 1
}

# ========== Build Complete ==========
Write-Host ""
Write-Host "====================================" -ForegroundColor Green
Write-Host "  Build Complete!" -ForegroundColor Green
Write-Host "====================================" -ForegroundColor Green
Write-Host ""
Write-Host "Output Directory:" -ForegroundColor Cyan
Write-Host "  - output/win64/AgentClient.exe (Windows 64-bit)" -ForegroundColor White
Write-Host "  - output/win32/AgentClient.exe (Windows 32-bit)" -ForegroundColor White
Write-Host "  - output/linux/AgentClient     (Linux 64-bit)" -ForegroundColor White
Write-Host "  - output/updater/Updater.exe   (Windows Updater)" -ForegroundColor White
Write-Host ""

# Display file sizes
Write-Host "File Sizes:" -ForegroundColor Cyan
Get-ChildItem -Path "output" -Recurse -File | ForEach-Object {
    $sizeMB = [math]::Round($_.Length / 1MB, 2)
    Write-Host "  - $($_.FullName.Replace((Get-Location).Path + '\', '')): ${sizeMB} MB" -ForegroundColor White
}

Write-Host ""
Write-Host "Build Time: $timestamp" -ForegroundColor Gray
Write-Host "Version: $version" -ForegroundColor Gray