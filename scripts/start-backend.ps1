param([switch]$TestOnly)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$backendRoot = Join-Path $projectRoot 'backend'
$localRoot = Join-Path $backendRoot '.local'
$envFile = Join-Path $localRoot 'compose.env'
New-Item -ItemType Directory -Force -Path $localRoot | Out-Null
if (-not (Test-Path -LiteralPath $envFile)) {
    $randomBytes = New-Object byte[] 32
    [Security.Cryptography.RandomNumberGenerator]::Fill($randomBytes)
    $dbPassword = [Convert]::ToHexString($randomBytes).ToLowerInvariant()
    [IO.File]::WriteAllText($envFile, "NT_DB_PASSWORD=$dbPassword`n")
}
if (-not (Select-String -LiteralPath $envFile -Pattern '^NT_MFA_KEY=' -Quiet)) {
    $keyBytes = New-Object byte[] 32
    [Security.Cryptography.RandomNumberGenerator]::Fill($keyBytes)
    [IO.File]::AppendAllText($envFile, "NT_MFA_KEY=$([Convert]::ToHexString($keyBytes).ToLowerInvariant())`n")
}
if (-not (Select-String -LiteralPath $envFile -Pattern '^NT_VAULT_KEY=' -Quiet)) {
    $vaultBytes = New-Object byte[] 32
    [Security.Cryptography.RandomNumberGenerator]::Fill($vaultBytes)
    [IO.File]::AppendAllText($envFile, "NT_VAULT_KEY=$([Convert]::ToHexString($vaultBytes).ToLowerInvariant())`n")
}
if (-not (Select-String -LiteralPath $envFile -Pattern '^NT_MP4_TOKEN=' -Quiet)) {
    $mp4Bytes = New-Object byte[] 32
    [Security.Cryptography.RandomNumberGenerator]::Fill($mp4Bytes)
    [IO.File]::AppendAllText($envFile, "NT_MP4_TOKEN=$([Convert]::ToHexString($mp4Bytes).ToLowerInvariant())`n")
}
docker info --format '{{.ServerVersion}}' | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Docker Engine is not ready. Start Docker Desktop and retry.' }
$composeArgs = @('compose', '--env-file', $envFile, '-f', (Join-Path $backendRoot 'compose.yaml'))
if ($TestOnly) {
    & docker @composeArgs --profile test run --rm tests
} else {
    $taskLocalGraphics = $env:NT_GUACD_ADDR -eq 'guacd:4822' -or
        ((-not $env:NT_GUACD_ADDR) -and (Select-String -LiteralPath $envFile -Pattern '^NT_GUACD_ADDR=guacd:4822\s*$' -Quiet))
    if ($taskLocalGraphics) {
        & docker @composeArgs --profile graphics up -d --build --wait guacd mp4-renderer server
    } else {
        & docker @composeArgs up -d --build --wait mp4-renderer server
    }
}
if ($LASTEXITCODE -ne 0) { throw 'Backend command failed; inspect Docker output above.' }
if (-not $TestOnly) {
    Write-Host 'Backend: http://127.0.0.1:8089 ; frontend: npm run dev:backend'
    Write-Host 'Open http://backend.localhost:5174 to initialize or log in.'
}
