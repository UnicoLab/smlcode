@echo off
REM SLMCode Windows CMD installer
REM
REM   curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.cmd -o install.cmd && install.cmd && del install.cmd
REM
setlocal
set "PS1_URL=https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1"
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm '%PS1_URL%' | iex"
if errorlevel 1 exit /b 1
echo.
echo Made with love by UnicoLab — https://unicolab.ai
endlocal
