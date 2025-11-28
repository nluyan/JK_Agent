# Build Script for Windows 7 Compatibility
# This script builds binaries compatible with Windows 7

Write-Host "====================================" -ForegroundColor Cyan
Write-Host "  Windows 7 Compatible Build" -ForegroundColor Cyan
Write-Host "====================================" -ForegroundColor Cyan
Write-Host ""

# Check Go version
$goVersion = go version
Write-Host "Go Version: $goVersion" -ForegroundColor Yellow

# Check if go1.20.14 is available
$go120Available = $false
try {
    $go120Version = go1.20.14 version 2>$null
    if ($LASTEXITCODE -eq 0) {
        $go120Available = $true
        Write-Host "Found Go 1.20.14 - will use it for Windows 7 compatibility" -ForegroundColor Green
        $goCmd = "go1.20.14"
    }
} catch {
    # go1.20.14 not found
}

if (-not $go120Available) {
    if ($goVersion -match "go1\.2[1-9]" -or $goVersion -match "go1\.[3-9]") {
        Write-Host "ERROR: Go 1.21+ is NOT compatible with Windows 7!" -ForegroundColor Red
        Write-Host "" -ForegroundColor Red
        Write-Host "Please install Go 1.20.14:" -ForegroundColor Yellow
        Write-Host "  1. Run: go install golang.org/dl/go1.20.14@latest" -ForegroundColor Cyan
        Write-Host "  2. Run: go1.20.14 download" -ForegroundColor Cyan
        Write-Host "  3. Run this script again" -ForegroundColor Cyan
        Write-Host ""
        exit 1
    }
    $goCmd = "go"
}

# Read version number
$version = "1.0.0"
if (Test-Path "config/settings.go") {
    $content = Get-Content "config/settings.go" -Raw
    if ($content -match 'Version\s*=\s*"([^"]+)"') {
        $version = $matches[1]
    }
}
Write-Host "Version: $version" -ForegroundColor Green

# Set build parameters for Windows 7
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
# Use Go 1.20 compatible settings
$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"

# Clean old output directory
Write-Host "`nCleaning output directory..." -ForegroundColor Yellow
if (Test-Path "output_win7") {
    Remove-Item -Path "output_win7" -Recurse -Force
}
New-Item -ItemType Directory -Path "output_win7" -Force | Out-Null

# Update dependencies
Write-Host "Updating dependencies..." -ForegroundColor Yellow
& $goCmd mod tidy
if ($LASTEXITCODE -ne 0) {
    Write-Host "Failed to update dependencies" -ForegroundColor Red
    exit 1
}

Write-Host "`n====================================" -ForegroundColor Cyan
Write-Host "  Building for Windows 7" -ForegroundColor Cyan
Write-Host "====================================" -ForegroundColor Cyan

# Windows 64-bit
Write-Host "`n[1/3] Building Windows 7 64-bit..." -ForegroundColor Yellow
$env:GOARCH = "amd64"
New-Item -ItemType Directory -Path "output_win7/win64" -Force | Out-Null
& $goCmd build -ldflags="-s -w" -o output_win7/win64/AgentClient.exe .
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Windows 7 64-bit build successful" -ForegroundColor Green
} else {
    Write-Host "  ✗ Windows 7 64-bit build failed" -ForegroundColor Red
    exit 1
}

# Windows 32-bit
Write-Host "`n[2/3] Building Windows 7 32-bit..." -ForegroundColor Yellow
$env:GOARCH = "386"
New-Item -ItemType Directory -Path "output_win7/win32" -Force | Out-Null
& $goCmd build -ldflags="-s -w" -o output_win7/win32/AgentClient.exe .
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Windows 7 32-bit build successful" -ForegroundColor Green
} else {
    Write-Host "  ✗ Windows 7 32-bit build failed" -ForegroundColor Red
    exit 1
}

# Updater
Write-Host "`n[3/3] Building Updater for Windows 7..." -ForegroundColor Yellow
$env:GOARCH = "amd64"
New-Item -ItemType Directory -Path "output_win7/updater" -Force | Out-Null
Set-Location -Path "updater"
& $goCmd build -ldflags="-s -w" -o ../output_win7/updater/Updater.exe .
Set-Location -Path ".."
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Updater build successful" -ForegroundColor Green
} else {
    Write-Host "  ✗ Updater build failed" -ForegroundColor Red
    exit 1
}

# Build Complete
Write-Host ""
Write-Host "====================================" -ForegroundColor Green
Write-Host "  Build Complete!" -ForegroundColor Green
Write-Host "====================================" -ForegroundColor Green
Write-Host ""
Write-Host "Output Directory: output_win7/" -ForegroundColor Cyan
Write-Host "  - output_win7/win64/AgentClient.exe (Windows 7+ 64-bit)" -ForegroundColor White
Write-Host "  - output_win7/win32/AgentClient.exe (Windows 7+ 32-bit)" -ForegroundColor White
Write-Host "  - output_win7/updater/Updater.exe   (Windows 7+ Updater)" -ForegroundColor White
Write-Host ""

# Display file sizes
Write-Host "File Sizes:" -ForegroundColor Cyan
Get-ChildItem -Path "output_win7" -Recurse -File | ForEach-Object {
    $sizeMB = [math]::Round($_.Length / 1MB, 2)
    Write-Host "  - $($_.FullName.Replace((Get-Location).Path + '\', '')): ${sizeMB} MB" -ForegroundColor White
}

Write-Host ""
Write-Host "Build Time: $timestamp" -ForegroundColor Gray
Write-Host "Version: $version" -ForegroundColor Gray
Write-Host ""
Write-Host "IMPORTANT: If you still see 'bcryptprimitives.dll' errors on Windows 7:" -ForegroundColor Yellow
Write-Host "  1. Downgrade to Go 1.20.x: https://go.dev/dl/" -ForegroundColor Yellow
Write-Host "  2. Rebuild using this script" -ForegroundColor Yellow
Write-Host "  3. Or install KB updates on Windows 7" -ForegroundColor Yellow
