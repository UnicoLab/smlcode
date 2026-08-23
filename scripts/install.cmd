@echo off
REM SLMCode Windows CMD installer
REM
REM   curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.cmd -o install.cmd && install.cmd && del install.cmd
REM
REM Pin a version (install.ps1 reads this from the environment, which a child
REM powershell.exe inherits):
REM   set SLMCODE_VERSION=v0.17.0
REM   install.cmd
REM
REM This is a thin shim. Everything real — architecture detection, the
REM SHA256SUMS check, the PATH entry, install.json — lives in install.ps1.
setlocal
set "PS1_URL=https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1"
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm '%PS1_URL%' | iex"
if errorlevel 1 (
  echo.
  echo Install failed. See the PowerShell output above.
  echo To uninstall, or to pass options, save the script first:
  echo   powershell -NoProfile -Command "irm '%PS1_URL%' -OutFile install.ps1"
  echo   powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1 -Uninstall
  exit /b 1
)
echo.
echo Made with love by UnicoLab - https://unicolab.ai
endlocal
