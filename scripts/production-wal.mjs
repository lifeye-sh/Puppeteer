import {readFile,writeFile,chown} from 'node:fs/promises';
import {randomBytes} from 'node:crypto';
import {resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

export async function addWAL(directory) {
 const dir=resolve(directory),envPath=resolve(dir,'production.env');
 if(process.platform==='linux' && process.getuid()!==0) throw Error('Run as root to assign PostgreSQL secret ownership.');
 const env=await readFile(envPath,'utf8'),file=resolve(dir,'secrets/wal_cipher_pass');
 try {
  await writeFile(file,randomBytes(32).toString('hex')+'\n',{mode:0o400,flag:'wx'});
  if(process.platform==='linux') await chown(file,70,70);
 }catch(e){if(e.code!=='EEXIST')throw e;}
 if(!/^[0-9a-f]{64}$/.test((await readFile(file,'utf8')).trim())) throw Error('Existing WAL key is invalid; it was not replaced.');
 const added=!/^PUPPETEER_POSTGRES_WAL_IMAGE=/m.test(env);
 if(added) await writeFile(envPath,env.trimEnd()+'\nPUPPETEER_POSTGRES_WAL_IMAGE=puppeteer/postgres:r17-wal-preview\n',{mode:0o600});
 return {addedSettings:added?1:0,secret:'preserved-or-created'};
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
 if(!process.argv[2])throw Error('Usage: node scripts/production-wal.mjs CONFIG_DIRECTORY');
 console.log(JSON.stringify(await addWAL(process.argv[2])));
}
