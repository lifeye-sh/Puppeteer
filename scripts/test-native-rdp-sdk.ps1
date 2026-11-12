$ErrorActionPreference = 'Stop'
$image = 'nt-native-rdp-sdk:3.32.2'
$flags = @('run','--rm','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','10001:10001',$image)
$version = & docker @flags freerdp-proxy --version 2>&1
if ($LASTEXITCODE -ne 0 -or ($version -join "`n") -notmatch 'version 3\.32\.2') {
    throw 'FreeRDP SDK version/startup check failed'
}
$symbols = & docker @flags nm -D /opt/freerdp/lib/libfreerdp-server-proxy3.so
if ($LASTEXITCODE -ne 0 -or ($symbols -join "`n") -notmatch '\bT pf_server_start_with_peer_socket\b') {
    throw 'Required peer socket interface missing'
}
Write-Output 'FreeRDP 3.32.2 SDK: non-root startup and peer socket export passed. Native RDP business acceptance is not covered.'
