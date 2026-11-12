param([string]$Operator = 'accept-admin')
$ErrorActionPreference = 'Stop'
$backendRoot = Join-Path (Split-Path -Parent $PSScriptRoot) 'backend'
$fixtureFile = Join-Path $backendRoot '.local/r02-fixtures.json'
if (-not (Test-Path -LiteralPath $fixtureFile)) {
    $fixtures = @('operator','reviewer','denied') | ForEach-Object {
        $bytes = New-Object byte[] 24
        [Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
        @{ username = $_; password = ('R02-' + [Convert]::ToHexString($bytes).ToLowerInvariant()) }
    }
    [IO.File]::WriteAllText($fixtureFile, ($fixtures | ConvertTo-Json))
}
Get-Content -LiteralPath $fixtureFile -Raw | docker compose --env-file (Join-Path $backendRoot '.local/compose.env') -f (Join-Path $backendRoot 'compose.yaml') exec -T server /app/nt seed-r02 --operator $Operator
if ($LASTEXITCODE -ne 0) { throw 'Fixture setup failed; existing users are not overwritten. Inspect the error above.' }
Write-Host "Local credentials: $fixtureFile"
