import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,open,writeFile,readFile,rm,mkdir,stat} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {Readable} from 'node:stream';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {inspectArchive} from './archive.mjs';
import {receiveArchive} from './upload.mjs';
const exec=promisify(execFile);
const header=n=>({type:'header',format:'terminal-events-v1',durationMs:500,events:n});
const event=(sequence=1,payload='eA==')=>({kind:'output',payload,sequence,elapsedMs:0});
const wire=values=>values.map(JSON.stringify).join('\n')+'\n';
async function directory(t){const dir=await mkdtemp(join(tmpdir(),'renderer-stream-'));t.after(()=>rm(dir,{recursive:true,force:true}));return dir;}

test('85 MiB archive validates with a 32 MiB JavaScript heap',async t=>{
 const dir=await directory(t),input=join(dir,'input.jsonl');
 const file=await open(input,'wx');
 try{
  await file.writeFile(JSON.stringify(header(1024))+'\n');
  const payload=Buffer.alloc(64*1024,120).toString('base64');
  for(let n=1;n<=1024;n++)await file.writeFile(JSON.stringify(event(n,payload))+'\n');
  await file.writeFile(JSON.stringify({type:'end',events:1024})+'\n');
 }finally{await file.close()}
 const {stdout}=await exec(process.execPath,['--max-old-space-size=32','--input-type=module','-e',
  `import {inspectArchive} from ${JSON.stringify(new URL('./archive.mjs',import.meta.url).href)};const h=await inspectArchive(process.argv[1]);console.log(JSON.stringify({events:h.events,rss:process.memoryUsage().rss}));`,input],{timeout:60000});
 assert.equal(JSON.parse(stdout).events,1024);
 t.diagnostic(`archive bytes=${(await stat(input)).size}; child RSS=${JSON.parse(stdout).rss}`);
});

test('streamed validation rejects truncation, extra records and invalid ordering',async t=>{
 const dir=await directory(t),input=join(dir,'bad.jsonl');
 for(const values of [[header(1),event()],[header(1),event(),{type:'end',events:1},event(2)],
  [header(1),event(2),{type:'end',events:1}],[header(1),event(1,'x==='),{type:'end',events:1}],
  [header(1),{...event(),elapsedMs:501},{type:'end',events:1}],[null]]){
  await writeFile(input,wire(values));await assert.rejects(inspectArchive(input));
 }
 await writeFile(input,wire([header(1),event(),{type:'end',events:1}]));
 await assert.rejects(inspectArchive(input,AbortSignal.abort()),{name:'AbortError'});
});

test('large individual event validates without a recursive base64 regexp',async t=>{
 const dir=await directory(t),input=join(dir,'large.jsonl');
 await writeFile(input,wire([header(1),event(1,Buffer.alloc(8*1024*1024).toString('base64')),{type:'end',events:1}]));
 assert.equal((await inspectArchive(input)).events,1);
});

test('upload streams fragments, refuses overwrite and enforces length without destroying request',async t=>{
 const dir=await directory(t),input=join(dir,'input');
 const req=Readable.from([Buffer.from('abc'),Buffer.from('def')]);
 assert.equal(await receiveArchive(req,input),6);assert.equal(await readFile(input,'utf8'),'abcdef');
 await assert.rejects(receiveArchive(Readable.from(['changed']),input),{code:'EEXIST'});
 const large=Readable.from([Buffer.alloc(4),Buffer.alloc(4)],{autoDestroy:false});
 await assert.rejects(receiveArchive(large,join(dir,'large'),6),/input_limit/);
 assert.equal(large.destroyed,false);large.destroy();
});

test('stale scratch files survive startup and count against quota',async t=>{
 const dir=await directory(t),job=join(dir,'mp4-job-stale');await mkdir(job);
 const path=join(job,'preserved.mp4'),file=await open(path,'wx');
 try{await file.truncate(400*1024*1024)}finally{await file.close()}
 const script=`const s=await import(${JSON.stringify(new URL('./scratch.mjs',import.meta.url).href)});await s.initializeScratch();try{await s.admitScratch();process.exitCode=2}catch(e){if(e.code!=='ENOSPC')throw e;console.log(e.message)}`;
 const {stdout}=await exec(process.execPath,['--input-type=module','-e',script],{env:{...process.env,NT_MP4_SCRATCH_DIRECTORY:dir,NT_MP4_SCRATCH_QUOTA_MIB:'1024'}});
 assert.match(stdout,/temporary_storage_full/);assert.equal((await stat(path)).size,400*1024*1024);
});
