import {execFileSync} from 'node:child_process';
import {readFileSync,mkdirSync,writeFileSync,mkdtempSync,rmSync} from 'node:fs';
import {resolve} from 'node:path';
import {randomBytes} from 'node:crypto';
const name=`puppeteer-browser-${process.pid}`,containers=[];
mkdirSync(resolve('backend/.local'),{recursive:true});
const tls=mkdtempSync(resolve('backend/.local/r17-browser-tls-'));
const docker=(args,options={})=>execFileSync('docker',args,{encoding:'utf8',timeout:180000,...options}).trim();
try{
 docker(['run','--rm','--network','none','--user','0','-v',tls+':/tls','--entrypoint','sh','puppeteer/browser-drill:r17-preview','-ec',
 `umask 077; openssl req -x509 -newkey rsa:2048 -nodes -keyout /tls/ca.key -out /tls/ca.crt -days 2 -subj /CN=R17-Isolated-CA -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign,cRLSign 2>/dev/null
 openssl req -newkey rsa:2048 -nodes -keyout /tls/server.key -out /tls/server.csr -subj /CN=localhost 2>/dev/null
 printf 'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:localhost,IP:127.0.0.1\n' > /tls/extensions
 openssl x509 -req -in /tls/server.csr -CA /tls/ca.crt -CAkey /tls/ca.key -CAcreateserial -out /tls/server.crt -days 2 -extfile /tls/extensions 2>/dev/null
 chown 101:101 /tls/server.key; chmod 400 /tls/server.key; chmod 444 /tls/server.crt /tls/ca.crt`]);
 docker(['network','create','--internal',name]);
 const subnet=JSON.parse(docker(['network','inspect',name]))[0].IPAM.Config[0].Subnet;
 const proxy=subnet.split('/')[0].split('.').slice(0,3).join('.')+'.10';
 const token=randomBytes(32).toString('hex');
 containers.push(name+'-renderer');docker(['run','-d','--name',name+'-renderer','--network',name,'--network-alias','renderer','--memory','2g','--cpus','2','--pids-limit','160','--read-only','--tmpfs','/tmp:rw,size=768m,mode=1777','-e','NT_MP4_TOKEN='+token,'puppeteer/renderer:r17-capacity-preview']);
 containers.push(name+'-server');
 docker(['create','--name',name+'-server','--network',name,'--network-alias','server',
 '-v',resolve('backend/.local/r17-browser.test')+':/fixture.test:ro',
 '-e','NT_R17_BROWSER_FIXTURE=1','-e','NT_R17_PROXY_IP='+proxy,'-e','NT_MP4_TEST_TOKEN='+token,'-e','NT_TEST_DATABASE_URL=postgres://postgres:r16-disposable-fixture-only@r16-postgres-fixture:5432/nt_r16_test?sslmode=disable',
 '--entrypoint','/fixture.test','puppeteer/server:r17-final-preview','-test.run','^TestR17BrowserFixture$','-test.v','-test.timeout=12m']);
 docker(['network','connect','nt-backend-r01_default',name+'-server']);docker(['start',name+'-server']);
 for(let n=0;n<60;n++){if(docker(['logs',name+'-server']).includes('browser fixture ready'))break;if(n===59)throw Error('fixture startup timeout');await new Promise(r=>setTimeout(r,500))}
 containers.push(name+'-web');
 docker(['run','-d','--name',name+'-web','--network',name,'--ip',proxy,'--read-only','--tmpfs','/tmp:rw,size=128m,mode=1777',
 '-v',resolve('deploy/production/nginx.conf')+':/etc/nginx/nginx.conf:ro','-v',tls+'/server.crt:/run/tls/server.crt:ro','-v',tls+'/server.key:/run/tls/server.key:ro','puppeteer/web:r17-core-preview']);
 mkdirSync(resolve('backend/.local/r17-browser-evidence'),{recursive:true});
 const script=readFileSync(resolve('backend/test/deployment/browser-journey.mjs'),'utf8');
 const result=docker(['run','--rm','-i','--network','container:'+name+'-web','--memory','2g','--cpus','2','--tmpfs','/tmp:rw,size=512m,mode=1777',
 '-v',tls+'/ca.crt:/ca.crt:ro','-v',resolve('backend/.local/r17-browser-evidence')+':/evidence',
 '--entrypoint','sh','puppeteer/browser-drill:r17-preview','-ec',
 'mkdir -p /tmp/browser-home/.pki/nssdb; certutil -N --empty-password -d sql:/tmp/browser-home/.pki/nssdb; certutil -A -d sql:/tmp/browser-home/.pki/nssdb -n r17-test-ca -t "C,," -i /ca.crt; exec node --input-type=module'],{input:script});
 writeFileSync(resolve('backend/.local/r17-browser-result.json'),result+'\n');console.log(result);
 const audit=JSON.parse(docker(['exec',name+'-server','wget','-qO-','http://127.0.0.1:8089/api/__fixture/audit']));
 if(audit.closedSessionsWithClientIP<1)throw Error('proxy client IP not preserved in session audit');
 console.log(JSON.stringify({proxyAuditVerified:true}));
}finally{
 try{docker(['stop','-t','15',name+'-server'])}catch{}
 for(const c of containers.reverse()){try{docker(['rm','-fv',c])}catch{}}
 try{docker(['network','rm',name])}catch{}
 rmSync(tls,{recursive:true,force:true});
}
