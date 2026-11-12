// Run through stdin inside the isolated renderer container, never against production jobs.
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
const token=(await readFile(process.env.NT_MP4_TOKEN_FILE,'utf8')).trim();
const base='http://127.0.0.1:8090';
const headers={Authorization:'Bearer '+token};
const request=(path,options={})=>fetch(base+path,{...options,headers:{...headers,...options.headers},signal:AbortSignal.timeout(30000)});
const archive=duration=>[
 {type:'header',format:'terminal-events-v1',durationMs:duration,events:1},
 {sequence:1,kind:'output',elapsedMs:0,payload:Buffer.from('Puppeteer R17 service acceptance\r\n').toString('base64')},
 {type:'end',events:1},
].map(v=>JSON.stringify(v)).join('\n');
assert.equal((await fetch(base+'/jobs')).status,401);
let id;
try {
 const first=await request('/jobs',{method:'POST',body:archive(30000)});assert.equal(first.status,202);id=(await first.json()).id;
 assert.equal((await request('/jobs',{method:'POST',body:archive(500)})).status,429);
 assert.equal((await request('/jobs/'+id,{method:'DELETE'})).status,200);
 assert.equal((await request('/jobs/'+id)).status,404);id=undefined;
 const second=await request('/jobs',{method:'POST',body:archive(500)});assert.equal(second.status,202);id=(await second.json()).id;
 const until=Date.now()+60000;let ready=false;
 while(Date.now()<until){
  const state=await(await request('/jobs/'+id)).json();
  assert.notEqual(state.state,'failed');
  if(state.state==='succeeded'){ready=true;break}
  await new Promise(resolve=>setTimeout(resolve,200));
 }
 assert(ready,'conversion timed out');
 const result=await request('/jobs/'+id+'/result');assert.equal(result.status,200);
 const bytes=Buffer.from(await result.arrayBuffer());assert.equal(bytes.toString('ascii',4,8),'ftyp');assert(bytes.length>1000);
 console.log(JSON.stringify({unauthenticated:401,busy:429,cancel:true,conversionAfterCancellation:true,mp4Bytes:bytes.length}));
}finally{if(id)await request('/jobs/'+id,{method:'DELETE'});}
