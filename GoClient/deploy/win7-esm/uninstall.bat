@echo off
setlocal EnableExtensions

set "SERVICE_NAME=JikeAgent"
set "INSTALL_DIR=%ProgramData%\JikeAgent"
set "REG_KEY=HKLM\SOFTWARE\JikeAgent"

fltmc.exe >nul 2>&1
if errorlevel 1 (
    echo [ERROR] Administrator or LocalSystem privileges are required.
    exit /b 5
)

sc.exe query "%SERVICE_NAME%" >nul 2>&1
if not errorlevel 1 (
    sc.exe stop "%SERVICE_NAME%" >nul 2>&1
    call :wait_for_state STOPPED 30
    if errorlevel 1 (
        echo [ERROR] The JikeAgent service did not stop in time.
        exit /b 10
    )

    sc.exe delete "%SERVICE_NAME%" >nul
    if errorlevel 1 (
        echo [ERROR] Failed to delete the JikeAgent service.
        exit /b 20
    )
)

reg.exe delete "%REG_KEY%" /f >nul 2>&1
rmdir /s /q "%INSTALL_DIR%" >nul 2>&1
if exist "%INSTALL_DIR%" (
    echo [ERROR] Failed to remove "%INSTALL_DIR%".
    exit /b 30
)

echo [INFO] JikeAgent uninstalled successfully.
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
