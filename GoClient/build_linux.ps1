# Build Script - JikeAgent GoClient (Linux Only)
# Builds Linux 64-bit binary using Go cross-compilation

Write-Host "====================================" -ForegroundColor Cyan
Write-Host "  JikeAgent GoClient - Linux Build" -ForegroundColor Cyan
Write-Host "====================================" -ForegroundColor Cyan
Write-Host ""

# Read version number
$version = "1.0.0"
if (Test-Path "config/settings.go") {
    $content = Get-Content "config/settings.go" -Raw
    if ($content -match 'Version\s*=\s*"([^"]+)"') {
        $version = $matches[1]
    }
}
Write-Host "Version: $version" -ForegroundColor Green

# Show Go version
$goVersion = go version
Write-Host "Go Version: $goVersion" -ForegroundColor Yellow

# Set build parameters for Linux
$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"

# Update dependencies
Write-Host "`nUpdating dependencies..." -ForegroundColor Yellow
go mod tidy
if ($LASTEXITCODE -ne 0) {
    Write-Host "  ✗ Failed to update dependencies" -ForegroundColor Red
    exit 1
}

# Clean linux output directory
Write-Host "`nCleaning linux output directory..." -ForegroundColor Yellow
if (Test-Path "output/linux") {
    Remove-Item -Path "output/linux" -Recurse -Force
}
New-Item -ItemType Directory -Path "output/linux" -Force | Out-Null

# Build Linux 64-bit
Write-Host "`n[1/1] Building Linux 64-bit..." -ForegroundColor Yellow
go build -ldflags="-s -w" -o output/linux/AgentClient .
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Linux 64-bit build successful" -ForegroundColor Green
} else {
    Write-Host "  ✗ Linux 64-bit build failed" -ForegroundColor Red
    exit 1
}

# Build summary
Write-Host ""
Write-Host "====================================" -ForegroundColor Green
Write-Host "  Linux Build Complete!" -ForegroundColor Green
Write-Host "====================================" -ForegroundColor Green
Write-Host ""
Write-Host "Output Directory:" -ForegroundColor Cyan
Write-Host "  - output/linux/AgentClient (Linux 64-bit)" -ForegroundColor White
Write-Host ""

# Display file sizes
Write-Host "File Sizes:" -ForegroundColor Cyan
Get-ChildItem -Path "output/linux" -Recurse -File | ForEach-Object {
    $sizeMB = [math]::Round($_.Length / 1MB, 2)
    Write-Host "  - $($_.FullName.Replace((Get-Location).Path + '\\', '')): ${sizeMB} MB" -ForegroundColor White
}

Write-Host ""
Write-Host "Build Time: $timestamp" -ForegroundColor Gray
Write-Host "Version: $version" -ForegroundColor Gray
