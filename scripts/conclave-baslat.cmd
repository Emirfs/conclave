@echo off
setlocal
rem Conclave'i baslatir: daemon calismiyorsa arka planda ayaga kaldirir,
rem sonra masaustu penceresini acar. Pencere kapansa da daemon calismaya
rem devam eder; devam eden isler kesilmez.
rem
rem Hem kurulu bir Conclave'de (iki exe yan yana) hem gelistirme agacinda
rem calisir: once yanindaki dosyalara bakar, bulamazsa repo duzenini dener.

set "HERE=%~dp0"
set "ROOT=%~dp0.."

set "DAEMON=%HERE%conclave.exe"
set "DESKTOP=%HERE%conclave-desktop.exe"
if not exist "%DAEMON%"  set "DAEMON=%ROOT%\build\conclave.exe"
if not exist "%DESKTOP%" set "DESKTOP=%ROOT%\cmd\conclave-desktop\build\bin\conclave-desktop.exe"

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
