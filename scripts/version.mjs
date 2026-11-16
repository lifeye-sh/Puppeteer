import {releaseVersionPattern,compareReleaseVersions} from './release-version.mjs';
import {readFile,writeFile,readdir,access} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {resolve} from 'node:path';

const root=fileURLToPath(new URL('../',import.meta.url));
const args=process.argv.slice(2);
if(args.length && !(args.length===1&&args[0]==='--check') && !(args.length===2&&args[0]==='--set'))throw Error('Usage: node scripts/version.mjs [--check | --set beta0.9]');
const old=(await readFile(resolve(root,'VERSION'),'utf8')).trim();
const version=args[0]==='--set'?args[1]:old;
if(!releaseVersionPattern.test(version))throw Error('Expected an explicit betaMAJOR.MINOR or r17-rcN version');
const metaPath=resolve(root,'backend/internal/platform/buildmeta/version.go');
const meta=await readFile(metaPath,'utf8');
const schemaVersion=Math.max(...(await readdir(resolve(root,'backend/migrations'))).filter(n=>/^\d+_.+\.sql$/.test(n)).map(n=>Number(n.split('_')[0])));
const capabilitiesRevision=Number(/const CapabilitiesRevision = (\d+)/.exec(meta)?.[1]);
if(!Number.isInteger(schemaVersion)||schemaVersion<1||!Number.isInteger(capabilitiesRevision))throw Error('Invalid schema/capability metadata');
const notes=`deploy/production/${version}.md`;
await access(resolve(root,notes));
const releases=[];
for(const name of await readdir(resolve(root,'deploy/production'))){
 if(!name.startsWith('dockerhub-')||!name.endsWith('.json')||!releaseVersionPattern.test(name.slice(10,-5)))continue;
 const release=JSON.parse(await readFile(resolve(root,'deploy/production',name),'utf8'));
 if(release.product==='Puppeteer'&&release.version&&release.images?.length)releases.push(release);
}
releases.sort((a,b)=>compareReleaseVersions(b.version,a.version));
const published=releases.find(r=>r.version===version);
if(published){
 if(published.schemaVersion!==schemaVersion||published.platform!=='linux/amd64')throw Error('Published image/schema metadata differs');
 const components=new Set();
 for(const entry of published.images){
  if(!['server','web','graphics','renderer'].includes(entry.component)||components.has(entry.component))throw Error('Unexpected image component');
  components.add(entry.component);
  if(!/^sha256:[a-f0-9]{64}$/.test(entry.digest)||entry.pinnedReference!==`cloners/puppeteer-${entry.component}@${entry.digest}`||entry.reference!==`cloners/puppeteer-${entry.component}:${version}`)throw Error('Invalid published image reference');
 }
 if(components.size!==4)throw Error('All four published components are required');
}
const manifest={product:'Puppeteer',version,channel:published?(version.startsWith('beta')?'beta':'release-candidate'):'source-candidate',platform:'linux/amd64',schemaVersion,capabilitiesRevision,containerImagesPublished:Boolean(published),publishedImageVersion:releases[0]?.version??null,releaseNotes:notes};
const manifestPath=resolve(root,'deploy/production/source-release.json');
if(args[0]==='--set'){
 await writeFile(resolve(root,'VERSION'),version+'\n');
 await writeFile(metaPath,meta.replace(/const Release = "[^"]+"/,`const Release = "${version}"`));
 await writeFile(manifestPath,JSON.stringify(manifest,null,2)+'\n');
}else{
 if(!meta.includes(`const Release = "${version}"`))throw Error('Backend release differs from VERSION');
 const actual=JSON.parse(await readFile(manifestPath,'utf8'));
 if(JSON.stringify(actual)!==JSON.stringify(manifest))throw Error('Source release metadata is stale; prepare the release notes, then run --set');
}
console.log(JSON.stringify({...manifest,checked:true,action:args[0]==='--set'?'prepared':'verified'}));
