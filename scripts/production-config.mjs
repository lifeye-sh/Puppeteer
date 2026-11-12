import {mkdir,writeFile,chown} from 'node:fs/promises';
import {randomBytes} from 'node:crypto';
import {resolve} from 'node:path';
import {addServices} from './production-addons.mjs';

const [directory,origin,port='8443']=process.argv.slice(2);
if(!directory||!origin) throw Error('Usage: node scripts/production-config.mjs DIRECTORY HTTPS_ORIGIN [HOST_PORT]');
const url=new URL(origin);
if(url.protocol!=='https:'||url.origin!==origin||url.username||url.password||!/^[a-zA-Z0-9.:[\]-]+$/.test(url.host)) throw Error('Provide a clean HTTPS origin');
if(!/^\d+$/.test(port)||Number(port)<1||Number(port)>65535) throw Error('Invalid host port');
const target=resolve(directory);
if(process.platform==='linux' && process.getuid()!==0) throw Error('Run configuration initialization as root to assign private secret ownership to service UIDs.');
if(/[\r\n$#"']/.test(target)) throw Error('Unsupported configuration directory');
// Exclusive creation: a retry must never replace keys of an existing database.
await mkdir(target,{mode:0o700});
await mkdir(resolve(target,'secrets'),{mode:0o700});
await mkdir(resolve(target,'tls'),{mode:0o700});
const password=randomBytes(32).toString('hex');
const secrets={database_password:password,database_url:`postgres://puppeteer:${password}@postgres:5432/puppeteer?sslmode=disable`,mfa_key:randomBytes(32).toString('hex'),vault_key:randomBytes(32).toString('hex')};
for(const [name,value] of Object.entries(secrets)) {
 const path=resolve(target,'secrets',name);
 await writeFile(path,value+'\n',{mode:0o400,flag:'wx'});
 if(process.platform==='linux') {
  const uid=name==='database_password'?70:10001;
  await chown(path,uid,uid);
 }
}
if(process.platform==='linux') await chown(resolve(target,'tls'),101,101);
await writeFile(resolve(target,'production.env'),[
 'PUPPETEER_SERVER_IMAGE=puppeteer/server:r17-final-preview',
 'PUPPETEER_WEB_IMAGE=puppeteer/web:r17-core-preview',
 `PUPPETEER_CONFIG_DIR=${target.replaceAll('\\','/')}`,
 `PUPPETEER_ORIGIN=${origin}`,
 'PUPPETEER_BIND=127.0.0.1',
 `PUPPETEER_HTTPS_PORT=${port}`,
 '',
].join('\n'),{mode:0o600,flag:'wx'});
console.log('Created private configuration. Install tls/server.crt and tls/server.key before starting. Existing configuration is never overwritten.');
await addServices(target);
