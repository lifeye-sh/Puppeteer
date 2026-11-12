import {readFile,writeFile,chown} from 'node:fs/promises';
import {randomBytes} from 'node:crypto';
import {resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

export async function addServices(directory) {
 const target=resolve(directory),envPath=resolve(target,'production.env');
 if(process.platform==='linux' && process.getuid()!==0) throw Error('Run as root to assign private secret ownership.');
 const env=await readFile(envPath,'utf8');
 const value=/^PUPPETEER_ORIGIN=(.+)$/m.exec(env)?.[1].trim();
 const host=new URL(value).hostname.replace(/^\[|\]$/g,'');
 const file=resolve(target,'secrets/mp4_token');
 try {
  await writeFile(file,randomBytes(32).toString('hex')+'\n',{mode:0o400,flag:'wx'});
  if(process.platform==='linux') await chown(file,10001,10001);
 }catch(error){if(error.code!=='EEXIST')throw error;}
 if(!/^[0-9a-f]{64}$/.test((await readFile(file,'utf8')).trim())) throw Error('Existing renderer secret is invalid; it was not replaced.');
 const defaults={PUPPETEER_GRAPHICS_IMAGE:'puppeteer/graphics:r17-services-preview',PUPPETEER_RENDERER_IMAGE:'puppeteer/renderer:r17-capacity-preview',PUPPETEER_PROTOCOL_HOST:host,PUPPETEER_PROTOCOL_BIND:'127.0.0.1'};
 const added=Object.entries(defaults).filter(([key])=>!new RegExp('^'+key+'=','m').test(env)).map(([key,value])=>key+'='+value);
 if(added.length) await writeFile(envPath,env.trimEnd()+'\n'+added.join('\n')+'\n',{mode:0o600});
 return {addedSettings:added.length,secret:'preserved-or-created'};
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
 if(!process.argv[2]) throw Error('Usage: node scripts/production-addons.mjs CONFIG_DIRECTORY');
 console.log(JSON.stringify(await addServices(process.argv[2])));
}
