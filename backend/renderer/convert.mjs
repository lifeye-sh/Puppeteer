import {chromium} from 'playwright-core';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {readFile,stat,writeFile,mkdtemp,rm} from 'node:fs/promises';
import {dirname,join} from 'node:path';
import {once} from 'node:events';
const bundle=await readFile(new URL('./public/replay.js',import.meta.url),'utf8');
export function parseArchive(text){
 const lines=text.trim().split('\n').map(s=>JSON.parse(s));const header=lines.shift(),end=lines.pop();
 if(header?.type!=='header'||end?.type!=='end'||header.events!==lines.length||end.events!==lines.length||!lines.length)throw Error('recording_integrity_failed');
 if(!Number.isSafeInteger(header.durationMs)||header.durationMs<0||header.durationMs>7200000)throw Error('recording_limit');
 let last=0,total=0;for(const [i,e] of lines.entries()){
  if(header.format==='native-rdp-mp4-v1'){
   if(lines.length>2048||e.sequence!==i+1||e.kind!=='mp4'||!Number.isSafeInteger(e.elapsedMs)||e.elapsedMs<=last||e.elapsedMs-last>10000||
      typeof e.payload!=='string'||e.payload.length>4*Math.ceil(16*1024*1024/3))throw Error('recording_integrity_failed');
   const data=Buffer.from(e.payload,'base64');
   if(data.length<32||data.toString('ascii',4,8)!=='ftyp'||data.toString('base64')!==e.payload)throw Error('recording_integrity_failed');
   total+=data.length;if(total>100*1024*1024)throw Error('recording_limit');
   last=e.elapsedMs;continue;
  }
  if(e.sequence!==i+1||!Number.isSafeInteger(e.elapsedMs)||e.elapsedMs<last||e.elapsedMs>7200000||!['output','resize','exec-output','graphics'].includes(e.kind)||typeof e.payload!=='string'||!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(e.payload))throw Error('recording_integrity_failed');
  last=e.elapsedMs;total+=Buffer.byteLength(e.payload,'base64');if(total>100*1024*1024)throw Error('recording_limit');
 }if(last>header.durationMs||(header.format==='native-rdp-mp4-v1'&&last!==header.durationMs))throw Error('recording_integrity_failed');return {header,events:lines};
}
async function joinNative(archive,output,signal,progress){
 const dir=await mkdtemp(join(dirname(output),'segments-'));
 try{
  const list=[];
  for(const event of archive.events){
   signal.throwIfAborted();
   const name=event.sequence+'.mp4';
   await writeFile(join(dir,name),Buffer.from(event.payload,'base64'),{mode:0o600});
   list.push('file '+name);
  }
  await writeFile(join(dir,'index.txt'),list.join('\n')+'\n',{mode:0o600});
  // Normalize changing desktop sizes to the same full-HD canvas, including
  // aspect ratio, before exporting one seekable H.264 track.
  const process=spawn('ffmpeg',['-hide_banner','-loglevel','error','-nostdin','-protocol_whitelist','file','-safe','1','-f','concat','-i','index.txt','-map','0:v:0','-an','-vf','scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1','-filter_threads','1','-threads','1','-c:v','libx264','-preset','veryfast','-crf','23','-pix_fmt','yuv420p','-movflags','+faststart','-fs','268435456','-progress','pipe:1','-y',output],{cwd:dir,stdio:['ignore','pipe','ignore']});
  const exited=once(process,'close');
  const abort=()=>process.kill('SIGKILL');
  signal.addEventListener('abort',abort,{once:true});
  if(signal.aborted)abort();
  process.stdout.on('data',buf=>{const us=/out_time_us=(\d+)/.exec(buf.toString());if(us)progress(Math.min(98,Math.floor(Number(us[1])*0.1/archive.header.durationMs)))});
  try{
   const [code]=await exited;
   signal.throwIfAborted();
   if(code!==0)throw Error('encoding_failed');
   const {size}=await stat(output);
   if(size>=256*1024*1024)throw Error('output_limit');
   if(size<32)throw Error('encoding_failed');
   const {stdout}=await promisify(execFile)('ffprobe',['-v','error','-select_streams','v:0','-count_packets','-show_entries','stream=nb_read_packets:format=duration','-of','json',output],{signal,maxBuffer:16384});
   const probe=JSON.parse(stdout);
   if(Number(probe.streams[0]?.nb_read_packets)!==archive.header.durationMs/100||Math.abs(Number(probe.format.duration)*1000-archive.header.durationMs)>60)throw Error('encoding_failed');
   progress(99);
  }finally{signal.removeEventListener('abort',abort)}
 }finally{await rm(dir,{recursive:true,force:true})}
}
export async function convert(input,output,signal,progress){
 const archive=parseArchive(await readFile(input,'utf8'));
 if(archive.header.format==='native-rdp-mp4-v1')return joinNative(archive,output,signal,progress);
 let browser,ffmpeg;const abort=()=>{ffmpeg?.kill('SIGKILL');void browser?.close()};signal.addEventListener('abort',abort,{once:true});
 try{
  signal.throwIfAborted();browser=await chromium.launch({executablePath:'/usr/bin/chromium',headless:true,args:['--disable-dev-shm-usage'],env:{PATH:process.env.PATH,HOME:'/tmp',LANG:'C.UTF-8'}});
  const context=await browser.newContext({viewport:{width:1280,height:720},serviceWorkers:'block',acceptDownloads:false});
  await context.route('**/*',r=>r.abort());
  const page=await context.newPage();page.setDefaultTimeout(30000);await page.setContent('<!doctype html><meta charset="utf-8"><body style="margin:0;background:#1e1e1e"></body>');await page.addScriptTag({content:bundle});
  const duration=await page.evaluate(data=>window.initializeReplay(data),archive);
  ffmpeg=spawn('ffmpeg',['-hide_banner','-loglevel','error','-threads','1','-f','image2pipe','-vcodec','mjpeg','-framerate','20','-i','pipe:0','-an','-c:v','libx264','-preset','veryfast','-crf','25','-vf','scale=in_range=full:out_range=tv','-color_range','tv','-pix_fmt','yuv420p','-threads','1','-movflags','+faststart','-y',output],{stdio:['pipe','ignore','pipe']});
  let encodeError;ffmpeg.on('error',e=>encodeError=e);ffmpeg.stdin.on('error',e=>encodeError=e);ffmpeg.stderr.resume();const exited=once(ffmpeg,'close');exited.catch(()=>{});
  const frames=Math.ceil(duration/50)+1;
  for(let i=0;i<frames;i++){
   signal.throwIfAborted();if(encodeError)throw encodeError;
   if(ffmpeg.exitCode!==null||ffmpeg.signalCode!==null)throw Error('encoding_failed');
   const frame=Buffer.from(await page.evaluate(t=>window.renderFrame(t),i*50),'base64');
   if(!ffmpeg.stdin.write(frame))await Promise.race([
    once(ffmpeg.stdin,'drain',{signal}),
    exited.then(()=>{throw Error('encoding_failed')}),
   ]);
   if(i%20===0){progress(Math.min(98,Math.floor(i/frames*98)));if((await stat(output).catch(()=>({size:0}))).size>256*1024*1024)throw Error('output_limit')}
  }
  ffmpeg.stdin.end();const [code]=await exited;signal.throwIfAborted();if(code!==0)throw Error('encoding_failed');
  const info=await stat(output);if(info.size>256*1024*1024)throw Error('output_limit');if(info.size<32)throw Error('encoding_failed');progress(99);
 }finally{signal.removeEventListener('abort',abort);ffmpeg?.kill('SIGKILL');await browser?.close()}
}

