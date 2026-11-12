param(
 [switch]$Keep,
 [string]$TestBinary,
 [ValidatePattern('^[a-zA-Z0-9][a-zA-Z0-9._/:@-]+$')][string]$ServerImage='nt-backend-r01-server',
 [ValidatePattern('^[a-zA-Z0-9][a-zA-Z0-9._/:@-]+$')][string]$GraphicsImage='nt-backend-rdp-storage',
 [ValidatePattern('^nt-r[0-9]+-restore-drill$')][string]$ProjectName='nt-r11-restore-drill',
 [ValidateRange(1024,65535)][int]$Port=18091
)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
# Refuse before rewriting the existing drill's private connection configuration.
$running=@(& docker ps -aq --filter "label=com.docker.compose.project=$ProjectName")
if($LASTEXITCODE -ne 0 -or $running.Count -gt 0){throw 'An existing drill deployment was found; inspect it before rerunning.'}
$work=if($ProjectName -eq 'nt-r11-restore-drill'){Join-Path $root 'backend/.local/r11-deployment-drill'}else{Join-Path $root "backend/.local/$ProjectName"}
$offline=Join-Path $work 'offline'
$binary=if($TestBinary){(Resolve-Path -LiteralPath $TestBinary).Path}else{Join-Path $root 'backend/.local/r11-application.test'}
if(!(Test-Path -LiteralPath $binary -PathType Leaf)){throw 'Compile the R11 application test binary first.'}
New-Item -ItemType Directory -Force $offline | Out-Null
$envFile=Join-Path $work 'drill.env'
$dbPassword=[Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)).ToLowerInvariant()
@("R11_DRILL_DB_PASSWORD=$dbPassword","R11_DRILL_MFA_KEY=$(('2a'*32))","R11_DRILL_VAULT_KEY=$(('2b'*32))","R11_DRILL_TEST_BINARY=$($binary.Replace('\','/'))","R11_DRILL_OFFLINE_DIRECTORY=$($offline.Replace('\','/'))","R11_DRILL_SERVER_IMAGE=$ServerImage","R11_DRILL_GRAPHICS_IMAGE=$GraphicsImage","R11_DRILL_PORT=$Port") | Set-Content -LiteralPath $envFile -Encoding utf8
'ephemeral-r11-deployment' | Set-Content -LiteralPath (Join-Path $offline '.r11-deployment-context') -NoNewline
$compose=Join-Path $root 'backend/fixtures/restore/compose.yaml'
$project=$ProjectName
$argsBase=@('compose','--env-file',$envFile,'-f',$compose,'-p',$project)
function Docker-Step([string[]]$Arguments){& docker @argsBase @Arguments;if($LASTEXITCODE -ne 0){throw "Drill Docker step failed: $($Arguments[0])"}}
$existing=@(& docker @argsBase ps -a -q)
if($LASTEXITCODE -ne 0 -or $existing.Count -gt 0){throw 'An existing R11 drill deployment was found; inspect it before rerunning.'}
$restore=Join-Path $PSScriptRoot 'restore-backend.ps1'
$common=@{ComposeFile=$compose;EnvFile=$envFile;ProjectName=$project;ReadyUrl="http://127.0.0.1:$Port/readyz"}
try {
 Docker-Step @('up','-d','--wait','postgres')
 Docker-Step @('exec','-T','postgres','sh','-c','mkdir -p /drill/offline; printf ephemeral-r11-pitr > /drill/.r11-pitr-context; NT_PITR_MODE=seed NT_PITR_FIXTURE_DSN="postgres://nt_drill:${POSTGRES_PASSWORD}@127.0.0.1:5432/nt_restore_pitr_fixture?sslmode=disable" /fixtures/r11-application.test -test.run ''^TestR11PITRFixture$'' -test.v')
 foreach($file in @('recovery.age','identity','old-session.json')){Docker-Step @('cp',"postgres:/drill/offline/$file",(Join-Path $offline $file))}
 Docker-Step @('exec','-T','postgres','psql','-U','nt_drill','-d','nt_restore_pitr_fixture','-v','ON_ERROR_STOP=1','-c',"INSERT INTO r11_pitr_marker(id) VALUES('after-archive')")
 Docker-Step @('up','-d','--wait','server','guacd')
 $timer=[Diagnostics.Stopwatch]::StartNew()
 $result=(& $restore @common -Action Restore -Archive (Join-Path $offline 'recovery.age') -IdentityFile (Join-Path $offline 'identity') -TrustSource) -join "`n"
 Write-Output $result
 $timer.Stop();$restoreSeconds=$timer.Elapsed.TotalSeconds
 $ready=Invoke-RestMethod $common.ReadyUrl
 $restoreId=$ready.restoreId
 if(!$ready.frozen -or !$restoreId){throw 'Restore did not start frozen.'}
 & $restore @common -Action Rollback -RestoreId $restoreId
 & $restore @common -Action Resume -RestoreId $restoreId
 $timer.Restart()
 & $restore @common -Action Activate -RestoreId $restoreId
 $timer.Stop();$activateSeconds=$timer.Elapsed.TotalSeconds
 Docker-Step @('--profile','test','run','--rm','--no-deps','-T','verify','-test.run','^TestR11DeploymentFixture$','-test.v','-test.timeout=60s')
 $rejected=$false
 try{& $restore @common -Action Rollback -RestoreId $restoreId}catch{if($_.Exception.Message -match 'Automatic rollback is prohibited'){$rejected=$true}else{throw}}
 if(!$rejected){throw 'Rollback after writes was accepted.'}
 $ready=Invoke-RestMethod $common.ReadyUrl
 if($ready.status -ne 'ready' -or $ready.frozen){throw 'Refused rollback interrupted the active service.'}
 $summary=[ordered]@{status='passed';restoreId=$restoreId;restoreToFrozenSeconds=[math]::Round($restoreSeconds,3);activateSeconds=[math]::Round($activateSeconds,3);checks=@('safety archive','restricted staging database','API and graphics readiness','rollback before writes','resume','old session denied','new login and restore history','shared file and secrets','post-write rollback refused without stopping services')}
 $summary|ConvertTo-Json -Depth 4|Set-Content -LiteralPath (Join-Path $work 'result.json')
 $summary|ConvertTo-Json -Depth 4
} finally {
 if(!$Keep){Docker-Step @('down','--volumes','--remove-orphans')}
}
