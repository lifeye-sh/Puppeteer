import http from 'node:http';
import {randomUUID,timingSafeEqual} from 'node:crypto';
import {mkdtemp,rm,stat} from 'node:fs/promises';
import {createReadStream} from 'node:fs';
import {join} from 'node:path';
import {pipeline} from 'node:stream/promises';
import {convert} from './convert.mjs';
import {rendererToken} from './secrets.mjs';
import {scratchRoot,initializeScratch} from './scratch.mjs';
import {receiveArchive} from './upload.mjs';
import {quality} from './quality.mjs';
import {admitResources} from './pressure.mjs';
import {Lifecycle} from './lifecycle.mjs';
const profile=quality(process.env.NT_MP4_QUALITY||'balanced'),lifecycle=new Lifecycle();
await initializeScratch();
const token=rendererToken();
const jobs=new Map();let accepting=false;
function json(res,status,value){res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value))}
async function remove(id){const job=jobs.get(id);if(!job)return;job.controller.abort();await job.done;await rm(job.dir,{recursive:true,force:true});jobs.delete(id);lifecycle.touch()}
const server=http.createServer(async(req,res)=>{
 try{
  if(req.url==='/healthz'&&req.method==='GET'){json(res,200,{status:'ready'});return}
  const supplied=Buffer.from(req.headers.authorization??''),expected=Buffer.from('Bearer '+token);
  if(supplied.length!==expected.length||!timingSafeEqual(supplied,expected)){json(res,401,{error:'unauthorized'});return}
  if(req.method==='POST'&&req.url==='/runtime/resume'){lifecycle.resume();json(res,200,{ready:true});return}
  if(req.method==='POST'&&req.url==='/runtime/drain'){json(res,200,{drained:lifecycle.drain(accepting||jobs.size!==0)});return}
  if(req.url==='/runtime/admission'&&req.method==='GET'){
   if(lifecycle.draining||accepting||[...jobs.values()].some(j=>j.state==='running')||jobs.size>=4){json(res,429,{error:'renderer_busy'});return}
   await admitResources();json(res,200,{ready:true});return
  }
  if(req.url==='/jobs'&&req.method==='POST'){
   if(lifecycle.draining){json(res,503,{error:'renderer_standby'});return}
   if(Number(req.headers['content-length'])>150*1024*1024){json(res,413,{error:'input_limit'});return}
   if(accepting||[...jobs.values()].some(j=>j.state==='running')||jobs.size>=4){json(res,429,{error:'busy'});return}
   accepting=true;lifecycle.touch();let dir;
   try{
    await admitResources();
    dir=await mkdtemp(join(scratchRoot,'mp4-job-'));
    const id=randomUUID(),input=dir+'/input.jsonl',output=dir+'/output.mp4';
    await receiveArchive(req,input);
    const job={dir,output,controller:new AbortController(),state:'running',progress:0,reason:'',at:Date.now()};jobs.set(id,job);
    job.done=convert(input,output,job.controller.signal,v=>job.progress=v,profile.name).then(()=>{job.state='succeeded';job.progress=100}).catch(e=>{job.state='failed';job.reason=e?.code==='ENOSPC'?'temporary_storage_full':e.message==='output_limit'?'output_limit':'conversion_failed'}).finally(async()=>{await rm(input,{force:true}).catch(()=>{});job.at=Date.now()});
    json(res,202,{id});dir=undefined;
   }finally{accepting=false;lifecycle.touch();if(dir)await rm(dir,{recursive:true,force:true})}return
  }
  const match=/^\/jobs\/([a-f0-9-]{36})(\/result)?$/.exec(req.url??'');if(!match){json(res,404,{});return}
  const job=jobs.get(match[1]);if(!job){json(res,404,{});return}
  if(req.method==='DELETE'){await remove(match[1]);json(res,200,{});return}
  if(req.method!=='GET'){json(res,405,{});return}
  if(match[2]){if(job.state!=='succeeded'){json(res,409,{});return}const info=await stat(job.output);res.writeHead(200,{'Content-Type':'video/mp4','Content-Length':info.size});await pipeline(createReadStream(job.output),res);return}
  json(res,200,{state:job.state,progress:job.progress,reason:job.reason,quality:profile.name});
 }catch(e){if(!res.headersSent){
  const status=['renderer_memory_pressure','resource_probe_unavailable'].includes(e?.message)?503:e?.code==='ENOSPC'?507:e?.message==='input_limit'?413:400;
  if(!req.complete){res.setHeader('Connection','close');res.once('finish',()=>req.destroy());}
  json(res,status,{error:status===503?e.message:status===507?'temporary_storage_full':status===413?'input_limit':'conversion_request_failed'});
 }else res.destroy()}
});
server.requestTimeout=120000;server.headersTimeout=10000;server.listen(8090,'0.0.0.0');
setInterval(()=>{for(const [id,job] of jobs)if(Date.now()-job.at>(job.state==='running'?4*3600000:600000))void remove(id).catch(()=>{})},60000).unref();
process.on('SIGTERM',async()=>{server.close();await Promise.all([...jobs.keys()].map(remove));process.exit(0)});
