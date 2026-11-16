import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,readdir,readFile,rm} from 'node:fs/promises';
import {convert} from './convert.mjs';

async function zombies(){
 let count=0;
 for(const pid of await readdir('/proc')){
  if(!/^\d+$/.test(pid))continue;
  const status=await readFile(`/proc/${pid}/status`,'utf8').catch(()=>'');
  if(/^State:\s+Z/m.test(status))count++;
 }
 return count;
}

// Run inside the production image/PID namespace. Without an init process,
// Chromium leaves adopted crash-handler children behind on every conversion.
test('repeated conversions and cancellations reap Chromium descendants within a fixed PID budget',async t=>{
 const dir=await mkdtemp('/tmp/mp4-reaping-'),baseline=await zombies();
 let watching=true,samples=0,positiveSamples=0,maxZombies=0;
 const observer=(async()=>{while(watching){const n=await zombies();samples++;if(n)positiveSamples++;maxZombies=Math.max(maxZombies,n);await new Promise(r=>setTimeout(r,100));}})();
 try{
  const input=dir+'/input.jsonl';
  await writeFile(input,[{type:'header',format:'terminal-events-v1',durationMs:500,events:1},
   {kind:'output',payload:Buffer.from('repeated conversion').toString('base64'),sequence:1,elapsedMs:0},
   {type:'end',events:1}].map(JSON.stringify).join('\n'));
  for(let i=0;i<25;i++){
   await convert(input,dir+'/output.mp4',AbortSignal.timeout(30000),()=>{});
   // Give PID 1 a bounded interval to reap newly adopted children.
   const deadline=Date.now()+2000;
   while(await zombies()>baseline&&Date.now()<deadline)await new Promise(r=>setTimeout(r,20));
   assert.ok(await zombies()<=baseline,`Chromium descendants leaked after conversion ${i+1}`);
   if(i%5===4){
    const controller=new AbortController();
    await assert.rejects(convert(input,dir+'/canceled.mp4',controller.signal,()=>controller.abort()));
    assert.equal(controller.signal.aborted,true);
    const until=Date.now()+2000;
    while(await zombies()>baseline&&Date.now()<until)await new Promise(r=>setTimeout(r,20));
    if(await zombies()>baseline){
     const tree=[];
     for(const pid of await readdir('/proc'))if(/^\d+$/.test(pid)){
      const status=await readFile(`/proc/${pid}/status`,'utf8').catch(()=>'');
      tree.push({pid,...Object.fromEntries(status.split('\n').filter(s=>/^(Name|State|PPid):/.test(s)).map(s=>s.split(/:\s+/,2)))});
     }
     t.diagnostic(JSON.stringify({cancellation:i+1,tree}));
     assert.fail(`Chromium descendants leaked after cancellation ${i+1}`);
    }
   }
  }
  assert.equal(await zombies(),baseline,'final idle process check');
  t.diagnostic(JSON.stringify({samples,positiveSamples,maxZombies,finalZombies:await zombies(),conversions:25,cancellations:5}));
 }finally{watching=false;await observer;await rm(dir,{recursive:true,force:true})}
});
