param(
    [string]$ServerUrl = "",
    [string]$Group = "win7",
    [int]$CheckUpdate = 60,
    [string]$HubAuthToken = "",
    [string]$PayloadDirectory = (Join-Path $PSScriptRoot "..\..\..\publish\win7-esm-lite"),
    [string]$ClientExe = "",
    [string]$OutputPath = "",
    [switch]$Rebuild,
    [switch]$SkipVerify
)

$ErrorActionPreference = "Stop"

function Write-Step {
    param([string]$Message)
    Write-Host "[STEP] $Message" -ForegroundColor Cyan
}

function Get-HostSuffix {
    param([string]$Url)
    $value = $Url -replace "^[A-Za-z][A-Za-z0-9+.\-]*://", ""
    $value = ($value -split "/")[0]
    $value = $value -replace "[^A-Za-z0-9._\-]", "-"
    return $value.Trim(".")
}

$payload = [IO.Path]::GetFullPath($PayloadDirectory)
$publishRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\..\..\publish"))
$goClientRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$agentDirectory = Join-Path $payload "agent"
$clientPath = Join-Path $agentDirectory "AgentClient.exe"
$settingsPath = Join-Path $agentDirectory "appsettings.json"
$versionPath = Join-Path $payload "version.txt"
$installBat = Join-Path $payload "install.bat"

New-Item -ItemType Directory -Force -Path $agentDirectory | Out-Null

if ($Rebuild) {
    if (-not (Get-Command go1.20.14 -ErrorAction SilentlyContinue)) {
        throw "go1.20.14 is required for Windows 7 builds. Install it with: go install golang.org/dl/go1.20.14@latest; go1.20.14 download"
    }
    Write-Step "Building the Win7 x86 client with go1.20.14"
    $previousLocation = (Get-Location).Path
    try {
        Set-Location -LiteralPath $goClientRoot
        $env:CGO_ENABLED = "0"
        $env:GOOS = "windows"
        $env:GOARCH = "386"
        & go1.20.14 build -ldflags="-s -w" -o $clientPath .
        if ($LASTEXITCODE -ne 0) {
            throw "Go build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Set-Location -LiteralPath $previousLocation
        $env:CGO_ENABLED = ""
        $env:GOOS = ""
        $env:GOARCH = ""
    }
} elseif (-not [string]::IsNullOrWhiteSpace($ClientExe)) {
    Write-Step "Copying the client binary from $ClientExe"
    Copy-Item -LiteralPath ([IO.Path]::GetFullPath($ClientExe)) -Destination $clientPath -Force
}

if (-not (Test-Path -LiteralPath $clientPath)) {
    throw "Missing client binary: $clientPath (use -Rebuild or -ClientExe)"
}
if (-not (Test-Path -LiteralPath $installBat)) {
    throw "Missing installer script: $installBat"
}

Write-Step "Reading the client version"
$versionOutput = ((& $clientPath --version) -join " ")
if ($versionOutput -notmatch "(\d+\.\d+(?:\.\d+)?)") {
    throw "Unable to read the client version from: $versionOutput"
}
$version = $matches[1]
$existingVersion = ""
if (Test-Path -LiteralPath $versionPath) {
    $existingVersion = (Get-Content -LiteralPath $versionPath -Raw).Trim()
}
if ($existingVersion -ne $version) {
    Write-Host "  version.txt: '$existingVersion' -> '$version'"
    [IO.File]::WriteAllText($versionPath, $version)
}

if (-not [string]::IsNullOrWhiteSpace($ServerUrl)) {
    Write-Step "Writing appsettings.json"
    $settingsJson = "{`r`n  `"Group`": `"$Group`",`r`n  `"ServerUrl`": `"$ServerUrl`",`r`n  `"CheckUpdate`": $CheckUpdate,`r`n  `"HubAuthToken`": `"$HubAuthToken`"`r`n}"
    [IO.File]::WriteAllText($settingsPath, $settingsJson, (New-Object Text.UTF8Encoding($false)))
} else {
    if (-not (Test-Path -LiteralPath $settingsPath)) {
        throw "Missing appsettings.json and no -ServerUrl was supplied: $settingsPath"
    }
    $existingSettings = Get-Content -LiteralPath $settingsPath -Raw | ConvertFrom-Json
    $ServerUrl = [string]$existingSettings.ServerUrl
    $Group = [string]$existingSettings.Group
    $CheckUpdate = [int]$existingSettings.CheckUpdate
    $HubAuthToken = [string]$existingSettings.HubAuthToken
    Write-Step "Reusing the existing appsettings.json ($ServerUrl)"
}

Write-Step "Validating the payload"
& cmd.exe /c "`"$installBat`" --validate" 2>&1 | ForEach-Object { Write-Host "  $_" }
if ($LASTEXITCODE -ne 0) {
    throw "Payload validation failed with exit code $LASTEXITCODE"
}

if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $suffix = Get-HostSuffix -Url $ServerUrl
    $fileName = "JikeAgent-Go-Win7-x86-$version-ESM-Lite"
    if (-not [string]::IsNullOrWhiteSpace($suffix)) {
        $fileName = "$fileName-$suffix"
    }
    $OutputPath = Join-Path $publishRoot "$fileName.exe"
}
$output = [IO.Path]::GetFullPath($OutputPath)

Write-Step "Building the self-extracting package"
try {
    & (Join-Path $PSScriptRoot "build_sfx.ps1") -PayloadDirectory $payload -OutputPath $output | Out-Null
} catch {
    throw "build_sfx.ps1 failed: $($_.Exception.Message)"
}
if (-not (Test-Path -LiteralPath $output)) {
    throw "The SFX package was not created: $output"
}

if (-not $SkipVerify) {
    Write-Step "Verifying the package contents"
    $bytes = [IO.File]::ReadAllBytes($output)
    $rawText = [Text.Encoding]::GetEncoding(28591).GetString($bytes)
    $cabOffset = $rawText.IndexOf("MSCF")
    if ($cabOffset -lt 0) {
        throw "No embedded cabinet was found in $output"
    }
    $tempDirectory = Join-Path ([IO.Path]::GetTempPath()) ("JikeAgent-Lite-Verify-" + [guid]::NewGuid().ToString("N"))
    $extractDirectory = Join-Path $tempDirectory "files"
    New-Item -ItemType Directory -Force -Path $extractDirectory | Out-Null
    try {
        $cabPath = Join-Path $tempDirectory "embedded.cab"
        $cabBytes = New-Object "byte[]" ($bytes.Length - $cabOffset)
        [Array]::Copy($bytes, $cabOffset, $cabBytes, 0, $cabBytes.Length)
        [IO.File]::WriteAllBytes($cabPath, $cabBytes)
        & expand.exe -F:* $cabPath $extractDirectory *> $null
        if ($LASTEXITCODE -ne 0) {
            throw "expand.exe failed with exit code $LASTEXITCODE"
        }

        $requiredNames = @("AgentClient.exe", "appsettings.json", "install.bat", "install-sfx.bat", "version.txt")
        $extractedNames = @(Get-ChildItem -LiteralPath $extractDirectory -File | ForEach-Object { $_.Name })
        foreach ($name in $requiredNames) {
            if ($extractedNames -notcontains $name) {
                throw "The package is missing $name"
            }
        }
        if ($extractedNames -contains "rustdesk.exe") {
            throw "The Lite package must not contain rustdesk.exe"
        }

        $extractedSettings = Get-Content -LiteralPath (Join-Path $extractDirectory "appsettings.json") -Raw | ConvertFrom-Json
        if ([string]$extractedSettings.ServerUrl -ne $ServerUrl) {
            throw "ServerUrl mismatch in the package: $($extractedSettings.ServerUrl)"
        }
        if ([string]$extractedSettings.Group -ne $Group) {
            throw "Group mismatch in the package: $($extractedSettings.Group)"
        }
        if ([int]$extractedSettings.CheckUpdate -ne $CheckUpdate) {
            throw "CheckUpdate mismatch in the package: $($extractedSettings.CheckUpdate)"
        }
        if ([string]$extractedSettings.HubAuthToken -ne $HubAuthToken) {
            throw "HubAuthToken mismatch in the package"
        }

        $extractedVersion = (Get-Content -LiteralPath (Join-Path $extractDirectory "version.txt") -Raw).Trim()
        if ($extractedVersion -ne $version) {
            throw "version.txt mismatch in the package: $extractedVersion"
        }

        $extractedClient = Join-Path $extractDirectory "AgentClient.exe"
        $extractedClientHash = (Get-FileHash -LiteralPath $extractedClient -Algorithm SHA256).Hash
        $payloadClientHash = (Get-FileHash -LiteralPath $clientPath -Algorithm SHA256).Hash
        if ($extractedClientHash -ne $payloadClientHash) {
            throw "The packaged AgentClient.exe does not match the payload binary"
        }
        $extractedVersionOutput = ((& $extractedClient --version) -join " ")
        if ($extractedVersionOutput -notmatch [regex]::Escape($version)) {
            throw "The packaged client does not report version ${version}: $extractedVersionOutput"
        }

        Write-Host ("  files     : {0}" -f ($extractedNames -join ", "))
        Write-Host "  rustdesk  : excluded"
        Write-Host ("  client    : version $version, SHA256 $extractedClientHash")
    } finally {
        if (Test-Path -LiteralPath $tempDirectory) {
            Remove-Item -LiteralPath $tempDirectory -Recurse -Force
        }
    }
}

$item = Get-Item -LiteralPath $output
$sha256 = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash
Write-Host ""
Write-Host "====================================" -ForegroundColor Green
Write-Host "  Lite package ready" -ForegroundColor Green
Write-Host "====================================" -ForegroundColor Green
Write-Host ("  File       : {0}" -f $item.FullName)
Write-Host ("  Size       : {0:N0} bytes ({1} MB)" -f $item.Length, [math]::Round($item.Length / 1MB, 2))
Write-Host ("  SHA256     : {0}" -f $sha256)
Write-Host ("  Version    : {0}" -f $version)
Write-Host ("  ServerUrl  : {0}" -f $ServerUrl)
Write-Host ("  Group      : {0}" -f $Group)
Write-Host ("  CheckUpdate: {0}" -f $CheckUpdate)
Write-Host ("  HubAuth    : {0}" -f $(if ([string]::IsNullOrWhiteSpace($HubAuthToken)) { "disabled" } else { "enabled" }))
if ($item.Length -gt (4 * 1MB)) {
    Write-Host "  [WARN] The package exceeds the 4 MB ESM remote-execution limit." -ForegroundColor Yellow
}
Write-Host "  ESM install   : cmd.exe /c install.bat"
Write-Host "  ESM uninstall : cmd.exe /c uninstall.bat"
Write-Host "  ESM reg key   : HKLM\SYSTEM\CurrentControlSet\Services\JikeAgent (PackageVersion)"
Write-Host "  ESM svc check : sc.exe query JikeAgent"

$item
