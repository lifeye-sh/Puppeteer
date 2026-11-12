import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {setTimeout as delay} from 'node:timers/promises';
const exec=promisify(execFile);
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export async function verifyNativeRecording(id,asset){
 if(!uuid.test(id)||!uuid.test(asset))throw Error('Invalid session or asset ID');
 const sql=`SELECT json_build_object('consumed',t.consumed_at IS NOT NULL,'connected',s.connected_at IS NOT NULL,
 'closed',s.closed_at IS NOT NULL,'state',r.state,'format',r.format,'segments',r.events,'bytes',r.bytes,
 'durationMs',c.ms,'consistent',r.events=c.n AND r.bytes=c.bytes AND c.valid)
 FROM sessions s JOIN native_rdp_tickets t ON t.session_id=s.id JOIN recordings r ON r.session_id=s.id
 CROSS JOIN LATERAL (SELECT count(*) n,coalesce(sum(octet_length(payload)),0) bytes,max(elapsed_ms) ms,
 bool_and(kind='mp4' AND media_sha256 IS NOT NULL AND sha256(payload)=media_sha256) valid
 FROM recording_chunks WHERE session_id=s.id) c WHERE s.id='${id}' AND s.asset_id='${asset}'`;
 for(let attempt=0;attempt<12;attempt++){
  const {stdout}=await exec('docker',['exec','nt-backend-r01-postgres-1','psql','-U','nt_dev','-d','nt_backend','-XtAc',sql],{timeout:6000,windowsHide:true});
  if(!stdout.trim())throw Error('No matching platform session');
  const v=JSON.parse(stdout);
  if(!v.consumed)throw Error('The gateway did not consume this ticket. No verified desktop recording.');
  if(v.closed){
   if(!v.connected||v.state!=='complete'||v.format!=='native-rdp-mp4-v1'||!v.consistent||v.segments<1||v.bytes<=0||v.durationMs<=0)throw Error('Session ended without a verified complete desktop recording');
   return v;
  }
  await delay(500);
 }
 throw Error('Recording finalization pending');
}
