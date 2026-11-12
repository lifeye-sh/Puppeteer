import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {resolve,dirname,relative,isAbsolute} from 'node:path';
import {fileURLToPath} from 'node:url';

const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const file=resolve(process.argv[2]??resolve(root,'deploy/production/r17-release-manifest.json'));
const manifest=JSON.parse(await readFile(file,'utf8'));
if(manifest.format!==1||manifest.product!=='Puppeteer'||!Array.isArray(manifest.images)||!manifest.images.length||!Array.isArray(manifest.files)||!manifest.files.length)throw Error('Invalid release manifest');
for(const item of manifest.files){
 const path=resolve(root,item.path),local=relative(root,path);
 if(isAbsolute(local)||local==='..'||local.startsWith('..\\')||local.startsWith('../'))throw Error('Release path escapes project');
 const digest=createHash('sha256').update(await readFile(path)).digest('hex');
 if(digest!==item.sha256)throw Error('Release file changed: '+item.path);
}
for(const item of manifest.images){
 if(typeof item.tag!=='string'||item.tag.startsWith('-')||!/^sha256:[0-9a-f]{64}$/.test(item.imageId))throw Error('Invalid image identity');
 const id=execFileSync('docker',['image','inspect',item.tag,'--format','{{.Id}}'],{encoding:'utf8',timeout:30000}).trim();
 if(id!==item.imageId)throw Error('Release image changed: '+item.tag);
}
console.log(JSON.stringify({verified:true,product:manifest.product,round:manifest.round,images:manifest.images.length,files:manifest.files.length}));
