param([switch]$NoBuild,[switch]$WithMP4Export)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$previousImage = $env:NT_RDP_TEST_IMAGE
Push-Location $projectRoot
try {
    if (-not $NoBuild) {
        & docker build -f backend/native-rdp-worker.Dockerfile -t nt-native-rdp-worker:dev backend
        if ($LASTEXITCODE -ne 0) { throw 'Recorded desktop worker build failed. Build the fixed native RDP SDK first.' }
        & docker build -f backend/native-recording-test.Dockerfile -t nt-native-recording-test:dev backend
        if ($LASTEXITCODE -ne 0) { throw 'Recorder fixture build failed; build native RDP SDK/worker first.' }
    }
    $env:NT_RDP_TEST_IMAGE = 'nt-native-recording-test:dev'
    & docker compose --env-file backend/.local/compose.env -f backend/compose.yaml -f backend/compose.native-rdp-test.yaml run --rm --no-deps -e TMPDIR=/tmp -e NT_REQUIRE_RDP_RECORDER=1 tests go test -race ./internal/platform/rdprecording -count=1 -timeout=2m -v
    if ($LASTEXITCODE -ne 0) { throw 'Native recording acceptance failed' }
    $extraEnv = @()
    if ($WithMP4Export) { $extraEnv = @('-e', 'NT_MP4_TEST_URL=http://mp4-renderer:8090') }
    & docker compose --env-file backend/.local/compose.env -f backend/compose.yaml -f backend/compose.native-rdp-test.yaml run --rm --no-deps -e NT_RDP_DESKTOP_FIXTURE=/opt/nt/bin/nt-rdp-desktop-fixture @extraEnv tests go test -race ./internal/platform/rdpworker ./internal/application -run 'TestNativeMediaInterruptedPrefix|TestR14FreeRDPAuthProbe|TestControl|TestSupervisor|TestTarget' -count=1 -timeout=3m -v
    if ($LASTEXITCODE -ne 0) { throw 'Recorded native RDP session acceptance failed' }
    Write-Output 'Native RDP capture, pixel playback comparison, revocation and recorder failure checks passed in the isolated FreeRDP environment.'
}
finally {
    $env:NT_RDP_TEST_IMAGE = $previousImage
    Pop-Location
}
