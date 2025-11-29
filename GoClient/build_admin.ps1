# JikeAdmin Build Script
# Build JikeAdmin.exe for Windows platform

param(
    [string]$OutputDir = "output/admin",
    [switch]$Release
)

Write-Host "Building JikeAdmin..." -ForegroundColor Green

# Set environment variables
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

# Ensure output directory exists
if (-not (Test-Path $OutputDir)) {
    New-Item -ItemType Directory -Path $OutputDir | Out-Null
}

# Switch to jikeadmin directory
Push-Location jikeadmin

try {
    # Build flags
    $buildFlags = @()
    
    if ($Release) {
        # Release mode: optimize and remove debug info
        $buildFlags += "-ldflags=-s -w -H windowsgui"
        Write-Host "Build mode: Release (GUI app, no console window)" -ForegroundColor Yellow
    } else {
        # Debug mode: keep console window
        Write-Host "Build mode: Debug (console app)" -ForegroundColor Yellow
    }
    
    # Output file path
    $outputPath = Join-Path (Join-Path ".." $OutputDir) "JikeAdmin.exe"
    
    # Execute build
    Write-Host "Compiling..." -ForegroundColor Cyan
    
    if ($Release) {
        go build -o $outputPath -ldflags="-s -w -H windowsgui" .
    } else {
        go build -o $outputPath .
    }
    
    if ($LASTEXITCODE -eq 0) {
        Write-Host "Build successful!" -ForegroundColor Green
        Write-Host "Output file: $outputPath" -ForegroundColor Cyan
        
        # Display file info
        $fileInfo = Get-Item $outputPath
        Write-Host "File size: $([Math]::Round($fileInfo.Length / 1MB, 2)) MB" -ForegroundColor Gray
    } else {
        Write-Host "Build failed!" -ForegroundColor Red
        exit 1
    }
} finally {
    Pop-Location
}

Write-Host "`nUsage:" -ForegroundColor Yellow
Write-Host "1. Run JikeAdmin.exe as Administrator to register jike:// protocol" -ForegroundColor White
Write-Host "2. After registration, use jike://remotedesk/<ID>/<password> to start remote desktop" -ForegroundColor White
Write-Host "3. Or use jike://filetransfer/<ID>/<password> to start file transfer" -ForegroundColor White
