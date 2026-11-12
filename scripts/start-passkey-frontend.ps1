$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$env:BACKEND_TLS_CERT = Join-Path $projectRoot 'backend/.local/passkey-tls/server.crt'
$env:BACKEND_TLS_KEY = Join-Path $projectRoot 'backend/.local/passkey-tls/server.key'
if (!(Test-Path -LiteralPath $env:BACKEND_TLS_CERT) -or !(Test-Path -LiteralPath $env:BACKEND_TLS_KEY)) {
    throw 'Missing development TLS files. Generate and trust the project certificate before starting HTTPS.'
}
if (Get-NetTCPConnection -LocalPort 5174 -State Listen -ErrorAction SilentlyContinue) {
    throw 'Port 5174 is already in use. Keep the existing service or stop the identified project process first.'
}
Start-Process -FilePath (Get-Command node).Source -ArgumentList @('node_modules/vite/bin/vite.js','--mode','backend','--host','127.0.0.1','--port','5174','--strictPort') -WorkingDirectory $projectRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $projectRoot 'backend/.local/r13-https.log') -RedirectStandardError (Join-Path $projectRoot 'backend/.local/r13-https-error.log')
Write-Output 'Started https://backend.localhost:5174; see backend/.local/r13-https*.log.'
