# Build Script for RemoteAccess Portable (Windows)
param (
    [string]$Mode = "release"
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
    Write-Host "Gerando executavel de producao otimizado (sem janela CMD)..." -ForegroundColor Yellow
    go build -ldflags="-H=windowsgui -s -w" -o "RemoteAccess.exe" ./cmd/remoteaccess
} else {
    Write-Host "Gerando executavel de desenvolvimento..." -ForegroundColor Yellow
    go build -o "RemoteAccess.exe" ./cmd/remoteaccess
}

if ($LASTEXITCODE -eq 0) {
    $file = Get-Item "RemoteAccess.exe"
    $sizeMB = [math]::Round($file.Length / 1MB, 2)
    Write-Host "=============================================" -ForegroundColor Green
    Write-Host " [SUCESSO] Executavel gerado: $($file.FullName)" -ForegroundColor Green
    Write-Host " [TAMANHO] $sizeMB MB" -ForegroundColor Green
    Write-Host " [MODO] Janela Nativa GUI (Sem CMD)" -ForegroundColor Green
    Write-Host "=============================================" -ForegroundColor Green
} else {
    Write-Host "[ERRO] Falha na compilacao." -ForegroundColor Red
}
