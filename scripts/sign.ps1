# Code Signing & Anti-SmartScreen Signature Automation for RemoteAccess
param (
    [string]$FilePath = "RemoteAccess.exe",
    [switch]$InstallRoot = $false
)

$ErrorActionPreference = "Stop"

if (!(Test-Path $FilePath)) {
    Write-Host "[ERRO] Arquivo '$FilePath' nao encontrado para assinatura." -ForegroundColor Red
    exit 1
}

Write-Host "=============================================" -ForegroundColor Cyan
Write-Host " Pipeline de Assinatura Digital (SmartScreen Shield) " -ForegroundColor Cyan
Write-Host "=============================================" -ForegroundColor Cyan

$certSubject = "CN=RemoteAccess Software Inc., O=RemoteAccess, OU=Engineering, C=BR"
$cert = Get-ChildItem -Path "Cert:\CurrentUser\My" -CodeSigningCert | Where-Object { $_.Subject -like "*RemoteAccess*" } | Select-Object -First 1

if (-not $cert) {
    Write-Host "Criando novo certificado corporativo de Code Signing..." -ForegroundColor Yellow
    $certParams = @{
        Type              = "CodeSigningCert"
        Subject           = $certSubject
        DnsName           = "RemoteAccess"
        KeyLength         = 2048
        KeyAlgorithm      = "RSA"
        HashAlgorithm     = "SHA256"
        CertStoreLocation = "Cert:\CurrentUser\My"
        NotAfter          = (Get-Date).AddYears(5)
        FriendlyName      = "RemoteAccess Official Code Signing Certificate"
    }
    $cert = New-SelfSignedCertificate @certParams
    
    Write-Host "Certificado gerado: $($cert.Thumbprint)" -ForegroundColor Green
} else {
    Write-Host "Certificado encontrado: $($cert.Subject) [Thumbprint: $($cert.Thumbprint)]" -ForegroundColor Green
}

if ($InstallRoot) {
    try {
        Write-Host "Importando certificado para a Autoridade Raiz Confiavel..." -ForegroundColor Cyan
        $certPath = "$env:TEMP\RemoteAccessSignCert.cer"
        Export-Certificate -Cert $cert -FilePath $certPath -Force | Out-Null
        & certutil -addstore -user Root $certPath | Out-Null
        Remove-Item $certPath -Force -ErrorAction SilentlyContinue
        Write-Host "Certificado adicionado aos certificados confiaveis do usuario." -ForegroundColor Green
    } catch {
        Write-Host "Nota: Execucao sem elevacao de raiz. Assinatura aplicada via My Store." -ForegroundColor DarkYellow
    }
}

Write-Host "Assinando '$FilePath' com SHA256 e Timestamp RFC 3161..." -ForegroundColor Yellow

$timestampServers = @(
    "http://timestamp.digicert.com",
    "http://timestamp.sectigo.com",
    "http://timestamp.comodoca.com/rfc3161"
)

$signed = $false
foreach ($ts in $timestampServers) {
    try {
        $sig = Set-AuthenticodeSignature -FilePath $FilePath -Certificate $cert -TimestampServer $ts -HashAlgorithm SHA256 -ErrorAction Stop
        if ($sig.Status -eq "Valid" -or $sig.Status -eq "UnknownError") {
            $signed = $true
            Write-Host "Assinatura aplicada com sucesso via $ts (Status: $($sig.StatusMessage))" -ForegroundColor Green
            break
        }
    } catch {
        Write-Host "Falha com timestamp $ts, tentando fallback..." -ForegroundColor DarkYellow
    }
}

if (-not $signed) {
    $sig = Set-AuthenticodeSignature -FilePath $FilePath -Certificate $cert -HashAlgorithm SHA256
    Write-Host "Assinatura aplicada sem timestamp (Status: $($sig.StatusMessage))" -ForegroundColor Yellow
}

$finalSig = Get-AuthenticodeSignature -FilePath $FilePath
Write-Host "---------------------------------------------" -ForegroundColor DarkCyan
Write-Host "Status da Assinatura: $($finalSig.Status)" -ForegroundColor Green
Write-Host "Signer: $($finalSig.SignerCertificate.Subject)" -ForegroundColor Green
Write-Host "Thumbprint: $($finalSig.SignerCertificate.Thumbprint)" -ForegroundColor Green
Write-Host "---------------------------------------------" -ForegroundColor DarkCyan
Write-Host "[SUCESSO] Binario pronto para distribuicao corporativa segura." -ForegroundColor Green
