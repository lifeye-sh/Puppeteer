import {execFileSync} from 'node:child_process';
import {readFile,stat} from 'node:fs/promises';
import {resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const root=fileURLToPath(new URL('../',import.meta.url));
const names=[...new Set(execFileSync('git',['ls-files','-co','--exclude-standard','-z'],{cwd:root,encoding:'utf8'}).split('\0').filter(Boolean))];
const retired=/next[-_ ]?terminal/i;
const failures=[];
for(const name of names){
 const path=resolve(root,name);
 try{if(!(await stat(path)).isFile())continue}catch{continue}
 if(retired.test(name))failures.push(name+': retired path');
 const data=await readFile(path);
 if(data.includes(0))continue;
 if(retired.test(data.toString('utf8')))failures.push(name+': retired product identity');
}
const packageJSON=JSON.parse(await readFile(resolve(root,'package.json'),'utf8'));
if(packageJSON.dependencies['@puppeteer/guacamole-common-js']!=='file:backend/renderer/vendor/guacamole-common-js')failures.push('graphics client must use the reviewed local Apache sources');
const remotes=execFileSync('git',['remote','-v'],{cwd:root,encoding:'utf8'});
if(retired.test(remotes))failures.push('retired Git remote');
if(failures.length){console.error(failures.join('\n'));process.exitCode=1}
else console.log(JSON.stringify({product:'Puppeteer',filesChecked:names.length,retiredReferences:0,gitRemotes:'passed',graphicsSource:'Apache Guacamole'}));
