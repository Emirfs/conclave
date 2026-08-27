@echo off
setlocal
rem Conclave'i baslatir: daemon calismiyorsa arka planda ayaga kaldirir,
rem sonra masaustu penceresini acar. Pencere kapansa da daemon calismaya
rem devam eder; devam eden isler kesilmez.

set "ROOT=%~dp0.."
set "DAEMON=%ROOT%\build\conclave.exe"
set "DESKTOP=%ROOT%\cmd\conclave-desktop\build\bin\conclave-desktop.exe"

if not exist "%DAEMON%" (
  echo Daemon bulunamadi: %DAEMON%
  echo Once derle:  go build -o build\conclave.exe .\cmd\conclave
  exit /b 1
)
if not exist "%DESKTOP%" (
  echo Masaustu istemcisi bulunamadi: %DESKTOP%
  echo Once derle:  cd cmd\conclave-desktop ^&^& wails build
  exit /b 1
)

tasklist /FI "IMAGENAME eq conclave.exe" 2>nul | find /I "conclave.exe" >nul
if errorlevel 1 (
  echo Daemon baslatiliyor...
  start "conclave-daemon" /B "%DAEMON%" daemon
  rem Dinlemeye baslamasi icin kisa bir an.
  ping -n 2 127.0.0.1 >nul
) else (
  echo Daemon zaten calisiyor.
)

echo Arayuz aciliyor...
start "" "%DESKTOP%"
endlocal
