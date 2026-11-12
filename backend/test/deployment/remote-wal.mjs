// Real SSH pgBackRest repository, isolated containers/volumes; no business mounts.
import {execFileSync} from 'node:child_process';
import {mkdirSync,writeFileSync,mkdtempSync,rmSync,readFileSync} from 'node:fs';
import {resolve} from 'node:path';
import assert from 'node:assert/strict';
import {runMaintenance} from '../../../scripts/wal-maintenance.mjs';
const id=`puppeteer-r17-wal-${process.pid}`,image='puppeteer/wal-drill:r17-preview';
const docker=(...args)=>execFileSync('docker',args,{encoding:'utf8',timeout:180000}).trim();
const containers=[],volumes=[];
const evidence={scope:'Docker simulated remote SSH WAL repository',checks:[],passed:false};
const maintenance=process.argv.includes('--maintenance');
let taskDir,webhook,collector;
const pg=(name,script)=>docker('exec','--user','postgres',`${id}-${name}`,'sh','-ec',script);
const sql=(name,q)=>docker('exec','--user','postgres',`${id}-${name}`,'psql','-U','puppeteer','-d','postgres','-At','-v','ON_ERROR_STOP=1','-c',q);
try {
 docker('network','create','--internal',id);
 for(const v of ['keys','repo','source','restore']){docker('volume','create',`${id}-${v}`);volumes.push(`${id}-${v}`)}
 if(maintenance){
  mkdirSync(resolve('backend/.local'),{recursive:true});
  taskDir=mkdtempSync(resolve('backend/.local/r17-maintenance-'));
  const name=`${id}-notifications`;containers.push(name);
  docker('run','-d','--name',name,'-p','127.0.0.1::8080','--read-only','--cap-drop','ALL','node:24.13.0-alpine','node','-e',
   `let reject=false,events=[];require('http').createServer((q,s)=>{s.setHeader('connection','close');if(q.url==='/reject'){reject=true;s.end();return}if(q.url==='/accept'){reject=false;s.end();return}if(q.method==='GET'){s.end(JSON.stringify(events));return}if(reject){s.statusCode=503;s.end();return}let b='';q.on('data',c=>b+=c);q.on('end',()=>{events.push(JSON.parse(b));s.end('ok')})}).listen(8080,'0.0.0.0')`);
  collector=`http://127.0.0.1:${docker('port',name,'8080/tcp').split(':').at(-1)}`;
  webhook=resolve(taskDir,'webhook');writeFileSync(webhook,collector+'/events',{mode:0o600});
 }
 const maintain=operation=>runMaintenance({container:`${id}-source`,directory:resolve(taskDir,'state'),operation,webhookFile:webhook,allowLocalHTTP:true});
 docker('run','--rm','--network','none','-v',`${id}-keys:/fixture`,'--entrypoint','sh',image,'-ec',`
 umask 077
 ssh-keygen -q -t ed25519 -N '' -f /fixture/client
 ssh-keygen -q -t ed25519 -N '' -f /fixture/host
 cp /fixture/client.pub /fixture/authorized_keys
 printf 'repo ' > /fixture/known_hosts; cat /fixture/host.pub >> /fixture/known_hosts
 head -c 48 /dev/urandom | base64 > /fixture/wal_cipher_pass
 printf 'Host repo\n User postgres\n IdentityFile /fixture/client\n UserKnownHostsFile /fixture/known_hosts\n StrictHostKeyChecking yes\n BatchMode yes\n ConnectTimeout 3\n' > /fixture/ssh_config
 chown -R postgres:postgres /fixture
 `);
 function start(name,volume,command) {
  containers.push(`${id}-${name}`);
  return docker('run','-d','--name',`${id}-${name}`,'--network',id,'--network-alias',name,
   '-v',`${id}-keys:/fixture:ro`,'-v',`${id}-keys:/run/secrets:ro`,
   '-v',`${id}-${volume}:${name==='repo'?'/var/lib/pgbackrest':'/var/lib/postgresql'}`,
   '--entrypoint','sh',image,'-ec',command);
 }
 start('repo','repo',`exec /usr/sbin/sshd -D -e -h /fixture/host -o AuthorizedKeysFile=/fixture/authorized_keys -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no -o PermitRootLogin=no -o AllowUsers=postgres`);
 const config=`sed -i '/^process-max=/a repo1-host=repo\\nrepo1-host-user=postgres\\nrepo1-host-cmd=/usr/local/bin/pgbackrest-file\\ncmd-ssh=/usr/local/bin/drill-ssh\\nprotocol-timeout=15\\ndb-timeout=10' /etc/pgbackrest/pgbackrest.conf
 printf '#!/bin/sh\nexec ssh -F /fixture/ssh_config "$@"\n' > /usr/local/bin/drill-ssh
 chmod 755 /usr/local/bin/drill-ssh
 exec sleep infinity`;
 start('source','source',config);
 start('restore','restore',config);
 pg('source',`initdb -D /var/lib/postgresql/18/docker -U puppeteer -A trust >/tmp/init.log
 printf "listen_addresses=''\narchive_mode=on\narchive_timeout=60\narchive_command='pgbackrest-file --stanza=puppeteer archive-push %%p'\n" >> /var/lib/postgresql/18/docker/postgresql.conf
 pg_ctl -D /var/lib/postgresql/18/docker -l /tmp/pg.log -w start`);
 pg('source','pgbackrest-file --stanza=puppeteer stanza-create && pgbackrest-file --stanza=puppeteer check');
 sql('source','CREATE TABLE marker(id text PRIMARY KEY)');
 pg('source','pgbackrest-file --stanza=puppeteer --type=full --start-fast backup');
 sql('source',"INSERT INTO marker VALUES('keep')");
 sql('source',"SELECT pg_create_restore_point('r17_remote_target')");
 sql('source',"INSERT INTO marker VALUES('discard')");
 pg('source','pgbackrest-file --stanza=puppeteer check');
 evidence.checks.push('SSH pinned host key and encrypted remote full/WAL archive');
 if(maintenance){
  for(let tick=0;tick<2;tick++){
   await new Promise(r=>setTimeout(r,1000));
   assert.equal((await maintain('check')).status,'succeeded');
  }
  const events=await (await fetch(collector+'/events')).json();assert.equal(events.length,2);
  const lock=resolve(taskDir,'state/running.lock');mkdirSync(lock);
  await assert.rejects(maintain('check'),/already running/);rmSync(lock,{recursive:true});
  await fetch(collector+'/reject');
  evidence.checks.push('two accelerated timer ticks execute real archive checks and deliver notifications; overlap blocked');
 }
 docker('stop','-t','3',`${id}-repo`);
 const before=Number(sql('source','SELECT failed_count FROM pg_stat_archiver'));
 sql('source',"INSERT INTO marker VALUES('during_outage'); SELECT pg_switch_wal()");
 let failed=false;
 for(let n=0;n<30;n++) {
  if(Number(sql('source','SELECT failed_count FROM pg_stat_archiver'))>before){failed=true;break}
  await new Promise(r=>setTimeout(r,1000));
 }
 assert.ok(failed,'repository outage must surface as archive failure');
 assert.equal(sql('source','SELECT count(*) FROM marker'),'3');
 evidence.checks.push('repository outage detected; source remains writable');
 if(maintenance){
  const result=await maintain('check');assert.equal(result.status,'failed');assert.equal(result.pendingNotifications,1);
  const state=JSON.parse(readFileSync(resolve(taskDir,'state/state.json'),'utf8'));
  assert.notEqual(state.lastSuccess.check,state.lastRun.finished);
  evidence.checks.push('repository failure recorded; webhook 503 retains durable notification; success timestamp unchanged');
 }
 docker('start',`${id}-repo`);
 // Explicit retry drains retained WAL; does not remove files from pg_wal.
 pg('source',`for f in /var/lib/postgresql/18/docker/pg_wal/archive_status/*.ready; do
 test -e "$f" || continue
 wal=$(basename "$f" .ready)
 pgbackrest-file --stanza=puppeteer archive-push "/var/lib/postgresql/18/docker/pg_wal/$wal"
 done
 pgbackrest-file --stanza=puppeteer check
 pgbackrest-file --stanza=puppeteer --type=diff backup`);
 evidence.checks.push('repository restart, WAL retry and differential backup succeed');
 if(maintenance){
  await fetch(collector+'/accept');
  const result=await maintain('diff');assert.equal(result.status,'succeeded');assert.equal(result.pendingNotifications,0);
  const events=await (await fetch(collector+'/events')).json();
  assert.equal(events.length,4);assert.equal(events[2].status,'failed');assert.equal(events[3].status,'succeeded');
  assert.equal(new Set(events.map(e=>e.id)).size,4);
  evidence.checks.push('subsequent worker run retries durable failed notification then sends successful backup event in order');
 }
 docker('stop','-t','3',`${id}-source`);
 const started=Date.now();
 // Explicitly select full: latest diff is later than the requested recovery point.
 const info=JSON.parse(pg('restore','pgbackrest-file --stanza=puppeteer --output=json info'));
 const full=info[0].backup.find(b=>b.type==='full').label;
 pg('restore',`pgbackrest-file --stanza=puppeteer --set=${full} --type=name --target=r17_remote_target --target-action=promote --archive-mode=off restore
 pg_ctl -D /var/lib/postgresql/18/docker -l /tmp/pg.log -w start`);
 let promoted=false;
 for(let n=0;n<30;n++) {
  if(sql('restore','SELECT pg_is_in_recovery()')==='f'){promoted=true;break}
  await new Promise(r=>setTimeout(r,1000));
 }
 assert.ok(promoted);assert.equal(sql('restore','SELECT string_agg(id, chr(44)) FROM marker'),'keep');
 assert.equal(sql('restore','SHOW archive_mode'),'off');
 evidence.recoverySeconds=(Date.now()-started)/1000;
 evidence.checks.push('source offline; separate empty volume restored only from remote repository; target transaction verified');
 evidence.passed=true;
} finally {
 for(const name of containers.reverse()){try{docker('rm','-fv',name)}catch{}}
 for(const name of volumes){try{docker('volume','rm',name)}catch{}}
 try{docker('network','rm',id)}catch{}
 if(taskDir)rmSync(taskDir,{recursive:true,force:true});
 mkdirSync(resolve('backend/.local'),{recursive:true});
 writeFileSync(resolve(`backend/.local/r17-${maintenance?'maintenance':'remote-wal'}-result.json`),JSON.stringify(evidence,null,2)+'\n');
 console.log(JSON.stringify(evidence,null,2));
}
