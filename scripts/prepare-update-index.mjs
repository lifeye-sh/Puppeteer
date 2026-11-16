// Prepare a reviewable release catalog. Signing is a separate offline step.
import {readFile,writeFile,mkdir} from 'node:fs/promises';
import {dirname,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
const args=process.argv.slice(2),options={};
for(let i=0;i<args.length;i+=2){
 if(!['--version','--min-source','--sequence','--published-at','--output'].includes(args[i])||!args[i+1]||options[args[i]])throw Error('Usage: node scripts/prepare-update-index.mjs --version beta0.10 --min-source beta0.9 --sequence 1 --published-at ISO_DATE --output catalog.json');
 options[args[i]]=args[i+1];
}
const version=options['--version'],minimum=options['--min-source'],sequence=Number(options['--sequence']);
const pattern=/^(?:beta(?:0|[1-9]\d{0,8})\.(?:0|[1-9]\d{0,8})|r17-rc[1-9]\d{0,8}|v(?:0|[1-9]\d{0,8})\.(?:0|[1-9]\d{0,8})\.(?:0|[1-9]\d{0,8}))$/;
if(!pattern.test(version??'')||!pattern.test(minimum??'')||!Number.isSafeInteger(sequence)||sequence<1||!options['--output'])throw Error('Explicit versions, positive sequence and output path required');
const manifest=JSON.parse(await readFile(resolve(root,`deploy/production/dockerhub-${version}.json`),'utf8'));
if(manifest.product!=='Puppeteer'||manifest.version!==version||manifest.platform!=='linux/amd64'||manifest.images?.length!==4)throw Error('Invalid published release manifest');
const now=new Date(),expiry=new Date(now.getTime()+30*86400000);
const published=new Date(options['--published-at']??'');
if(!Number.isFinite(published.getTime())||published>now)throw Error('Verified release publication time is required');
const catalog={product:'Puppeteer',format:1,sequence,issuedAt:now.toISOString(),expiresAt:expiry.toISOString(),releases:[{
 version,channel:version.startsWith('v')?'stable':'beta',publishedAt:published.toISOString(),platform:manifest.platform,minSource:minimum,
 schemaVersion:manifest.schemaVersion,capabilitiesRevision:manifest.capabilitiesRevision,
 notesUrl:`https://github.com/lifeye-sh/Puppeteer/releases/tag/${version}`,
 images:manifest.images.map(({component,digest,pinnedReference})=>({component,digest,pinnedReference})),
}]};
const output=resolve(options['--output']);await mkdir(dirname(output),{recursive:true});await writeFile(output,JSON.stringify(catalog,null,2)+'\n');
console.log(`Prepared unsigned catalog ${sequence}; review publication date and compatibility before signing. This command includes only ${version}; merge retained channel releases before signing when needed.`);
