@echo off
REM 作用：Windows 下双击或 cmd 调用 pack-windows.ps1
REM 运行主机：Windows
REM 调用方：人工
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0pack-windows.ps1" %*
exit /b %ERRORLEVEL%
