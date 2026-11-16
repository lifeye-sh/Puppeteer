import {Terminal} from '@xterm/xterm';
import Guacamole from '@puppeteer/guacamole-common-js';

const palette=['#000000','#cd0000','#00cd00','#cdcd00','#0000ee','#cd00cd','#00cdcd','#e5e5e5','#7f7f7f','#ff0000','#00ff00','#ffff00','#5c5cff','#ff00ff','#00ffff','#ffffff'];
function color(c,fg){const n=fg?c.getFgColor():c.getBgColor();if(fg?c.isFgDefault():c.isBgDefault())return fg?'#e5e7eb':'#1e1e1e';if(fg?c.isFgRGB():c.isBgRGB())return '#'+n.toString(16).padStart(6,'0');if(n<16)return palette[n];if(n>=232){const v=8+(n-232)*10;return `rgb(${v},${v},${v})`}const v=n-16,l=[0,95,135,175,215,255];return `rgb(${l[Math.floor(v/36)]},${l[Math.floor(v/6)%6]},${l[v%6]})`}
let terminal,graphics,tunnel,events=[],index=0,ended=false,parser,instructions=[],decoder,nextFrame,origin,lastTimestamp,frameBytes=0,graphicsError,replayDuration=0;
async function graphicsFrame(){
 if(ended)return null;
 const frame=[];let bytes=0;
 while(true){
  if(!instructions.length){
   const chunk=await window.readReplayChunk();
   if(chunk===null){
    decoder.decode();ended=true;
    // A sentinel must parse as exactly one fresh instruction. Otherwise the
    // stream ended inside an element (the vendor parser has no finish method).
    const marker='puppeteer-stream-end';parser.receive(`${marker.length}.${marker};`);
    if(instructions.length!==1||instructions[0][0]!==marker||instructions[0][1].length)throw Error('graphics_incomplete_frame');
    instructions=[];
    // Closing a live connection can leave complete drawing instructions after
    // its last sync. Preserve them at the archive's end, using the normal display
    // flush, without inventing a remote sync or accepting a truncated instruction.
    if(frame.length){
     if(origin===undefined)throw Error('graphics_incomplete_frame');
     return {elapsed:Math.max(replayDuration,lastTimestamp-origin),instructions:frame};
    }
    return null;
   }
   const data=decoder.decode(Uint8Array.from(atob(chunk),c=>c.charCodeAt(0)),{stream:true});
   frameBytes+=data.length;if(frameBytes>32*1024*1024)throw Error('graphics_frame_limit');
   parser.receive(data);if(!instructions.length)continue;
  }
  const item=instructions.shift();frame.push(item);bytes+=item[0].length+item[1].join('').length;
  if(bytes>32*1024*1024)throw Error('graphics_frame_limit');
  if(item[0]==='sync'){
   const timestamp=Number(item[1][0]);if(!Number.isSafeInteger(timestamp)||(lastTimestamp!==undefined&&timestamp<lastTimestamp))throw Error('graphics_timestamp');
   origin??=timestamp;lastTimestamp=timestamp;frameBytes=0;
   return {elapsed:timestamp-origin,instructions:frame};
  }
 }
}
let outputWidth=1280,outputHeight=720,jpegQuality=.85;
const canvas=document.createElement('canvas');canvas.width=1280;canvas.height=720;document.body.append(canvas);
const ctx=canvas.getContext('2d');const host=document.createElement('div');host.style.cssText='position:absolute;left:-10000px;width:1280px;height:720px';document.body.append(host);
window.initializeReplay=async header=>{
 outputWidth=canvas.width=header.width;outputHeight=canvas.height=header.height;jpegQuality=header.jpeg;
 let duration=header.durationMs;
 replayDuration=duration;
 if(header.format==='guacamole-events-v1'){
  tunnel=new Guacamole.Tunnel();tunnel.connect=()=>{};tunnel.sendMessage=()=>{};tunnel.disconnect=()=>{};
  graphics=new Guacamole.Client(tunnel);host.append(graphics.getDisplay().getElement());graphics.connect();
  graphics.onerror=()=>{graphicsError=Error('graphics_decode')};
  parser=new Guacamole.Parser();decoder=new TextDecoder('utf-8',{fatal:true});parser.oninstruction=(op,args)=>instructions.push([op,args]);
  nextFrame=await graphicsFrame();if(!nextFrame)throw Error('graphics_decode');
 }else if(header.format==='terminal-events-v1'){
  terminal=new Terminal({cols:80,rows:24,allowProposedApi:true,disableStdin:true,scrollback:0});terminal.parser.registerOscHandler(52,()=>true);terminal.open(host);
 }else throw Error('unsupported_format');
 if(!Number.isFinite(duration)||duration<0||duration>7200000)throw Error('recording_limit');return Math.max(500,duration);
};
window.renderFrame=async elapsed=>{
 if(graphicsError)throw graphicsError;
 if(graphics){
  while(nextFrame&&nextFrame.elapsed<=elapsed){
   for(const [op,args] of nextFrame.instructions)tunnel.oninstruction(op,args);
   await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('graphics_flush_timeout')),15000);graphics.getDisplay().flush(()=>{clearTimeout(timer);resolve()})});
   nextFrame=await graphicsFrame();
  }
 }
 if(terminal){while(true){
  if(index>=events.length){if(ended)break;events=await window.readReplayChunk();index=0;if(events===null){ended=true;events=[];break;}}
  if(events[index].elapsedMs>elapsed)break;
  const e=events[index++],payload=Uint8Array.from(atob(e.payload),c=>c.charCodeAt(0));
  if(e.kind==='resize'){const s=new TextDecoder().decode(payload);if(!/^\d+,\d+$/.test(s))throw Error('invalid_size');const [cols,rows]=s.split(',').map(Number);if(cols<2||cols>500||rows<2||rows>300)throw Error('invalid_size');terminal.resize(cols,rows)}else await new Promise(resolve=>terminal.write(payload,resolve));
 }}
 ctx.fillStyle='#1e1e1e';ctx.fillRect(0,0,outputWidth,outputHeight);
 if(graphics){const image=graphics.getDisplay().flatten();if(image.width>8192||image.height>8192)throw Error('invalid_size');if(image.width&&image.height){const scale=Math.min(outputWidth/image.width,outputHeight/image.height);ctx.drawImage(image,(outputWidth-image.width*scale)/2,(outputHeight-image.height*scale)/2,image.width*scale,image.height*scale)}}
 if(terminal){const w=Math.min(outputWidth/terminal.cols,outputHeight/terminal.rows/2),h=w*2;ctx.font=`${h*.8}px monospace`;ctx.textBaseline='top';const b=terminal.buffer.active;for(let y=0;y<terminal.rows;y++){const line=b.getLine(b.viewportY+y);for(let x=0;x<terminal.cols;x++){const c=line?.getCell(x);if(!c||!c.getWidth())continue;const fg=color(c,!c.isInverse());ctx.fillStyle=color(c,!!c.isInverse());ctx.fillRect(x*w,y*h,c.getWidth()*w,h);ctx.fillStyle=fg;if(!c.isInvisible())ctx.fillText(c.getChars(),x*w,y*h)}}}
 const duration=Math.max(elapsed,nextFrame?.elapsed??0);if(duration>7200000)throw Error('recording_limit');
 return {image:canvas.toDataURL('image/jpeg',jpegQuality).split(',')[1],duration};
};
