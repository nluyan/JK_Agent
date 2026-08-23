param(
    [string]$PayloadDirectory = (Join-Path $PSScriptRoot "..\..\..\publish\win7-esm-lite"),
    [string]$OutputPath = (Join-Path $PSScriptRoot "..\..\..\publish\JikeAgent-Go-Win7-x86-2.17-ESM-Lite.exe")
)

$ErrorActionPreference = "Stop"

$payload = [IO.Path]::GetFullPath($PayloadDirectory)
$output = [IO.Path]::GetFullPath($OutputPath)
$iexpress32 = Join-Path $env:SystemRoot "SysWOW64\iexpress.exe"
$iexpress = if (Test-Path -LiteralPath $iexpress32) {
    $iexpress32
} else {
    Join-Path $env:SystemRoot "System32\iexpress.exe"
}

if (-not (Test-Path -LiteralPath $iexpress)) {
    throw "IExpress was not found: $iexpress"
}

$requiredFiles = @(
    (Join-Path $payload "agent\AgentClient.exe"),
    (Join-Path $payload "agent\appsettings.json"),
    (Join-Path $payload "install.bat"),
    (Join-Path $payload "version.txt"),
    (Join-Path $PSScriptRoot "install-sfx.bat")
)

foreach ($file in $requiredFiles) {
    if (-not (Test-Path -LiteralPath $file)) {
        throw "Missing SFX input: $file"
    }
}

$workingDirectory = Join-Path ([IO.Path]::GetTempPath()) ("JikeAgent-SFX-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $workingDirectory -Force | Out-Null

try {
    Copy-Item -LiteralPath (Join-Path $payload "agent\AgentClient.exe") -Destination $workingDirectory
    Copy-Item -LiteralPath (Join-Path $payload "agent\appsettings.json") -Destination $workingDirectory
    Copy-Item -LiteralPath (Join-Path $payload "install.bat") -Destination $workingDirectory
    Copy-Item -LiteralPath (Join-Path $payload "version.txt") -Destination $workingDirectory
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "install-sfx.bat") -Destination $workingDirectory

    $sedPath = Join-Path $workingDirectory "package.sed"
    $sourcePath = $workingDirectory.TrimEnd("\") + "\"
    $sed = @"
[Version]
Class=IEXPRESS
SEDVersion=3

[Options]
PackagePurpose=InstallApp
ShowInstallProgramWindow=0
HideExtractAnimation=1
UseLongFileName=1
InsideCompressed=0
CAB_FixedSize=0
CAB_ResvCodeSigning=0
RebootMode=N
InstallPrompt=
DisplayLicense=
FinishMessage=
TargetName=%TargetName%
FriendlyName=%FriendlyName%
AppLaunched=%AppLaunched%
PostInstallCmd=<None>
AdminQuietInstCmd=%AppLaunched%
UserQuietInstCmd=%AppLaunched%
SourceFiles=SourceFiles

[Strings]
TargetName="$output"
FriendlyName="JikeAgent Win7 ESM Installer"
AppLaunched="cmd.exe /d /c install-sfx.bat"
FILE0="AgentClient.exe"
FILE1="appsettings.json"
FILE2="install.bat"
FILE3="install-sfx.bat"
FILE4="version.txt"

[SourceFiles]
SourceFiles0=$sourcePath

[SourceFiles0]
%FILE0%=
%FILE1%=
%FILE2%=
%FILE3%=
%FILE4%=
"@

    Set-Content -LiteralPath $sedPath -Value $sed -Encoding Ascii
    $process = Start-Process -FilePath $iexpress -ArgumentList @("/N", "/Q", $sedPath) -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "IExpress failed with exit code $($process.ExitCode)"
    }

    if (-not (Test-Path -LiteralPath $output)) {
        throw "IExpress did not create the expected output: $output"
    }

    Get-Item -LiteralPath $output
}
finally {
    if (Test-Path -LiteralPath $workingDirectory) {
        Remove-Item -LiteralPath $workingDirectory -Recurse -Force
    }
}
