// Host-side one-shot worker, suitable for systemd timers. Never mounts Docker's socket in an app.
import {spawnSync} from 'node:child_process';
import {mkdir,readFile,writeFile,rename,rmdir} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {resolve} from 'node:path';
import {randomUUID} from 'node:crypto';

export async function runMaintenance({container,directory,operation,webhookFile,allowLocalHTTP=false}) {
 if(!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(container||'')||!['check','full','diff'].includes(operation)) throw Error('Valid container and operation check|full|diff required');
 const dir=resolve(directory),lock=resolve(dir,'running.lock');
 await mkdir(dir,{recursive:true,mode:0o700});
 try{await mkdir(lock,{mode:0o700})}catch(e){if(e.code==='EEXIST')throw Error('Maintenance already running; verify any stale lock manually');throw e;}
 try {
  const file=resolve(dir,'state.json');
  let state={container,pending:[],lastSuccess:{}};
  try{state=JSON.parse(await readFile(file,'utf8'))}catch(e){if(e.code!=='ENOENT')throw e;}
  if(state.container!==container||!Array.isArray(state.pending)||!state.lastSuccess)throw Error('State belongs to another container or is invalid');
  const save=async()=>{const tmp=resolve(dir,'state.tmp');await writeFile(tmp,JSON.stringify(state,null,2)+'\n',{mode:0o600});await rename(tmp,file);};
  // Validate delivery configuration before running a database job.
  const target=new URL((await readFile(webhookFile,'utf8')).trim());
  if(target.username||target.password||target.hash||!(target.protocol==='https:'||(allowLocalHTTP&&target.protocol==='http:'&&['localhost','127.0.0.1','[::1]'].includes(target.hostname))))throw Error('HTTPS webhook required (test HTTP is loopback-only)');
  const args=['exec','--user','postgres',container,'pgbackrest-file','--stanza=puppeteer'];
  args.push(...(operation==='check'?['check']:[`--type=${operation}`,'--start-fast','backup']));
  const started=new Date().toISOString();
  const result=spawnSync('docker',args,{encoding:'utf8',timeout:15*60*1000,maxBuffer:1024*1024});
  const ok=!result.error&&result.status===0;
  // Never forward raw subprocess output: it may contain paths or deployment details.
  const event={id:randomUUID(),kind:'wal.maintenance',operation,status:ok?'succeeded':'failed',started,finished:new Date().toISOString()};
  if(ok)state.lastSuccess[operation]=event.finished;
  state.pending.push(event);state.lastRun=event;
  await save();
  while(state.pending.length) {
   try {
    const response=await fetch(target,{method:'POST',redirect:'error',signal:AbortSignal.timeout(10000),headers:{'content-type':'application/json'},body:JSON.stringify(state.pending[0])});
    await response.body?.cancel();
    if(!response.ok)break;
   }catch{break;}
   state.pending.shift();await save();
  }
  return {operation,status:event.status,pendingNotifications:state.pending.length,lastSuccess:state.lastSuccess};
 }finally{await rmdir(lock);}
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
 const options={};
 for(let i=2;i<process.argv.length;i++){
  const key=process.argv[i];
  if(key==='--allow-local-http'){options.allowLocalHTTP=true;continue;}
  const field={'--container':'container','--state-dir':'directory','--operation':'operation','--webhook-file':'webhookFile'}[key];
  if(!field||!process.argv[i+1])throw Error('Usage: --container NAME --state-dir DIR --operation check|full|diff --webhook-file FILE');
  options[field]=process.argv[++i];
 }
 try{const result=await runMaintenance(options);console.log(JSON.stringify(result));if(result.status!=='succeeded'||result.pendingNotifications)process.exitCode=1;}
 catch(e){console.error(e.message);process.exitCode=1;}
}
