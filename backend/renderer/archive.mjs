import {createReadStream} from 'node:fs';

const maxLine=24*1024*1024, maxArchive=150*1024*1024;
export function validBase64(s){return typeof s==='string'&&s.length%4===0&&/^[A-Za-z0-9+/]*={0,2}$/.test(s);}
// Bound both file size and individual records before allocating JSON objects.
export async function* records(path,signal){
 let pending=[],length=0,total=0;
 for await(const chunk of createReadStream(path,{highWaterMark:64*1024,signal})){
  total+=chunk.length;if(total>maxArchive)throw Error('recording_limit');
  let start=0;
  for(let i=0;i<chunk.length;i++)if(chunk[i]===10){
   if(length+i-start>maxLine)throw Error('recording_limit');
   const line=Buffer.concat([...pending,chunk.subarray(start,i)]).toString('utf8');
   pending=[];length=0;start=i+1;
   if(!line.trim())throw Error('recording_integrity_failed');
   yield JSON.parse(line);
  }
  length+=chunk.length-start;if(length>maxLine)throw Error('recording_limit');
  if(start<chunk.length)pending.push(chunk.subarray(start));
 }
 if(length){const line=Buffer.concat(pending).toString('utf8');if(line.trim())yield JSON.parse(line);}
}

export async function inspectArchive(path,signal){
 let header,end,count=0,last=0,total=0;
 for await(const e of records(path,signal)){
  if(!e||typeof e!=='object')throw Error('recording_integrity_failed');
  if(!header){
   header=e;
   if(e.type!=='header'||!['terminal-events-v1','guacamole-events-v1','native-rdp-mp4-v1'].includes(e.format)||!Number.isSafeInteger(e.events)||e.events<1||!Number.isSafeInteger(e.durationMs)||e.durationMs<0||e.durationMs>7200000)throw Error('recording_integrity_failed');
   continue;
  }
  if(end)throw Error('recording_integrity_failed');
  if(e.type==='end'){end=e;continue;}
  count++;
  if(e.sequence!==count||!Number.isSafeInteger(e.elapsedMs)||e.elapsedMs<last||e.elapsedMs>header.durationMs||!validBase64(e.payload))throw Error('recording_integrity_failed');
  const data=Buffer.from(e.payload,'base64');total+=data.length;
  if(total>100*1024*1024)throw Error('recording_limit');
  if(header.format==='native-rdp-mp4-v1'){
   if(count>2048||e.kind!=='mp4'||e.elapsedMs<=last||e.elapsedMs-last>10000||data.length>16*1024*1024||data.length<32||data.toString('ascii',4,8)!=='ftyp'||data.toString('base64')!==e.payload)throw Error('recording_integrity_failed');
  }else if(!(header.format==='guacamole-events-v1'?['graphics']:['output','resize','exec-output']).includes(e.kind))throw Error('recording_integrity_failed');
  last=e.elapsedMs;
 }
 if(!header||!end||count!==header.events||end.events!==count||(header.format==='native-rdp-mp4-v1'&&last!==header.durationMs))throw Error('recording_integrity_failed');
 return header;
}

export async function* events(path,signal){
 for await(const e of records(path,signal))if(e.type!=='header'&&e.type!=='end')yield e;
}
