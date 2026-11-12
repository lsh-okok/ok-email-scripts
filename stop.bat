@echo off
setlocal
title Mail Lite - Stop
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0mail-lite\scripts\service.ps1" -Action stop %*
set "result=%errorlevel%"
if not "%result%"=="0" pause
exit /b %result%
