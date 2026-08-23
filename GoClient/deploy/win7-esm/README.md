# Win7 ESM deployment package

This package installs the Go implementation of AgentClient on Windows 7. The
`386` binary built with Go 1.20.14 runs on both 32-bit and 64-bit Windows 7.

## Package layout

```text
agent/
  AgentClient.exe
  appsettings.json
  rustdesk.exe
install.bat
uninstall.bat
version.txt
```

The installer copies the payload to `C:\ProgramData\JikeAgent`, creates the
automatic `JikeAgent` Windows service, configures restart-on-failure, starts the
service, and writes ESM detection values to the non-redirected service key:

```text
HKLM\SYSTEM\CurrentControlSet\Services\JikeAgent
```

## ESM fields

- Install command: `cmd.exe /c install.bat`
- Uninstall command: `cmd.exe /c uninstall.bat`
- Installed registry key: `HKLM\SYSTEM\CurrentControlSet\Services\JikeAgent`
- Version registry value: `PackageVersion`
- Service check command: `sc.exe query JikeAgent`

Run `cmd.exe /c install.bat --validate` to validate the payload and version
without installing or changing the Windows service.

The ESM execution account must be LocalSystem or a local administrator. The
scripts are unattended and return a nonzero exit code when installation,
service startup, or removal fails.

Before packaging, set the intended `ServerUrl`, `Group`, and `CheckUpdate` in
`appsettings.json`. Do not include previous logs in the payload.

For the ESM remote-execution dialog, use `build_sfx.ps1` to create a compact
self-extracting EXE. The SFX payload intentionally excludes `rustdesk.exe` so
that it remains below the 4 MB remote-execution limit. Remote desktop automatic
installation is unavailable in this variant unless RustDesk is already
installed on the endpoint.
