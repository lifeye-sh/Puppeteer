param([switch]$BuildSdk, [switch]$NoBuild)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    if (-not (Test-Path -LiteralPath 'backend/.local/compose.env')) {
        throw 'Local compose.env is required. Configure the existing isolated PostgreSQL test environment first.'
    }
    if ($BuildSdk) {
        & docker build -f backend/native-rdp.Dockerfile -t nt-native-rdp-sdk:3.32.2 backend
        if ($LASTEXITCODE -ne 0) { throw 'FreeRDP SDK build failed' }
    }
    if (-not $NoBuild) {
        & docker build -f backend/native-rdp-worker.Dockerfile -t nt-native-rdp-worker:dev backend
        if ($LASTEXITCODE -ne 0) { throw 'FreeRDP probe worker build failed; build the fixed SDK first with -BuildSdk' }
    }
    & docker compose --env-file backend/.local/compose.env -f backend/compose.yaml -f backend/compose.native-rdp-test.yaml run --rm --no-deps tests go test -race ./internal/platform/rdpworker ./internal/application -run 'TestR14|TestNativeRDP|TestSupervisor|TestControl|TestTarget' -count=1 -timeout=8m -v
    if ($LASTEXITCODE -ne 0) { throw 'Native RDP protocol acceptance failed' }
    Write-Output 'R14 isolated protocol/race acceptance passed. This does not certify a Windows login, desktop, recording, channel policy, or production deployment.'
}
finally { Pop-Location }
