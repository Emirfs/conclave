@echo off
setlocal
rem Gelistirme modu: arayuzu canli yeniden yukleme ile calistirir.
rem
rem Bu modda frontend degisikliklerini (tsx, css) kaydettigin an pencerede
rem gorursun — her degisiklik icin `wails build` almana gerek kalmaz.
rem Go tarafini degistirirsen wails kendini yeniden derler.
rem
rem Pencereyi kapatmak yetmez: bu pencereyi Ctrl+C ile durdur.

set "ROOT=%~dp0.."
set "DAEMON=%ROOT%\build\conclave.exe"

if not exist "%DAEMON%" (
  echo Daemon bulunamadi: %DAEMON%
  echo Once derle:  go build -o build\conclave.exe .\cmd\conclave
  exit /b 1
)

tasklist /FI "IMAGENAME eq conclave.exe" 2>nul | find /I "conclave.exe" >nul
if errorlevel 1 (
  echo Daemon baslatiliyor...
  start "conclave-daemon" /B "%DAEMON%" daemon
  ping -n 2 127.0.0.1 >nul
) else (
  echo Daemon zaten calisiyor.
)

cd /d "%ROOT%\cmd\conclave-desktop"
echo Gelistirme modu aciliyor (durdurmak icin Ctrl+C)...
wails dev
endlocal
