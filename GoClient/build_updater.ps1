# Updater Build Script

Write-Host "Starting Updater build..." -ForegroundColor Green

# Navigate to updater directory
Set-Location -Path "updater"

# Download dependencies
Write-Host "Downloading dependencies..." -ForegroundColor Yellow
go mod tidy

# Build Windows executable
Write-Host "Building Updater.exe..." -ForegroundColor Yellow
$env:GOOS = "windows"
$env:GOARCH = "386"
go build -ldflags="-s -w" -o "Updater.exe" .

if ($LASTEXITCODE -eq 0) {
    Write-Host "Updater.exe built successfully!" -ForegroundColor Green
    
    # Move to parent directory
    Move-Item -Path "Updater.exe" -Destination ".." -Force
    Write-Host "Updater.exe moved to main directory" -ForegroundColor Green
} else {
    Write-Host "Build failed!" -ForegroundColor Red
    Set-Location -Path ".."
    exit 1
}

# Return to parent directory
Set-Location -Path ".."

Write-Host "Build completed!" -ForegroundColor Green
