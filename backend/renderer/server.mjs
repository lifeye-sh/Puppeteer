import http from 'node:http';
import {randomUUID,timingSafeEqual} from 'node:crypto';
import {mkdtemp,writeFile,rm,stat} from 'node:fs/promises';
import {createReadStream} from 'node:fs';
import {pipeline} from 'node:stream/promises';
import {convert} from './convert.mjs';
import {rendererToken} from './secrets.mjs';
const token=rendererToken();
const jobs=new Map();let accepting=false;
function json(res,status,value){res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value))}
async function remove(id){const job=jobs.get(id);if(!job)return;job.controller.abort();await job.done;await rm(job.dir,{recursive:true,force:true});jobs.delete(id)}
const server=http.createServer(async(req,res)=>{
 try{
  if(req.url==='/healthz'&&req.method==='GET'){json(res,200,{status:'ready'});return}
  const supplied=Buffer.from(req.headers.authorization??''),expected=Buffer.from('Bearer '+token);
  if(supplied.length!==expected.length||!timingSafeEqual(supplied,expected)){json(res,401,{error:'unauthorized'});return}
  if(req.url==='/jobs'&&req.method==='POST'){
   if(Number(req.headers['content-length'])>150*1024*1024){json(res,413,{error:'input_limit'});return}
   if(accepting||[...jobs.values()].some(j=>j.state==='running')||jobs.size>=4){json(res,429,{error:'busy'});return}
   accepting=true;let dir;
   try{
    dir=await mkdtemp('/tmp/mp4-');let size=0;const chunks=[];for await(const chunk of req){size+=chunk.length;if(size>150*1024*1024)throw Error('input_limit');chunks.push(chunk)}
    const id=randomUUID(),input=dir+'/input.jsonl',output=dir+'/output.mp4';await writeFile(input,Buffer.concat(chunks),{mode:0o600});
    const job={dir,output,controller:new AbortController(),state:'running',progress:0,reason:'',at:Date.now()};jobs.set(id,job);
    job.done=convert(input,output,job.controller.signal,v=>job.progress=v).then(()=>{job.state='succeeded';job.progress=100}).catch(e=>{job.state='failed';job.reason=e.message==='output_limit'?'output_limit':'conversion_failed'}).finally(()=>job.at=Date.now());
    json(res,202,{id});dir=undefined;
   }finally{accepting=false;if(dir)await rm(dir,{recursive:true,force:true})}return
  }
  const match=/^\/jobs\/([a-f0-9-]{36})(\/result)?$/.exec(req.url??'');if(!match){json(res,404,{});return}
  const job=jobs.get(match[1]);if(!job){json(res,404,{});return}
  if(req.method==='DELETE'){await remove(match[1]);json(res,200,{});return}
  if(req.method!=='GET'){json(res,405,{});return}
  if(match[2]){if(job.state!=='succeeded'){json(res,409,{});return}const info=await stat(job.output);res.writeHead(200,{'Content-Type':'video/mp4','Content-Length':info.size});await pipeline(createReadStream(job.output),res);return}
  json(res,200,{state:job.state,progress:job.progress,reason:job.reason});
 }catch(e){if(!res.headersSent){
  const status=e?.code==='ENOSPC'?507:e?.message==='input_limit'?413:400;
  json(res,status,{error:status===507?'temporary_storage_full':status===413?'input_limit':'conversion_request_failed'});
 }else res.destroy()}
});
server.requestTimeout=120000;server.headersTimeout=10000;server.listen(8090,'0.0.0.0');
setInterval(()=>{for(const [id,job] of jobs)if(Date.now()-job.at>(job.state==='running'?4*3600000:600000))void remove(id)},60000).unref();
process.on('SIGTERM',async()=>{server.close();await Promise.all([...jobs.keys()].map(remove));process.exit(0)});
