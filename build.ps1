# Build Script for RemoteAccess Portable (Windows)
param (
    [string]$Mode = "release",
    [switch]$Sign = $true,
    [switch]$InstallRoot = $false
)

$env:Path = "C:\Program Files\Go\bin;$env:USERPROFILE\go\bin;" + $env:Path

Write-Host "=============================================" -ForegroundColor Cyan
Write-Host " Compilando RemoteAccess AnyDesk Edition ($Mode) " -ForegroundColor Cyan
Write-Host "=============================================" -ForegroundColor Cyan

# Generate embedded Windows resources (Icon, Version, DPI Manifest)
if (Test-Path "$env:USERPROFILE\go\bin\go-winres.exe") {
    Write-Host "Embutindo icone de alta definicao e manifesto Windows..." -ForegroundColor DarkCyan
    & "$env:USERPROFILE\go\bin\go-winres.exe" make --in winres/winres.json --out ./cmd/remoteaccess/rsrc
}

if ($Mode -eq "release") {
    # -H=windowsgui removes CMD console window
    # -s -w removes debug info and symbol table for smaller binary
    # -trimpath strips absolute build paths to avoid heuristics detection
    Write-Host "Gerando executavel de producao otimizado (sem janela CMD, -trimpath)..." -ForegroundColor Yellow
    go build -trimpath -ldflags="-H=windowsgui -s -w" -o "RemoteAccess.exe" ./cmd/remoteaccess
} else {
    Write-Host "Gerando executavel de desenvolvimento..." -ForegroundColor Yellow
    go build -trimpath -o "RemoteAccess.exe" ./cmd/remoteaccess
}

if ($LASTEXITCODE -eq 0) {
    $file = Get-Item "RemoteAccess.exe"
    $sizeMB = [math]::Round($file.Length / 1MB, 2)
    Write-Host "=============================================" -ForegroundColor Green
    Write-Host " [SUCESSO] Executavel gerado: $($file.FullName)" -ForegroundColor Green
    Write-Host " [TAMANHO] $sizeMB MB" -ForegroundColor Green
    Write-Host " [MODO] Janela Nativa GUI (Sem CMD)" -ForegroundColor Green
    Write-Host "=============================================" -ForegroundColor Green

    if ($Sign) {
        if (Test-Path "scripts/sign.ps1") {
            if ($InstallRoot) {
                & ./scripts/sign.ps1 -FilePath "RemoteAccess.exe" -InstallRoot
            } else {
                & ./scripts/sign.ps1 -FilePath "RemoteAccess.exe"
            }
        }
    }
} else {
    Write-Host "[ERRO] Falha na compilacao." -ForegroundColor Red
}
