$ErrorActionPreference = 'Stop'
$r16Root = (Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
$r16Backend = (Join-Path $r16Root 'backend').Replace('\','/')
$r16DriverDir = Join-Path $r16Root 'backend/.local/r16-jdbc'
New-Item -ItemType Directory -Force -Path $r16DriverDir | Out-Null
$r16Jar = Join-Path $r16DriverDir 'postgresql-42.7.14.jar'
if (!(Test-Path -LiteralPath $r16Jar)) {
    Invoke-WebRequest -Uri 'https://jdbc.postgresql.org/download/postgresql-42.7.14.jar' -OutFile $r16Jar
}
if ((Get-FileHash -LiteralPath $r16Jar -Algorithm SHA256).Hash -ne '73914527305A40CCE504B0D3D90B23CAF565136912D607AE8AE7C5895512332C') {
    throw 'Unexpected PostgreSQL JDBC artifact checksum'
}
docker build -t nt-r16-postgres-client-tests -f (Join-Path $PSScriptRoot 'Dockerfile') $PSScriptRoot
if ($LASTEXITCODE -ne 0) { throw 'Test image build failed' }
# This DSN deliberately targets only the disposable internal fixture, never the business database.
$r16Args = @('run','--rm','--network','nt-backend-r01_default',
    '-v','nt-backend-r01_gomod:/go/pkg/mod','-v','nt-backend-r01_gobuild:/root/.cache/go-build',
    '-v',"${r16Backend}:/src",'-w','/src','-e','GOMAXPROCS=2',
    '-e','NT_TEST_DATABASE_URL=postgres://postgres:r16-disposable-fixture-only@r16-postgres-fixture:5432/nt_r16_test?sslmode=disable',
    '-e','NT_R16_PG_FIXTURE=/src/.local/r16-pg',
    '-e','NT_R16_JDBC=/src/.local/r16-jdbc/postgresql-42.7.14.jar','-e','NT_R16_PSQL=1',
    'nt-r16-postgres-client-tests','sh','-c',
    'go test -p 1 ./internal/platform/pgproxy ./internal/modules/databaseauth -count=1 -timeout=180s && go test ./internal/application -run TestR16 -v -count=1 -timeout=180s')
docker @r16Args
if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL client acceptance failed' }
