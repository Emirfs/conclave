<#
.SYNOPSIS
    Conclave'i kurar veya gunceller.

.DESCRIPTION
    En son yayinlanan surumu GitHub'dan indirir, SHA256 ile dogrular ve
    %LOCALAPPDATA%\Programs\Conclave altina acar. Ayni betik guncelleme icin de
    calisir: uygulama acikken "Guncelle" dendiginde arayuz bu betigi cagirir.

    Tek satirlik kurulum:
        irm https://raw.githubusercontent.com/Emirfs/conclave/main/install.ps1 | iex

    Parametre vermek gerekiyorsa:
        & ([scriptblock]::Create((irm https://raw.githubusercontent.com/Emirfs/conclave/main/install.ps1))) -Version v0.2.0

.PARAMETER Version
    Kurulacak surum etiketi (ornek: v0.2.0). Verilmezse en son yayin kurulur.

.PARAMETER InstallDir
    Kurulum dizini. Varsayilan: %LOCALAPPDATA%\Programs\Conclave

.PARAMETER WaitForPid
    Dosyalari degistirmeden once bu surecin cikmasini bekler. Calisan uygulama
    kendi exe'sini degistiremez; tek tikla guncellemede arayuz kendi PID'ini
    buraya gecer.

.PARAMETER Restart
    Kurulumdan sonra Conclave'i acar.

.PARAMETER NoShortcuts
    Masaustu ve baslat menusu kisayollarini olusturmaz.

.PARAMETER NoPath
    Kurulum dizinini kullanici PATH'ine eklemez.
#>
[CmdletBinding()]
param(
    [string]$Version,
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\Conclave'),
    [int]$WaitForPid = 0,
    [switch]$Restart,
    [switch]$NoShortcuts,
    [switch]$NoPath
)

$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repository = 'Emirfs/conclave'
$AssetName = 'conclave-windows-amd64.zip'

function Write-Step([string]$Message) { Write-Host "  $Message" }

# 64 bit disinda indirilecek bir yapi yok; sessizce yanlis exe kurmaktansa
# durup soylemek dogru.
if (-not [Environment]::Is64BitOperatingSystem) {
    throw 'Conclave 64 bit Windows gerektiriyor.'
}

Write-Host ''
Write-Host 'Conclave' -ForegroundColor Cyan

# --- Yayin bilgisi -----------------------------------------------------------

if ($Version) {
    $releaseUrl = "https://api.github.com/repos/$Repository/releases/tags/$Version"
} else {
    $releaseUrl = "https://api.github.com/repos/$Repository/releases/latest"
}

try {
    $release = Invoke-RestMethod -Uri $releaseUrl -Headers @{
        'Accept'     = 'application/vnd.github+json'
        'User-Agent' = 'conclave-installer'
    }
} catch {
    $statusCode = $null
    try { $statusCode = [int]$_.Exception.Response.StatusCode } catch {}
    if ($statusCode -eq 404) {
        # Bir depoda yayin olmamasi bir ariza degil; kurulacak sey yok, o kadar.
        if ($Version) { throw "$Version diye bir yayin yok." }
        throw 'Conclave''in henuz yayinlanmis bir surumu yok. Kaynaktan derlemek icin README''ye bakin.'
    }
    throw "Yayin bilgisi alinamadi ($releaseUrl): $($_.Exception.Message)"
}

$tag = $release.tag_name
if (-not $tag) { throw 'Yayinin etiketi yok.' }

$asset = $release.assets | Where-Object { $_.name -eq $AssetName } | Select-Object -First 1
if (-not $asset) { throw "$tag yayininda $AssetName yok." }
$checksums = $release.assets | Where-Object { $_.name -eq 'checksums.txt' } | Select-Object -First 1

# Zaten kurulu olan bu surumse indirmeye gerek yok. Guncelleme akisi -Restart
# ile gelir ve orada yeniden kurmak zararsiz.
$installedExe = Join-Path $InstallDir 'conclave.exe'
if ((Test-Path $installedExe) -and (-not $Restart)) {
    $installed = $null
    try { $installed = (& $installedExe version 2>$null | Select-Object -First 1) } catch {}
    if ($installed -and ($installed -eq $tag -or "v$installed" -eq $tag)) {
        Write-Step "$tag zaten kurulu."
        return
    }
}

Write-Step "$tag indiriliyor..."

# --- Indirme ve dogrulama ----------------------------------------------------

$workDir = Join-Path ([IO.Path]::GetTempPath()) ('conclave-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $workDir -Force | Out-Null
try {
    $archive = Join-Path $workDir $AssetName
    Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $archive -UseBasicParsing

    if ($checksums) {
        $manifest = (Invoke-WebRequest -Uri $checksums.browser_download_url -UseBasicParsing).Content
        $expected = $null
        foreach ($line in ($manifest -split "`n")) {
            $parts = ($line.Trim() -split '\s+')
            if ($parts.Count -ge 2 -and $parts[-1].TrimStart('*') -eq $AssetName) { $expected = $parts[0] }
        }
        if (-not $expected) { throw "checksums.txt icinde $AssetName yok." }
        $actual = (Get-FileHash -Path $archive -Algorithm SHA256).Hash
        if ($actual -ne $expected.ToUpperInvariant()) {
            throw "Indirilen dosyanin SHA256'si tutmuyor. Beklenen $expected, gelen $actual."
        }
        Write-Step 'SHA256 dogrulandi.'
    } else {
        # Dogrulanmamis bir kurulum sessizce gecmemeli: kullanici ne aldigini bilsin.
        Write-Warning 'Yayinda checksums.txt yok; indirilen dosya dogrulanamadi.'
    }

    $staging = Join-Path $workDir 'unpacked'
    Expand-Archive -Path $archive -DestinationPath $staging -Force

    # --- Calisan surecleri kapat --------------------------------------------

    if ($WaitForPid -gt 0) {
        Write-Step "Uygulamanin kapanmasi bekleniyor (pid $WaitForPid)..."
        try { Wait-Process -Id $WaitForPid -Timeout 30 -ErrorAction Stop } catch {}
    }

    $running = @()
    foreach ($name in @('conclave-desktop', 'conclave')) {
        $running += Get-Process -Name $name -ErrorAction SilentlyContinue
    }
    if ($running.Count -gt 0) {
        Write-Step 'Calisan Conclave kapatiliyor...'
        # Once nazikce: daemon yarim kalan isi SQLite'a yazar, bir sonraki
        # baslatista kaldigi yerden kuyruga alinir.
        foreach ($process in $running) { try { $process.CloseMainWindow() | Out-Null } catch {} }
        Start-Sleep -Milliseconds 1500
        foreach ($process in $running) {
            if (-not $process.HasExited) {
                try { Stop-Process -Id $process.Id -Force -ErrorAction Stop } catch {}
            }
        }
        # Dosya kilitlerinin birakilmasi bir an aliyor.
        Start-Sleep -Milliseconds 750
    }

    # --- Kopyala -------------------------------------------------------------

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Write-Step "Kuruluyor: $InstallDir"
    Copy-Item -Path (Join-Path $staging '*') -Destination $InstallDir -Recurse -Force
} finally {
    Remove-Item -Path $workDir -Recurse -Force -ErrorAction SilentlyContinue
}

# --- Kisayollar --------------------------------------------------------------

if (-not $NoShortcuts) {
    $shell = New-Object -ComObject WScript.Shell
    $startMenu = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
    $targets = @(
        @{ Name = 'Conclave'; Exe = 'conclave-desktop.exe'; Desc = 'Conclave arayuzunu acar' },
        @{ Name = 'Conclave - Kapat'; Exe = 'conclave-durdur.cmd'; Desc = "Arayuzu ve daemon'u kapatir" }
    )
    foreach ($location in @([Environment]::GetFolderPath('Desktop'), $startMenu)) {
        foreach ($target in $targets) {
            $link = $shell.CreateShortcut((Join-Path $location ($target.Name + '.lnk')))
            $link.TargetPath = Join-Path $InstallDir $target.Exe
            $link.WorkingDirectory = $InstallDir
            $link.IconLocation = Join-Path $InstallDir 'conclave-desktop.exe'
            $link.Description = $target.Desc
            # Bir .cmd kisayolu konsol penceresi acar; kullanici onu gormek zorunda degil.
            if ($target.Exe.EndsWith('.cmd')) { $link.WindowStyle = 7 }
            $link.Save()
        }
    }
    Write-Step 'Kisayollar olusturuldu.'
}

# --- PATH --------------------------------------------------------------------

if (-not $NoPath) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ })
    if ($entries -notcontains $InstallDir) {
        [Environment]::SetEnvironmentVariable('Path', ((@($entries) + $InstallDir) -join ';'), 'User')
        Write-Step "PATH'e eklendi (yeni terminallerde gecerli)."
    }
}

Write-Host ''
Write-Host "Conclave $tag hazir." -ForegroundColor Green
Write-Host "  Kurulum: $InstallDir"
Write-Host '  Komut:   conclave status'

if ($Restart) {
    Start-Process -FilePath (Join-Path $InstallDir 'conclave-desktop.exe') -WorkingDirectory $InstallDir
}
