@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0mail-lite\scripts\deploy.ps1" -Action Deploy %*
set "DEPLOY_EXIT=%ERRORLEVEL%"
pause
exit /b %DEPLOY_EXIT%
