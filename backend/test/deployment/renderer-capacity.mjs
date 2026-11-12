// Runs only inside a disposable renderer container with bounded /tmp tmpfs.
import assert from 'node:assert/strict';
import {open,rm,readdir,readFile} from 'node:fs/promises';
const token=process.env.NT_MP4_TOKEN;
const request=(path,options={})=>fetch('http://127.0.0.1:8090'+path,{...options,headers:{Authorization:'Bearer '+token,...options.headers},signal:AbortSignal.timeout(30000)});
const archive=duration=>[
 {type:'header',format:'terminal-events-v1',durationMs:duration,events:1},
 {sequence:1,kind:'output',elapsedMs:0,payload:Buffer.from('R17 capacity drill\r\n').toString('base64')},
 {type:'end',events:1},
].map(v=>JSON.stringify(v)).join('\n');
const ids=new Set(),evidence={checks:[],passed:false};
const submit=async duration=>{const r=await request('/jobs',{method:'POST',body:archive(duration)});assert.equal(r.status,202);const {id}=await r.json();ids.add(id);return id;};
const remove=async id=>{assert.equal((await request('/jobs/'+id,{method:'DELETE'})).status,200);ids.delete(id);};
async function wait(id,want){for(let i=0;i<300;i++){const s=await(await request('/jobs/'+id)).json();if(s.state===want)return;if(s.state==='failed'&&want!=='failed')throw Error('conversion failed');await new Promise(r=>setTimeout(r,200));}throw Error('job timeout');}
try{
 for(let i=0;i<40;i++){try{if((await fetch('http://127.0.0.1:8090/healthz')).ok)break}catch{}await new Promise(r=>setTimeout(r,250));}
 const responses=await Promise.all(Array.from({length:16},()=>request('/jobs',{method:'POST',body:archive(120000)})));
 assert.equal(responses.filter(r=>r.status===202).length,1);assert.equal(responses.filter(r=>r.status===429).length,15);
 const {id}=await responses.find(r=>r.status===202).json();ids.add(id);await remove(id);
 evidence.checks.push('16 concurrent requests: 1 accepted, 15 busy; cancellation releases slot');
 for(let n=0;n<4;n++){const job=await submit(300);await wait(job,'succeeded');}
 assert.equal((await request('/jobs',{method:'POST',body:archive(300)})).status,429);
 for(const job of [...ids])await remove(job);
 evidence.checks.push('four retained results enforce capacity; deletion frees slots');
 const long=await submit(120000);let killed=false;
 for(let n=0;n<100&&!killed;n++){
  for(const pid of (await readdir('/proc')).filter(x=>/^\d+$/.test(x))){
   try{if((await readFile('/proc/'+pid+'/comm','utf8')).trim()==='ffmpeg'&&!/^State:\s+Z/m.test(await readFile('/proc/'+pid+'/status','utf8'))){process.kill(Number(pid),'SIGKILL');killed=true;break}}catch{}
  }
  if(!killed)await new Promise(r=>setTimeout(r,100));
 }
 assert.ok(killed,'encoder process did not start');await wait(long,'failed');await remove(long);
 evidence.checks.push('killed encoder becomes failed job; cleanup releases capacity');
 const filler=await open('/tmp/r17-disk-fill','wx');
 try{const chunk=Buffer.alloc(1024*1024);while(true)await filler.write(chunk);}catch(e){assert.equal(e.code,'ENOSPC')}finally{await filler.close()}
 assert.equal((await request('/jobs',{method:'POST',body:archive(300)})).status,507);
 assert.equal((await fetch('http://127.0.0.1:8090/healthz')).status,200);
 await rm('/tmp/r17-disk-fill');
 const recovered=await submit(300);await wait(recovered,'succeeded');
 const bytes=Buffer.from(await(await request('/jobs/'+recovered+'/result')).arrayBuffer());assert.equal(bytes.toString('ascii',4,8),'ftyp');await remove(recovered);
 evidence.checks.push('full tmpfs returns 507; health remains available; freeing space restores MP4 conversion');
 const leftovers=(await readdir('/tmp')).filter(x=>x.startsWith('mp4-'));assert.deepEqual(leftovers,[]);
 evidence.mp4Bytes=bytes.length;evidence.passed=true;
}finally{
 await rm('/tmp/r17-disk-fill',{force:true});
 for(const id of ids)await request('/jobs/'+id,{method:'DELETE'});
 console.log(JSON.stringify(evidence));
}
