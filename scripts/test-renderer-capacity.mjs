import {execFileSync} from 'node:child_process';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {resolve} from 'node:path';
import {randomBytes} from 'node:crypto';
const name=`puppeteer-renderer-capacity-${process.pid}`;
const docker=(args,options={})=>execFileSync('docker',args,{encoding:'utf8',timeout:180000,...options}).trim();
try{
 docker(['run','-d','--name',name,'--network','none','--read-only','--cap-drop','ALL',
  '--security-opt','no-new-privileges:true','--memory','2g','--cpus','2','--pids-limit','160',
  '--tmpfs','/tmp:rw,size=256m,mode=1777','-e','NT_MP4_TOKEN='+randomBytes(32).toString('hex'),
  'puppeteer/renderer:r17-capacity-preview']);
 const output=docker(['exec','-i',name,'node','--input-type=module'],{
  input:readFileSync(resolve('backend/test/deployment/renderer-capacity.mjs'),'utf8')});
 const result=JSON.parse(output.split('\n').findLast(line=>line.startsWith('{')));
 result.containerLimits={memoryBytes:2147483648,cpus:2,pids:160,tmpfsMiB:256};
 result.oomKilled=JSON.parse(docker(['inspect',name,'--format','{{.State.OOMKilled}}']));
 if(result.oomKilled)throw Error('container was OOM killed');
 mkdirSync(resolve('backend/.local'),{recursive:true});
 writeFileSync(resolve('backend/.local/r17-renderer-capacity-result.json'),JSON.stringify(result,null,2)+'\n');
 console.log(JSON.stringify(result,null,2));
}finally{try{docker(['rm','-f',name])}catch{}}
