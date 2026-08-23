@echo off
setlocal EnableExtensions

set "SERVICE_NAME=JikeAgent"
set "DISPLAY_NAME=JikeAgent Service"
set "INSTALL_DIR=%ProgramData%\JikeAgent"
set "SOURCE_DIR=%~dp0agent"
set "VERSION_FILE=%~dp0version.txt"
set "REG_KEY=HKLM\SOFTWARE\JikeAgent"
set "SERVICE_REG_KEY=HKLM\SYSTEM\CurrentControlSet\Services\JikeAgent"

if not exist "%SOURCE_DIR%\AgentClient.exe" (
    echo [ERROR] Missing payload: agent\AgentClient.exe
    exit /b 2
)

if not exist "%SOURCE_DIR%\appsettings.json" (
    echo [ERROR] Missing payload: agent\appsettings.json
    exit /b 3
)

if not exist "%VERSION_FILE%" (
    echo [ERROR] Missing package version file: version.txt
    exit /b 4
)

set /p "PACKAGE_VERSION="<"%VERSION_FILE%"
if not defined PACKAGE_VERSION (
    echo [ERROR] Package version is empty.
    exit /b 4
)

"%SOURCE_DIR%\AgentClient.exe" --version | findstr /C:"%PACKAGE_VERSION%" >nul
if errorlevel 1 (
    echo [ERROR] AgentClient.exe does not match package version %PACKAGE_VERSION%.
    exit /b 4
)

if /I "%~1"=="--validate" (
    echo [INFO] JikeAgent %PACKAGE_VERSION% package validation succeeded.
    exit /b 0
)

fltmc.exe >nul 2>&1
if errorlevel 1 (
    echo [ERROR] Administrator or LocalSystem privileges are required.
    exit /b 5
)

echo [INFO] Installing JikeAgent %PACKAGE_VERSION% to "%INSTALL_DIR%"...

sc.exe query "%SERVICE_NAME%" >nul 2>&1
if not errorlevel 1 (
    sc.exe stop "%SERVICE_NAME%" >nul 2>&1
    call :wait_for_state STOPPED 30
    if errorlevel 1 (
        echo [ERROR] The existing JikeAgent service did not stop in time.
        exit /b 10
    )
)

if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"
if errorlevel 1 (
    echo [ERROR] Failed to create the installation directory.
    exit /b 20
)

robocopy "%SOURCE_DIR%" "%INSTALL_DIR%" /E /COPY:DAT /R:2 /W:2 /NFL /NDL /NJH /NJS /NP >nul
set "COPY_EXIT=%ERRORLEVEL%"
if %COPY_EXIT% GEQ 8 (
    echo [ERROR] Failed to copy the application payload. Robocopy exit code: %COPY_EXIT%
    exit /b 21
)

sc.exe query "%SERVICE_NAME%" >nul 2>&1
if errorlevel 1 (
    sc.exe create "%SERVICE_NAME%" binPath= "%INSTALL_DIR%\AgentClient.exe" start= auto DisplayName= "%DISPLAY_NAME%" >nul
) else (
    sc.exe config "%SERVICE_NAME%" binPath= "%INSTALL_DIR%\AgentClient.exe" start= auto DisplayName= "%DISPLAY_NAME%" >nul
)
if errorlevel 1 (
    echo [ERROR] Failed to create or configure the JikeAgent service.
    exit /b 30
)

sc.exe description "%SERVICE_NAME%" "JikeAgent remote management client" >nul 2>&1
sc.exe failure "%SERVICE_NAME%" reset= 86400 actions= restart/5000/restart/5000/restart/5000 >nul 2>&1

reg.exe add "%SERVICE_REG_KEY%" /v PackageVersion /t REG_SZ /d "%PACKAGE_VERSION%" /f >nul
if errorlevel 1 (
    echo [ERROR] Failed to write the service version registry value.
    exit /b 31
)
reg.exe add "%SERVICE_REG_KEY%" /v InstallPath /t REG_SZ /d "%INSTALL_DIR%" /f >nul

reg.exe add "%REG_KEY%" /v DisplayName /t REG_SZ /d "JikeAgent" /f >nul
if errorlevel 1 (
    echo [ERROR] Failed to write the installation detection registry key.
    exit /b 32
)
reg.exe add "%REG_KEY%" /v Version /t REG_SZ /d "%PACKAGE_VERSION%" /f >nul
reg.exe add "%REG_KEY%" /v InstallPath /t REG_SZ /d "%INSTALL_DIR%" /f >nul

sc.exe start "%SERVICE_NAME%" >nul 2>&1
call :wait_for_state RUNNING 30
if errorlevel 1 (
    echo [ERROR] JikeAgent was installed but the service did not start.
    exit /b 40
)

echo [INFO] JikeAgent %PACKAGE_VERSION% installed successfully.
exit /b 0

:wait_for_state
setlocal
set "EXPECTED_STATE=%~1"
set /a "WAIT_SECONDS=%~2"
set /a "WAITED=0"

:wait_loop
sc.exe query "%SERVICE_NAME%" 2>nul | findstr /I /C:"%EXPECTED_STATE%" >nul
if not errorlevel 1 (
    endlocal
    exit /b 0
)

if %WAITED% GEQ %WAIT_SECONDS% (
    endlocal
    exit /b 1
)

timeout.exe /t 1 /nobreak >nul 2>&1
set /a "WAITED+=1"
goto wait_loop
