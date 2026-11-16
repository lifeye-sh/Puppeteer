import {chromium} from 'playwright-core';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {readFile,stat,writeFile,mkdtemp,rm} from 'node:fs/promises';
import {dirname,join} from 'node:path';
import {once} from 'node:events';
import {inspectArchive,events} from './archive.mjs';
import {quality} from './quality.mjs';
const bundle=await readFile(new URL('./public/replay.js',import.meta.url),'utf8');
async function joinNative(archive,output,signal,progress,profile){
 const dir=await mkdtemp(join(dirname(output),'segments-'));
 try{
  const list=[];
  for await(const event of events(archive.input,signal)){
   signal.throwIfAborted();
   const name=event.sequence+'.mp4';
   await writeFile(join(dir,name),Buffer.from(event.payload,'base64'),{mode:0o600});
   list.push('file '+name);
  }
  await writeFile(join(dir,'index.txt'),list.join('\n')+'\n',{mode:0o600});
  // Normalize changing desktop sizes to the selected output canvas, including
  // aspect ratio, before exporting one seekable H.264 track.
  const process=spawn('ffmpeg',['-hide_banner','-loglevel','error','-nostdin','-protocol_whitelist','file','-safe','1','-f','concat','-i','index.txt','-map','0:v:0','-an','-vf',`scale=${profile.nativeWidth}:${profile.nativeHeight}:force_original_aspect_ratio=decrease,pad=${profile.nativeWidth}:${profile.nativeHeight}:(ow-iw)/2:(oh-ih)/2,setsar=1`,'-filter_threads','1','-threads','1','-c:v','libx264','-preset','veryfast','-crf',String(profile.nativeCRF),'-pix_fmt','yuv420p','-movflags','+faststart','-fs','268435456','-progress','pipe:1','-y',output],{cwd:dir,stdio:['ignore','pipe','ignore']});
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
export async function convert(input,output,signal,progress,qualityName='balanced'){
 const profile=quality(qualityName),interval=1000/profile.fps;
 const archive={header:await inspectArchive(input,signal),input};
 if(archive.header.format==='native-rdp-mp4-v1')return joinNative(archive,output,signal,progress,profile);
 const iterator=events(input,signal)[Symbol.asyncIterator]();
 let browser,ffmpeg,ffmpegDone,browserClosing;
 // Cancellation and finally must await the same close operation. A second
 // close can observe protocol disconnection before the first has reaped the
 // browser process, allowing the next job to start while children remain.
 const closeBrowser=()=>browser?(browserClosing??=browser.close()):Promise.resolve();
 const abort=()=>{ffmpeg?.kill('SIGKILL');void closeBrowser().catch(()=>{})};signal.addEventListener('abort',abort,{once:true});
 try{
  signal.throwIfAborted();browser=await chromium.launch({executablePath:'/usr/bin/chromium',headless:true,args:['--disable-dev-shm-usage'],env:{PATH:process.env.PATH,HOME:'/tmp',LANG:'C.UTF-8'}});
  signal.throwIfAborted();
  const context=await browser.newContext({viewport:{width:profile.width,height:profile.height},serviceWorkers:'block',acceptDownloads:false});
  await context.route('**/*',r=>r.abort());
  const page=await context.newPage();page.setDefaultTimeout(30000);await page.setContent('<!doctype html><meta charset="utf-8"><body style="margin:0;background:#1e1e1e"></body>');await page.addScriptTag({content:bundle});
  let chunk=Buffer.alloc(0),offset=0;
  await page.exposeFunction('readReplayChunk',async()=>{
   signal.throwIfAborted();
   if(archive.header.format==='guacamole-events-v1'){
    while(offset>=chunk.length){const next=await iterator.next();if(next.done)return null;chunk=Buffer.from(next.value.payload,'base64');offset=0;}
    const part=chunk.subarray(offset,offset+65536);offset+=part.length;return part.toString('base64');
   }
   const batch=[];let bytes=0;
   while(bytes<65536){const next=await iterator.next();if(next.done)break;batch.push(next.value);bytes+=next.value.payload.length;}
   return batch.length?batch:null;
  });
  const duration=await page.evaluate(data=>window.initializeReplay(data),{...archive.header,width:profile.width,height:profile.height,jpeg:profile.jpeg});
  ffmpeg=spawn('ffmpeg',['-hide_banner','-loglevel','error','-threads','1','-f','image2pipe','-vcodec','mjpeg','-framerate',String(profile.fps),'-i','pipe:0','-an','-c:v','libx264','-preset','veryfast','-crf',String(profile.crf),'-vf','scale=in_range=full:out_range=tv','-color_range','tv','-pix_fmt','yuv420p','-threads','1','-movflags','+faststart','-y',output],{stdio:['pipe','ignore','pipe']});
  let encodeError;ffmpeg.on('error',e=>encodeError=e);ffmpeg.stdin.on('error',e=>encodeError=e);ffmpeg.stderr.resume();const exited=ffmpegDone=once(ffmpeg,'close');exited.catch(()=>{});
  let frames=Math.ceil(duration/interval)+1;
  for(let i=0;i<frames;i++){
   signal.throwIfAborted();if(encodeError)throw encodeError;
   if(ffmpeg.exitCode!==null||ffmpeg.signalCode!==null)throw Error('encoding_failed');
   const rendered=await page.evaluate(t=>window.renderFrame(t),i*interval);
   frames=Math.max(frames,Math.ceil(rendered.duration/interval)+1);
   const frame=Buffer.from(rendered.image,'base64');
   // A write callback provides backpressure without retaining one unresolved
   // exit-promise subscriber per frame for the entire recording.
   await new Promise((resolve,reject)=>ffmpeg.stdin.write(frame,error=>error?reject(error):resolve()));
   if(i%profile.fps===0){progress(Math.min(98,Math.floor(i/frames*98)));if((await stat(output).catch(()=>({size:0}))).size>256*1024*1024)throw Error('output_limit')}
  }
  ffmpeg.stdin.end();const [code]=await exited;signal.throwIfAborted();if(code!==0)throw Error('encoding_failed');
  const info=await stat(output);if(info.size>256*1024*1024)throw Error('output_limit');if(info.size<32)throw Error('encoding_failed');progress(99);
 }finally{signal.removeEventListener('abort',abort);ffmpeg?.kill('SIGKILL');await ffmpegDone?.catch(()=>{});try{await closeBrowser()}finally{await iterator.return()}}
}

