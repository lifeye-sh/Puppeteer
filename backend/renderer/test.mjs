import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm,stat,readFile} from 'node:fs/promises';
import {execFileSync} from 'node:child_process';
import {convert} from './convert.mjs';
import {Lifecycle} from './lifecycle.mjs';
import {quality} from './quality.mjs';
const archive=(format,events)=>[JSON.stringify({type:'header',format,durationMs:500,events:events.length}),...events.map(e=>JSON.stringify(e)),JSON.stringify({type:'end',events:events.length})].join('\n');
const event=(kind,payload,sequence=1,elapsedMs=500)=>({kind,payload:Buffer.from(payload).toString('base64'),sequence,elapsedMs});

test('idle drain protects uploads and retained results and resume admits work',()=>{
 let now=0;const life=new Lifecycle(()=>now);
 assert.equal(life.drain(false),false);
 now=300000;assert.equal(life.drain(true),false);
 life.touch();now+=299999;assert.equal(life.drain(false),false);
 now++;assert.equal(life.drain(false),true);assert.equal(life.draining,true);
 life.resume();assert.equal(life.draining,false);assert.equal(life.drain(false),false);
 assert.throws(()=>quality('unknown'),/invalid_renderer_quality/);
 assert.throws(()=>quality('__proto__'),/invalid_renderer_quality/);
});

test('quality profiles encode requested size and frame rate with the final frame',async()=>{
 const dir=await mkdtemp('/tmp/mp4-quality-');
 const ins=(...args)=>args.map(a=>`${String(a).length}.${a}`).join(',')+';';
 const wire=ins('size',0,64,64)+ins('sync',1000)+ins('rect',0,0,0,64,64)+ins('cfill',12,0,220,40,20,255)+ins('sync',1500);
 try{for(const name of ['economy','balanced','sharp']){
  const profile=quality(name),input=dir+'/input.jsonl',output=dir+'/'+name+'.mp4';
  await writeFile(input,archive('guacamole-events-v1',[event('graphics',wire)]));
  await convert(input,output,AbortSignal.timeout(60000),()=>{},name);
  const probe=JSON.parse(execFileSync('ffprobe',['-v','error','-show_streams','-show_format','-of','json',output],{encoding:'utf8'}));
  assert.equal(probe.streams[0].width,profile.width);assert.equal(probe.streams[0].height,profile.height);assert.equal(probe.streams[0].r_frame_rate,profile.fps+'/1');
  assert.ok(Number(probe.format.duration)>=.5&&Number(probe.format.duration)<=.61);
  const pixels=execFileSync('ffmpeg',['-v','error','-ss','0.5','-i',output,'-frames:v','1','-vf',`crop=2:2:${profile.width/2}:${profile.height/2}`,'-pix_fmt','rgb24','-f','rawvideo','pipe:1']);
  assert.ok(pixels.length===12&&pixels[0]>pixels[2]+100,name+' lost final frame');
 }}finally{await rm(dir,{recursive:true,force:true})}
});
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
  for(const name of ['economy','sharp']){
   const output=dir+'/'+name+'.mp4';await convert(input,output,AbortSignal.timeout(30000),()=>{},name);
   const p=JSON.parse(execFileSync('ffprobe',['-v','error','-count_frames','-show_streams','-show_format','-of','json',output],{encoding:'utf8'}));
   assert.equal(p.streams[0].width,quality(name).nativeWidth);assert.equal(p.streams[0].height,quality(name).nativeHeight);
   assert.equal(p.streams[0].nb_read_frames,'10');assert.equal(p.streams[0].r_frame_rate,'10/1');assert.equal(Number(p.format.duration),1);
  }
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


test('graphics replay handles split UTF-8/instructions and rejects an incomplete tail',async()=>{
 const dir=await mkdtemp('/tmp/mp4-split-');
 const ins=(...args)=>args.map(a=>`${[...String(a)].length}.${a}`).join(',')+';';
 const wire=Buffer.from(ins('name','桌面😀')+ins('size',0,32,32)+ins('sync',1000)+ins('rect',0,0,0,32,32)+ins('cfill',12,0,220,40,20,255)+ins('sync',1500));
 const events=[];for(let at=0;at<wire.length;at+=7)events.push(event('graphics',wire.subarray(at,at+7),events.length+1));
 try{
  const input=dir+'/input.jsonl',output=dir+'/output.mp4';
  await writeFile(input,archive('guacamole-events-v1',events));
  await convert(input,output,AbortSignal.timeout(30000),()=>{});
  const pixel=execFileSync('ffmpeg',['-v','error','-ss','0.5','-i',output,'-frames:v','1','-vf','crop=2:2:640:360','-pix_fmt','rgb24','-f','rawvideo','pipe:1']);
  assert.ok(pixel.length===12&&pixel[0]>pixel[2]+100,'final graphical frame was not replayed');
  events.push(event('graphics','5.unfin',events.length+1));
  await writeFile(input,archive('guacamole-events-v1',events));
  await assert.rejects(convert(input,dir+'/bad.mp4',AbortSignal.timeout(30000),()=>{}),/graphics_incomplete_frame|Element terminator/);
 }finally{await rm(dir,{recursive:true,force:true})}
});

test('graphics preserves complete drawing instructions after the final sync on disconnect',async()=>{
 const dir=await mkdtemp('/tmp/mp4-disconnect-');
 const ins=(...args)=>args.map(a=>`${String(a).length}.${a}`).join(',')+';';
 const initial=ins('size',0,32,32)+ins('rect',0,0,0,32,32)+ins('cfill',12,0,20,40,220,255)+ins('sync',1000);
 const tail=ins('rect',0,0,0,32,32)+ins('cfill',12,0,220,40,20,255)+ins('nop');
 try{
  const input=dir+'/input.jsonl',output=dir+'/output.mp4';
  await writeFile(input,archive('guacamole-events-v1',[event('graphics',initial,1,0),event('graphics',tail,2,500)]));
  await convert(input,output,AbortSignal.timeout(30000),()=>{});
  for(const [time,channel] of [['0',2],['0.5',0]]){
   const pixel=execFileSync('ffmpeg',['-v','error','-ss',time,'-i',output,'-frames:v','1','-vf','crop=2:2:640:360','-pix_fmt','rgb24','-f','rawvideo','pipe:1']);
   assert.ok(pixel.length===12&&pixel[channel]>pixel[2-channel]+100,'disconnect tail rendered at wrong time or lost');
  }
  for(const [i,suffix] of [Buffer.from('5.unfin'),Buffer.from([0xe4,0xb8])].entries()){
   const raw=Buffer.concat([Buffer.from(tail),suffix]);
   await writeFile(input,archive('guacamole-events-v1',[event('graphics',initial,1,0),event('graphics',raw,2,500)]));
   await assert.rejects(convert(input,dir+'/bad-'+i+'.mp4',AbortSignal.timeout(30000),()=>{}),/graphics_incomplete_frame|Element terminator|encoded data/);
  }
 }finally{await rm(dir,{recursive:true,force:true})}
});
