@echo off
setlocal
title Mail Lite - Start
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0mail-lite\scripts\service.ps1" -Action start %*
set "result=%errorlevel%"
if not "%result%"=="0" pause
exit /b %result%
