import {readFile,stat,statfs,lstat,readdir} from 'node:fs/promises';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {X509Certificate,createPrivateKey,createPublicKey} from 'node:crypto';
import {isIP} from 'node:net';
import {spawnSync} from 'node:child_process';

const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const envFile=process.argv[2];
const addons=process.argv.includes('--services');
const protocols=process.argv.includes('--protocols');
const agents=process.argv.includes('--agents');
const wal=process.argv.includes('--wal');
if(!envFile) throw Error('Usage: node scripts/production-preflight.mjs /absolute/path/production.env [--services] [--protocols] [--agents] [--wal]');
function docker(args) {
 const r=spawnSync('docker',args,{cwd:root,encoding:'utf8'});
 if(r.error||r.status!==0) throw Error('Docker check failed: '+args.slice(0,2).join(' '));
 return r.stdout;
}
try {
 const files=['-f','deploy/production/compose.yaml',...(addons?['-f','deploy/production/services.yaml']:[]),...(protocols?['-f','deploy/production/protocols.yaml']:[]),...(agents?['-f','deploy/production/agents.yaml']:[]),...(wal?['-f','deploy/production/wal.yaml']:[])];
 const model=JSON.parse(docker(['compose','--env-file',resolve(envFile),'-p','puppeteer-preflight',...files,'config','--format','json']));
 const origin=new URL(model.services.server.environment.NT_ALLOWED_ORIGINS);
 if(origin.protocol!=='https:') throw Error('HTTPS origin required');
 const tls=model.services.web.volumes.find(v=>v.target==='/run/tls').source;
 const cert=new X509Certificate(await readFile(resolve(tls,'server.crt')));
 const key=createPublicKey(createPrivateKey(await readFile(resolve(tls,'server.key'))));
 if(!cert.publicKey.equals(key)) throw Error('TLS certificate and key do not match');
 const host=origin.hostname.replace(/^\[|\]$/g,'');
 if(!(isIP(host)?cert.checkIP(host):cert.checkHost(host))) throw Error('TLS certificate does not cover public hostname');
 if(Date.parse(cert.validFrom)>Date.now()||Date.parse(cert.validTo)<Date.now()+86400000) throw Error('TLS certificate is not currently valid for at least 24 hours');
 const values={};
 for(const [name,secret] of Object.entries(model.secrets)) {
  const info=await stat(secret.file);
  if(!info.isFile()) throw Error('Secret is not a regular file: '+name);
  if(process.platform==='linux' && (info.mode & 0o077)!==0) throw Error('Secret must be owner-only: '+name);
  const expected=['database_password','wal_cipher_pass'].includes(name)?70:10001;
  if(process.platform==='linux' && info.uid!==expected) throw Error('Secret ownership does not match service UID: '+name);
  values[name]=(await readFile(secret.file,'utf8')).trim();
 }
 if(!/^[0-9a-f]{64}$/.test(values.mfa_key)||!/^[0-9a-f]{64}$/.test(values.vault_key)||values.mfa_key===values.vault_key) throw Error('Invalid or reused encryption keys');
 if(wal && (!/^[0-9a-f]{64}$/.test(values.wal_cipher_pass)||[values.mfa_key,values.vault_key,values.mp4_token].includes(values.wal_cipher_pass))) throw Error('Invalid or reused WAL key');
 const dsn=new URL(values.database_url);
 if(decodeURIComponent(dsn.password)!==values.database_password || dsn.hostname!=='postgres' || dsn.searchParams.get('sslmode')!=='disable') throw Error('Database secret mismatch');
 if(process.platform==='linux') {
  const info=await stat(resolve(tls,'server.key'));
  if((info.mode&0o077)!==0||info.uid!==101) throw Error('TLS private key must be owner-only and owned by nginx UID 101');
 }
 for(const service of Object.values(model.services)) {
  if(!service.image || /:latest$/.test(service.image)) throw Error('Versioned images required');
  docker(['image','inspect',service.image,'--format','{{.Id}}']);
 }
 if(model.services.postgres.ports?.length) throw Error('Database must not expose host ports');
 const allowedPorts=protocols?[2022,13306,15432,18443]:[];
 if((model.services.server.ports??[]).some(p=>!allowedPorts.includes(p.target))) throw Error('Unexpected API/server host port exposure');
 if(addons){
  if(!/^[0-9a-f]{64}$/.test(values.mp4_token)) throw Error('Invalid renderer token');
  for(const name of ['guacd','mp4-renderer']) if(model.services[name].ports?.length) throw Error('Private data service must not publish ports');
  docker(['run','--rm','--cap-drop=ALL','--device=/dev/fuse','--entrypoint','sh',model.services.server.image,'-c','test -c /dev/fuse']);
 }
 if(agents){
  const repo=model.services.web.volumes.find(v=>v.target==='/srv/agent-updates')?.source;
  if(!repo) throw Error('Agent public repository mount missing');
  const entries=await readdir(repo);
  const roots=entries.filter(n=>/^[1-9][0-9]{0,3}\.root\.json$/.test(n)).sort((a,b)=>parseInt(a)-parseInt(b));
  if(!roots.length||roots.length>1024||roots.some((n,i)=>n!==`${i+1}.root.json`))throw Error('Agent root chain is not contiguous');
  const expected=[...roots,'snapshot.json','targets','targets.json','timestamp.json'];
  if(JSON.stringify((await readdir(repo)).sort())!==JSON.stringify(expected.sort())) throw Error('Agent public directory contains unexpected or missing files');
  if(JSON.stringify(await readdir(resolve(repo,'targets')))!==JSON.stringify(['nt-agent-linux-amd64'])) throw Error('Unexpected Agent target files');
  for(const name of [...expected,'targets/nt-agent-linux-amd64']) {
   const info=await lstat(resolve(repo,name));if(info.isSymbolicLink()) throw Error('Agent public symlink rejected');
  }
  for(const name of [roots.at(-1),'snapshot.json','targets.json','timestamp.json']) {
   const metadata=JSON.parse(await readFile(resolve(repo,name),'utf8'));
   if(Date.parse(metadata.signed?.expires)<=Date.now()||!Number.isFinite(Date.parse(metadata.signed?.expires))) throw Error('Agent metadata expired or invalid');
  }
 }
 const disk=await statfs(dirname(resolve(envFile)));
 if(disk.bavail*disk.bsize<5*1024**3) throw Error('Less than 5 GiB available on configuration filesystem');
 const engine=JSON.parse(docker(['info','--format','{{json .}}']));
 if(engine.OSType!=='linux'||engine.NCPU<2||engine.MemTotal<4*1024**3) throw Error('Linux Docker with 2 CPUs and 4 GiB RAM required');
 console.log(JSON.stringify({status:'passed',scope:addons?'extended-deployment-preflight':'core-deployment-preflight',protocolsEnabled:protocols,agentRepositoryEnabled:agents,walConfigured:wal,origin:origin.origin,certificateExpires:cert.validTo,selfIssued:cert.subject===cert.issuer,services:Object.keys(model.services),note:'Use nt-agent-release --export for signature validation. Certificate chain trust, Docker volume capacity and full recovery acceptance remain separate release gates.'},null,2));
} catch(error) {console.error(error.message);process.exitCode=1;}
