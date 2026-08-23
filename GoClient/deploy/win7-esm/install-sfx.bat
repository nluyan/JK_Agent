@echo off
setlocal EnableExtensions

set "WORK_DIR=%TEMP%\JikeAgentSfx-%RANDOM%-%RANDOM%"
mkdir "%WORK_DIR%\agent" >nul 2>&1
if errorlevel 1 exit /b 50

copy /y "%~dp0AgentClient.exe" "%WORK_DIR%\agent\AgentClient.exe" >nul
if errorlevel 1 exit /b 51

copy /y "%~dp0appsettings.json" "%WORK_DIR%\agent\appsettings.json" >nul
if errorlevel 1 exit /b 52

copy /y "%~dp0install.bat" "%WORK_DIR%\install.bat" >nul
if errorlevel 1 exit /b 53

copy /y "%~dp0version.txt" "%WORK_DIR%\version.txt" >nul
if errorlevel 1 exit /b 54

call "%WORK_DIR%\install.bat"
set "INSTALL_EXIT=%ERRORLEVEL%"

rmdir /s /q "%WORK_DIR%" >nul 2>&1
exit /b %INSTALL_EXIT%
