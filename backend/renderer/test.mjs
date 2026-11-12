import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm,stat,readFile} from 'node:fs/promises';
import {execFileSync} from 'node:child_process';
import {parseArchive,convert} from './convert.mjs';
const archive=(format,events)=>[JSON.stringify({type:'header',format,durationMs:500,events:events.length}),...events.map(e=>JSON.stringify(e)),JSON.stringify({type:'end',events:events.length})].join('\n');
const event=(kind,payload,sequence=1,elapsedMs=500)=>({kind,payload:Buffer.from(payload).toString('base64'),sequence,elapsedMs});
test('reject invalid archive boundaries',()=>{assert.throws(()=>parseArchive(archive('terminal-events-v1',[event('output','x',2)])));assert.throws(()=>parseArchive('{}\n{}'));});
test('native RDP export keeps the complete timeline across a desktop resize',async()=>{
 const dir=await mkdtemp('/tmp/mp4-native-');
 try{
  const frames=[];
  for(const [i,size,color] of [[0,'160x90','red'],[1,'96x96','blue']]){
   const path=dir+'/'+i+'.mp4';
   execFileSync('ffmpeg',['-loglevel','error','-f','lavfi','-i','color='+color+':s='+size+':r=10','-frames:v','5','-c:v','libx264','-threads','1','-pix_fmt','yuv420p','-movflags','+frag_keyframe+empty_moov+default_base_moof',path]);
   frames.push(event('mp4',await readFile(path),i+1,(i+1)*500));
  }
  const input=dir+'/recording.jsonl',out=dir+'/out.mp4';
  await writeFile(input,[{type:'header',format:'native-rdp-mp4-v1',durationMs:1000,events:2},...frames,{type:'end',events:2}].map(e=>JSON.stringify(e)).join('\n'));
  await convert(input,out,AbortSignal.timeout(30000),()=>{});
  for(const [time,channel] of [['0.1',0],['0.8',2]]){
   const data=execFileSync('ffmpeg',['-v','error','-ss',time,'-i',out,'-frames:v','1','-vf','crop=2:2:960:540','-pix_fmt','rgb24','-f','rawvideo','pipe:1']);
   assert.ok(data.length===12&&data[channel]>200&&data[2-channel]<30);
  }
 }finally{await rm(dir,{recursive:true,force:true})}
});
test('actual terminal and graphical H264 encodes',async()=>{
 const dir=await mkdtemp('/tmp/mp4-test-');
 const ins=(...args)=>args.map(a=>`${String(a).length}.${a}`).join(',')+';';
 const graphics=ins('size',0,640,480)+ins('rect',0,0,0,640,480)+ins('cfill',12,0,32,100,220,255)+ins('sync',1000)+ins('rect',0,0,0,120,120)+ins('cfill',12,0,220,100,32,255)+ins('sync',1500);
 try{for(const [name,format,events] of [['terminal','terminal-events-v1',[event('output','hello 世界\r\n\x1b[32mOK',1,0),event('resize','100,30',2)]],['graphics','guacamole-events-v1',[event('graphics',graphics)]]]){
  const input=dir+'/'+name+'.jsonl',output=dir+'/'+name+'.mp4';await writeFile(input,archive(format,events));await convert(input,output,AbortSignal.timeout(60000),()=>{});
  const probe=JSON.parse(execFileSync('ffprobe',['-v','error','-show_streams','-show_format','-of','json',output],{encoding:'utf8'}));
  assert.equal(probe.streams[0].codec_name,'h264');assert.equal(probe.streams[0].width,1280);assert.equal(probe.streams[0].height,720);assert.equal(probe.streams[0].pix_fmt,'yuv420p');assert.ok(Number(probe.format.duration)>=.5);assert.ok((await stat(output)).size>1000);
  const pixels=execFileSync('ffmpeg',['-v','error','-i',output,'-frames:v','1','-f','rawvideo','-pix_fmt','rgb24','pipe:1'],{maxBuffer:4*1024*1024});if(name==='graphics'){const offset=(360*1280+640)*3;assert.ok(pixels[offset+2]>pixels[offset]+80,'graphics frame is blue')}else assert.ok(new Set(pixels).size>5,'terminal contains rendered text');
 }}finally{await rm(dir,{recursive:true,force:true})}
});

