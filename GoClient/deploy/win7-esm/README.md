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

## Building a Lite package for one server address

`build_lite.ps1` runs the whole flow in one call: it writes `appsettings.json`,
syncs `version.txt` with the client binary, validates the payload, builds the
self-extracting EXE through `build_sfx.ps1`, and then re-extracts the EXE to
verify its contents.

```powershell
cd GoClient/deploy/win7-esm
./build_lite.ps1 -ServerUrl http://172.21.1.41
./build_lite.ps1 -ServerUrl http://172.21.1.41 -Group win7 -CheckUpdate 60
./build_lite.ps1 -Rebuild -ServerUrl http://172.21.1.41
```

The output name embeds the client version and the server host, for example
`publish/JikeAgent-Go-Win7-x86-2.20-ESM-Lite-172.21.1.41.exe`, so packages for
different servers do not overwrite each other. A host with a port becomes
`zabbix.jikefw.com-17037`.

Parameters:

- `-ServerUrl`: address written into `appsettings.json`. When omitted, the
  existing payload configuration is reused.
- `-Group`, `-CheckUpdate`: the remaining `appsettings.json` fields, defaults
  `win7` and `60`.
- `-HubAuthToken`: token written into `appsettings.json`. It must match the
  server's `HubAuthToken` once hub authentication is turned on, otherwise the
  client is rejected with 401 and keeps retrying with backoff. Defaults to
  empty. The build summary reports only whether it is set, never the value.
- `-Rebuild`: compile the Win7 x86 client from `GoClient` with `go1.20.14`.
- `-ClientExe <path>`: package an already built binary instead of the one in
  the payload directory.
- `-PayloadDirectory`: payload folder, defaults to `publish/win7-esm-lite`.
- `-OutputPath`: override the generated file name.
- `-SkipVerify`: skip the post-build extraction check.

IExpress embeds a build timestamp, so rebuilding the same configuration yields
a different SHA256. Always distribute the newest file.
