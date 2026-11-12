// Exercise the real production Nginx configuration against an isolated echo upstream.
// Usage: node backend/test/deployment/proxy-headers.mjs PATH_TO_TEST_TLS_DIRECTORY
import {execFileSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
import {resolve} from 'node:path';
import https from 'node:https';
import assert from 'node:assert/strict';
const tls = resolve(process.argv[2] || 'backend/.local/r17-core/tls');
const ca = readFileSync(resolve(tls, 'server.crt'));
const name = `puppeteer-proxy-test-${process.pid}`;
const docker = (...args) => execFileSync('docker', args, {encoding:'utf8', timeout:60000}).trim();
const echo = `require('http').createServer((r,s)=>{s.setHeader('content-type','application/json');s.end(JSON.stringify(r.headers))}).listen(8089,'0.0.0.0')`;
try {
  docker('network','create','--internal',name);
  docker('network','create',`${name}-edge`);
  docker('run','-d','--name',`${name}-echo`,'--network',name,'--network-alias','server',
    '--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true',
    'node:24.13.0-alpine','node','-e',echo);
  docker('run','-d','--name',`${name}-web`,'--network',name,'--network',`${name}-edge`,
    '-p','127.0.0.1::8443','--read-only','--tmpfs','/tmp:rw,size=32m,mode=1777',
    '--cap-drop','ALL','--security-opt','no-new-privileges:true',
    '-v',`${resolve('deploy/production/nginx.conf')}:/etc/nginx/nginx.conf:ro`,
    '-v',`${tls}:/run/tls:ro`,'puppeteer/web:r17-core-preview');
  const port = Number(docker('port',`${name}-web`,'8443/tcp').split(':').at(-1));
  const request = headers => new Promise((done,fail)=>{
    const req=https.get({hostname:'localhost',port,path:'/api/proxy-test',ca,headers},res=>{
      let body='';res.on('data',c=>body+=c);res.on('end',()=>{
        try {assert.equal(res.statusCode,200);done(JSON.parse(body))}catch(e){fail(e)}
      });
    });req.setTimeout(3000,()=>req.destroy(new Error('timeout')));req.on('error',fail);
  });
  let normal;
  for(let i=0;i<20;i++) {
    try {normal=await request({});break}catch(e){if(i===19)throw e;await new Promise(r=>setTimeout(r,250))}
  }
  assert.ok(normal['x-forwarded-for']);
  for(const spoof of ['203.0.113.99','203.0.113.99, 127.0.0.1']) {
    const actual=await request({'x-forwarded-for':spoof,'x-real-ip':spoof,'x-client-ip':spoof,
      forwarded:'for=203.0.113.99;proto=http','x-forwarded-proto':'http',
      'x-forwarded-host':'attacker.invalid','x-forwarded-port':'80'});
    for(const key of ['x-forwarded-for','x-real-ip','x-client-ip']) assert.equal(actual[key],normal['x-forwarded-for']);
    assert.equal(actual['x-forwarded-proto'],'https');
    for(const key of ['forwarded','x-forwarded-host','x-forwarded-port']) assert.equal(actual[key],undefined);
  }
  console.log('PASS: real Nginx overwrites spoofed client IP/protocol headers and removes alternate forwarding metadata; TLS verified.');
} finally {
  for(const suffix of ['web','echo']) {try{docker('rm','-f',`${name}-${suffix}`)}catch{}}
  try{docker('network','rm',name)}catch{}
  try{docker('network','rm',`${name}-edge`)}catch{}
}
