param([string]$Network = 'nt-backend-r01_default', [ValidateRange(5,240)][int]$Minutes = 120)
$ErrorActionPreference = 'Stop'
$r16TestTimeout = [string]($Minutes + 2) + 'm'
$r16Root = (Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
$r16Backend = (Join-Path $r16Root 'backend').Replace('\','/')
$r16Directory = Join-Path $r16Root 'backend/.local/r16-dbeaver'
New-Item -ItemType Directory -Force -Path $r16Directory | Out-Null
# Port is available only on this host. No business service or credentials change.
docker run --rm --name nt-r16-dbeaver-manual --network $Network `
    -p 127.0.0.1:15433:15433 `
    -v nt-backend-r01_gomod:/go/pkg/mod -v nt-backend-r01_gobuild:/root/.cache/go-build `
    -v "${r16Backend}:/src" -w /src -e GOMAXPROCS=2 `
    -e 'NT_TEST_DATABASE_URL=postgres://postgres:r16-disposable-fixture-only@r16-postgres-fixture:5432/nt_r16_test?sslmode=disable' `
    -e "NT_R16_DBEAVER_MINUTES=$Minutes" `
    -e NT_R16_PG_FIXTURE=/src/.local/r16-pg -e NT_R16_DBEAVER_DIR=/src/.local/r16-dbeaver `
    nt-r16-postgres-client-tests go test ./internal/application -run '^TestR16DBeaverManualFixture$' -v -count=1 -timeout $r16TestTimeout
if ($LASTEXITCODE -ne 0) { throw 'Desktop acceptance fixture did not finish successfully' }
