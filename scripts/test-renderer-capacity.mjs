import {execFileSync} from 'node:child_process';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {resolve} from 'node:path';
import {randomBytes} from 'node:crypto';
const name=`puppeteer-renderer-capacity-${process.pid}`;
const docker=(args,options={})=>execFileSync('docker',args,{encoding:'utf8',timeout:180000,...options}).trim();
try{
 docker(['run','-d','--name',name,'--network','none','--read-only','--cap-drop','ALL',
  '--security-opt','no-new-privileges:true','--memory','192m','--memory-swap','192m','--cpus','2','--pids-limit','160',
  '--tmpfs','/tmp:rw,size=256m,mode=1777','-e','NT_MP4_TOKEN='+randomBytes(32).toString('hex'),
  process.env.PUPPETEER_TEST_RENDERER_IMAGE||'puppeteer/renderer:r17-capacity-preview']);
 // Verify real cgroup pressure without allocating memory to exhaustion.
 docker(['exec','-i',name,'node','--input-type=module'],{input:`
 import assert from 'node:assert/strict';
 const endpoint='http://127.0.0.1:8090';
 for(let n=0;n<40;n++){try{if((await fetch(endpoint+'/healthz')).ok)break}catch{}await new Promise(r=>setTimeout(r,250))}
 const headers={Authorization:'Bearer '+process.env.NT_MP4_TOKEN};
 for(const [path,method] of [['/runtime/admission','GET'],['/jobs','POST']]){
  const r=await fetch(endpoint+path,{method,headers});assert.equal(r.status,503);assert.equal((await r.json()).error,'renderer_memory_pressure');
 }
 assert.equal((await fetch(endpoint+'/healthz')).status,200);
 `});
 docker(['update','--memory','2g','--memory-swap','2g',name]);
 const output=docker(['exec','-i',name,'node','--input-type=module'],{
  input:readFileSync(resolve('backend/test/deployment/renderer-capacity.mjs'),'utf8')});
 const result=JSON.parse(output.split('\n').findLast(line=>line.startsWith('{')));
 result.memoryAdmission="192 MiB defers; 2 GiB resumes without restart";
 result.containerLimits={memoryBytes:2147483648,cpus:2,pids:160,tmpfsMiB:256};
 result.oomKilled=JSON.parse(docker(['inspect',name,'--format','{{.State.OOMKilled}}']));
 if(result.oomKilled)throw Error('container was OOM killed');
 mkdirSync(resolve('backend/.local'),{recursive:true});
 writeFileSync(resolve('backend/.local/r17-renderer-capacity-result.json'),JSON.stringify(result,null,2)+'\n');
 console.log(JSON.stringify(result,null,2));
}finally{try{docker(['rm','-f',name])}catch{}}
