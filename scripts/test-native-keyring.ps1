param([switch]$BuildFixture)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    & docker image inspect nt-native-keyring-test:dev *> $null
    if ($BuildFixture -or $LASTEXITCODE -ne 0) {
        & docker build -f backend/native-keyring-test.Dockerfile -t nt-native-keyring-test:dev backend
        if ($LASTEXITCODE -ne 0) { throw 'Keyring fixture build failed; requires the existing nt-native-rdp-worker:dev SDK image' }
    }
    & docker compose --env-file backend/.local/compose.env -f backend/compose.yaml -f backend/compose.native-rdp-test.yaml run --rm --no-deps tests go test -race -c -o .local/nativeclient-keyring-linux.test ./internal/nativeclient
    if ($LASTEXITCODE -ne 0) { throw 'Keyring acceptance test build failed' }
    $testBinary = Join-Path $projectRoot 'backend/.local/nativeclient-keyring-linux.test'
    $fixtureScript = Join-Path $projectRoot 'backend/fixtures/keyring/accept.sh'
    & docker run --rm --network none --mount "type=bind,source=$testBinary,target=/acceptance/nativeclient.test,readonly" --mount "type=bind,source=$fixtureScript,target=/acceptance/accept.sh,readonly" --tmpfs /home/keyring-test:uid=10001,gid=10001,mode=700 nt-native-keyring-test:dev dbus-run-session -- sh /acceptance/accept.sh
    if ($LASTEXITCODE -ne 0) { throw 'Isolated GNOME Keyring acceptance failed' }
    Write-Output 'Linux Secret Service acceptance passed in a disposable, non-root, offline container. No host keyring or D-Bus socket was mounted.'
}
finally { Pop-Location }
