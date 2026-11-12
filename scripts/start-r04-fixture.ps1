param([Parameter(Mandatory=$true)][string]$Operator)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path $PSScriptRoot -Parent
Push-Location $repoRoot
try {
    $composeArgs = @('compose','--env-file','backend/.local/compose.env','-f','backend/compose.yaml')
    $fixtureJson = & docker @composeArgs exec -T server /app/nt seed-r04 --operator $Operator
    if ($LASTEXITCODE -ne 0) { throw 'R04 fixture seed failed. Start the R04 backend first.' }
    $fixture = $fixtureJson | ConvertFrom-Json
    $fixtureJson | Set-Content -LiteralPath backend/.local/r04-fixtures.json -Encoding utf8
    [IO.File]::WriteAllText((Join-Path $repoRoot 'backend/.local/r04-authorized_keys'), $fixture.publicKey)
    & docker @composeArgs --profile ssh-fixture up -d --build ssh-fixture
    if ($LASTEXITCODE -ne 0) { throw 'OpenSSH fixture startup failed.' }
    & docker @composeArgs exec -T ssh-fixture ssh-keygen -lf /hostkeys/ssh_host_ed25519_key.pub
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read host fingerprint. Check ssh-fixture logs.' }
    Write-Output "Asset ID: $($fixture.assetId)"
    Write-Output 'Compare this console fingerprint with the SSH host-key dialog before trusting it.'
} finally { Pop-Location }
