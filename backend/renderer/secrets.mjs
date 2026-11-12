import {readFileSync,statSync} from 'node:fs';
export function rendererToken(env=process.env) {
 const file=env.NT_MP4_TOKEN_FILE;
 if(file&&env.NT_MP4_TOKEN) throw Error('renderer secret sources conflict');
 let token=env.NT_MP4_TOKEN;
 if(file){
  if(!statSync(file).isFile()||statSync(file).size>4096) throw Error('invalid renderer secret file');
  token=readFileSync(file,'utf8').trim();
 }
 if(!token||token.length<32||token.length>4096) throw Error('renderer token required');
 return token;
}
