param(
 [ValidateSet('Restore','Resume','Rollback','Activate','Status')][string]$Action='Status',
 [string]$Archive,[string]$IdentityFile,[string]$RestoreId,[switch]$OpenWrites,[switch]$TrustSource,
 [string]$ComposeFile,[string]$EnvFile,[string]$ProjectName,
 [string]$ReadyUrl='http://127.0.0.1:8089/readyz'
)
$ErrorActionPreference='Stop'
$projectRoot=Split-Path -Parent $PSScriptRoot
if(!$ComposeFile){$ComposeFile=Join-Path $projectRoot 'backend/compose.yaml'}
if(!$EnvFile){$EnvFile=Join-Path $projectRoot 'backend/.local/compose.env'}
$composeArgs=@('compose','--env-file',$EnvFile,'-f',$ComposeFile,'--profile','graphics')
if($ProjectName){$composeArgs+=@('-p',$ProjectName)}
function Invoke-RestoreCli([string[]]$CliArgs,[string[]]$Mounts=@()){
 $reply=& docker @composeArgs run --rm --no-deps -T --entrypoint /app/nt @Mounts server @CliArgs
 $exitCode=$LASTEXITCODE
 if($reply){try{$parsed=($reply -join "`n")|ConvertFrom-Json;if($parsed -isnot [array] -and $parsed.restoreId -is [string]){$script:RestoreId=$parsed.restoreId}}catch{}}
 if($exitCode -ne 0){throw "Restore CLI failed. Recovery ID: $script:RestoreId. Services remain stopped if a switch was in progress."}
 return ($reply -join "`n")
}
function Wait-Runtime([bool]$Frozen){
 $deadline=(Get-Date).AddSeconds(90)
 while((Get-Date) -lt $deadline){
  try{$ready=Invoke-RestMethod -Uri $ReadyUrl -TimeoutSec 3;if($ready.status -eq 'ready' -and $ready.restoreId -eq $script:RestoreId -and $ready.frozen -eq $Frozen){return}}catch{}
  Start-Sleep -Seconds 1
 }
 throw "Runtime readiness timed out for $script:RestoreId. Business remains frozen; inspect status before retrying."
}
function Wait-Graphics {
 $created=@(& docker @composeArgs ps -a --services)
 if($LASTEXITCODE -ne 0){throw 'Cannot inspect graphics service.'}
 if($created -notcontains 'guacd'){return}
 $containerId=(& docker @composeArgs ps -a -q guacd).Trim()
 $deadline=(Get-Date).AddSeconds(90)
 do{$health=(& docker inspect --format '{{.State.Health.Status}}' $containerId).Trim();if($health -eq 'healthy'){return};Start-Sleep -Seconds 1}while((Get-Date) -lt $deadline)
 throw 'Graphics storage is not healthy. Business remains frozen.'
}
if($Action -eq 'Restore'){
 if(!$TrustSource){throw 'Use -TrustSource only for an archive from a trusted instance; restored SQL is executable.'}
 if(!$Archive -or !$IdentityFile){throw 'Archive and IdentityFile are required.'}
 $archivePath=(Resolve-Path -LiteralPath $Archive).Path
 $identityPath=(Resolve-Path -LiteralPath $IdentityFile).Path
 if(!(Test-Path -LiteralPath $archivePath -PathType Leaf) -or !(Test-Path -LiteralPath $identityPath -PathType Leaf)){throw 'Restore inputs must be regular files.'}
}elseif(!$RestoreId){throw 'RestoreId is required.'}
if($Action -eq 'Status'){Invoke-RestoreCli @('restore','status','--restore-id',$RestoreId);return}
if($Action -eq 'Activate'){
 Wait-Runtime $true
 Wait-Graphics
 Invoke-RestoreCli @('restore','activate','--restore-id',$RestoreId)
 Wait-Runtime $false
 return
}
if($Action -eq 'Rollback'){
 $events=(Invoke-RestoreCli @('restore','status','--restore-id',$RestoreId))|ConvertFrom-Json
 if($events.state -contains 'writes_opened' -or $events.state -contains 'completed'){throw 'Writes were opened. Automatic rollback is prohibited; services were not stopped.'}
}
$existing=@(& docker @composeArgs ps -a --services)
if($LASTEXITCODE -ne 0){throw 'Cannot inspect deployment services.'}
$services=@('server');if($existing -contains 'guacd'){$services+='guacd'}
& docker @composeArgs stop @services
if($LASTEXITCODE -ne 0){throw 'Cannot stop all database consumers; restore aborted.'}
try{
 if($Action -eq 'Restore'){
  Invoke-RestoreCli @('restore','run','--archive','/restore-input/archive','--identity-file','/restore-input/identity','--trust-source') @('-v',"${archivePath}:/restore-input/archive:ro",'-v',"${identityPath}:/restore-input/identity:ro")
 }else{Invoke-RestoreCli @('restore',$Action.ToLowerInvariant(),'--restore-id',$RestoreId)}
 & docker @composeArgs start @services
 if($LASTEXITCODE -ne 0){throw 'Service startup failed; use resume or rollback before opening writes.'}
 if($Action -eq 'Rollback'){Write-Output "Rolled back $RestoreId. Original deployment selected.";return}
 Wait-Runtime $true
 Wait-Graphics
 if($OpenWrites){Invoke-RestoreCli @('restore','activate','--restore-id',$RestoreId);Wait-Runtime $false}
 else{Write-Output "Restore $RestoreId verified and frozen. Inspect the independent log, then run -Action Activate -RestoreId $RestoreId, or Rollback before opening writes."}
}catch{Write-Error "Recovery ID: $RestoreId. $($_.Exception.Message)";throw}
