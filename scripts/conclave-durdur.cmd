@echo off
setlocal
rem Conclave'i tamamen kapatir: once arayuz, sonra daemon.
rem
rem Yalnizca pencereyi kapatmak daemon'u durdurmaz — bu bilerek boyle: kapali
rem arayuzle de isler devam eder. Bu betik "her seyi kapat" icindir.
rem Yalnizca arayuzu kapatmak istiyorsan pencereyi kapatman yeterli.

echo Arayuz kapatiliyor...
taskkill /F /IM conclave-desktop.exe >nul 2>&1
if errorlevel 1 (echo   zaten kapali) else (echo   kapatildi)

echo Daemon durduruluyor...
taskkill /F /IM conclave.exe >nul 2>&1
if errorlevel 1 (echo   zaten kapali) else (echo   durduruldu)

echo.
echo Devam eden isler SQLite'ta duruyor; bir dahaki baslatista kaldigi
echo yerden kuyruga alinir.
endlocal
