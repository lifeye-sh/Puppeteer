import {readFile,writeFile,readdir,access} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {resolve} from 'node:path';

const root=fileURLToPath(new URL('../',import.meta.url));
const args=process.argv.slice(2);
if(args.length && !(args.length===1&&args[0]==='--check') && !(args.length===2&&args[0]==='--set'))throw Error('Usage: node scripts/version.mjs [--check | --set r17-rcN]');
const old=(await readFile(resolve(root,'VERSION'),'utf8')).trim();
const version=args[0]==='--set'?args[1]:old;
if(!/^r17-rc[1-9]\d*$/.test(version))throw Error('Expected an explicit r17-rcN source version');
const metaPath=resolve(root,'backend/internal/platform/buildmeta/version.go');
const meta=await readFile(metaPath,'utf8');
const schemaVersion=Math.max(...(await readdir(resolve(root,'backend/migrations'))).filter(n=>/^\d+_.+\.sql$/.test(n)).map(n=>Number(n.split('_')[0])));
const capabilitiesRevision=Number(/const CapabilitiesRevision = (\d+)/.exec(meta)?.[1]);
if(!Number.isInteger(schemaVersion)||schemaVersion<1||!Number.isInteger(capabilitiesRevision))throw Error('Invalid schema/capability metadata');
const notes=`deploy/production/${version}.md`;
await access(resolve(root,notes));
const manifest={product:'Puppeteer',version,channel:'source-candidate',platform:'linux/amd64',schemaVersion,capabilitiesRevision,containerImagesPublished:false,publishedImageVersion:'r17-rc11',releaseNotes:notes};
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
