// Interactive acceptance of the installed Windows client against the local gateway.
import {createServer} from 'node:http';
import https from 'node:https';
import {randomBytes,createHash} from 'node:crypto';
import {mkdtemp,readFile,writeFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {verifyNativeRecording} from './verify-native-rdp-session.mjs';

const origin='https://backend.localhost:5174',clientId='nt-native-v1';
const asset=process.argv[2];
if(process.platform!=='win32'||!/^[0-9a-f-]{36}$/.test(asset??''))throw Error('Windows and an asset UUID are required');
const ca=await readFile(new URL('../backend/.local/passkey-tls/server.crt',import.meta.url));
function post(path,body,type,token){
 return new Promise((resolve,reject)=>{
 const req=https.request(origin+path,{method:'POST',family:4,ca,servername:'backend.localhost',lookup:(host,options,cb)=>options.all?cb(null,[{address:'127.0.0.1',family:4}]):cb(null,'127.0.0.1',4),
 headers:{'Content-Type':type,...(token?{Authorization:'Bearer '+token}:{})}},res=>{
 const parts=[];let size=0;
 res.on('error',reject);res.on('data',b=>{size+=b.length;if(size>8192)res.destroy(Error('Response too large'));else parts.push(b)});
 res.on('end',()=>res.statusCode===200?resolve(Buffer.concat(parts).toString()):reject(Error('Platform HTTP '+res.statusCode)));
 });
 req.on('error',reject);req.setTimeout(15000,()=>req.destroy(Error('Platform timeout')));req.end(body);
 });
}
const verifier=randomBytes(32).toString('base64url'),state=randomBytes(32).toString('hex');
let finish;
const authorization=new Promise(resolve=>{finish=resolve});
const listener=createServer((req,res)=>{
 res.setHeader('Cache-Control','no-store');res.setHeader('Referrer-Policy','no-referrer');
 const q=new URL(req.url,'http://127.0.0.1');
 if(req.method!=='GET'||req.headers.host!=='127.0.0.1:'+listener.address().port||q.pathname!=='/callback'||q.searchParams.getAll('state').length!==1||q.searchParams.get('state')!==state){res.writeHead(400);res.end();return}
 const code=q.searchParams.get('code');
 res.end('Authorization received. You may close this tab.');finish(q.searchParams.getAll('code').length===1&&/^[a-f0-9]{64}$/.test(code??'')?code:null);
});
listener.listen(0,'127.0.0.1');await once(listener,'listening');
const callback='http://127.0.0.1:'+listener.address().port+'/callback';
const scope='assets:read sessions:connect';
const query=new URLSearchParams({client_id:clientId,redirect_uri:callback,response_type:'code',scope,state,code_challenge_method:'S256',code_challenge:createHash('sha256').update(verifier).digest('base64url')});
let token,dir;
const timer=setTimeout(()=>finish(null),180000);
try{
 const browser=spawn(join(process.env.SystemRoot,'System32','rundll32.exe'),['url.dll,FileProtocolHandler',origin+'/api/oidc/server/authorize?'+query],{windowsHide:true,stdio:'ignore'});
 await once(browser,'spawn');
 const code=await authorization;
 if(!code)throw Error('Authorization declined or expired');
 const form='application/x-www-form-urlencoded';
 const response=JSON.parse(await post('/api/oidc/server/token',new URLSearchParams({client_id:clientId,grant_type:'authorization_code',code,code_verifier:verifier,redirect_uri:callback,device_name:'R14 Windows desktop acceptance',device_os:'windows',client_version:'0.4.0-r14'}).toString(),form));
 if(response.scope!==scope||response.token_type!=='Bearer'||!/^[0-9a-f]{64}$/.test(response.access_token))throw Error('Invalid authorization grant');
 token=response.access_token;
 const file=await post('/api/native/v1/rdp-file',JSON.stringify({assetId:asset}),'application/json',token);
 const match=/^username:s:(NTICKET:([0-9a-f-]{36}):[0-9a-f]{64})\r?$/m.exec(file);
 if(!match||!file.split('\r\n').includes('full address:s:backend.localhost:13389'))throw Error('Unexpected RDP descriptor');
 dir=await mkdtemp(join(tmpdir(),'nt-view-'));
 const path=join(dir,'view.rdp');
 await writeFile(path,['full address:s:backend.localhost:13389','username:s:'+match[1],'authentication level:i:1','enablecredsspsupport:i:0','negotiate security layer:i:1','screen mode id:i:1','desktopwidth:i:1280','desktopheight:i:720','redirectclipboard:i:0','drivestoredirect:s:','redirectprinters:i:0','redirectsmartcards:i:0','audiomode:i:2',''].join('\r\n'),{mode:0o600,flag:'wx'});
 console.log('Session '+match[2]+'. View the desktop for at least 12 seconds, then close mstsc.');
 const desktop=spawn(join(process.env.SystemRoot,'System32','mstsc.exe'),['/public',path],{stdio:'ignore'});
 const [exit]=await once(desktop,'exit');
 if(exit!==0)throw Error('mstsc exited with '+exit);
 const media=await verifyNativeRecording(match[2],asset);
 console.log(`Verified ${media.segments} MP4 segment(s), ${media.bytes} bytes, ${media.durationMs} ms.`);
}finally{
 clearTimeout(timer);listener.close();
 try{if(dir)await rm(dir,{recursive:true,force:true})}finally{
 if(token)await post('/api/oidc/server/revoke',new URLSearchParams({client_id:clientId,token}).toString(),'application/x-www-form-urlencoded');
 }
}
