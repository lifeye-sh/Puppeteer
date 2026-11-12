import {Terminal} from '@xterm/xterm';
import Guacamole from '@dushixiang/guacamole-common-js';

const palette=['#000000','#cd0000','#00cd00','#cdcd00','#0000ee','#cd00cd','#00cdcd','#e5e5e5','#7f7f7f','#ff0000','#00ff00','#ffff00','#5c5cff','#ff00ff','#00ffff','#ffffff'];
function color(c,fg){const n=fg?c.getFgColor():c.getBgColor();if(fg?c.isFgDefault():c.isBgDefault())return fg?'#e5e7eb':'#1e1e1e';if(fg?c.isFgRGB():c.isBgRGB())return '#'+n.toString(16).padStart(6,'0');if(n<16)return palette[n];if(n>=232){const v=8+(n-232)*10;return `rgb(${v},${v},${v})`}const v=n-16,l=[0,95,135,175,215,255];return `rgb(${l[Math.floor(v/36)]},${l[Math.floor(v/6)%6]},${l[v%6]})`}
let terminal,graphics,events,index=0;
const canvas=document.createElement('canvas');canvas.width=1280;canvas.height=720;document.body.append(canvas);
const ctx=canvas.getContext('2d');const host=document.createElement('div');host.style.cssText='position:absolute;left:-10000px;width:1280px;height:720px';document.body.append(host);
window.initializeReplay=async archive=>{
 events=archive.events.map(e=>({...e,payload:Uint8Array.from(atob(e.payload),c=>c.charCodeAt(0))}));
 let duration=archive.header.durationMs;
 if(archive.header.format==='guacamole-events-v1'){
  const url=URL.createObjectURL(new Blob(events.map(e=>e.payload)));
  graphics=new Guacamole.SessionRecording(new Guacamole.StaticHTTPTunnel(url));host.append(graphics.getDisplay().getElement());
  await new Promise((resolve,reject)=>{graphics.onload=resolve;graphics.onerror=()=>reject(Error('graphics_decode'));graphics.connect()});
  duration=Math.max(duration,graphics.getDuration());
 }else if(archive.header.format==='terminal-events-v1'){
  terminal=new Terminal({cols:80,rows:24,allowProposedApi:true,disableStdin:true,scrollback:0});terminal.parser.registerOscHandler(52,()=>true);terminal.open(host);
 }else throw Error('unsupported_format');
 if(!Number.isFinite(duration)||duration<0||duration>7200000)throw Error('recording_limit');return Math.max(500,duration);
};
window.renderFrame=async elapsed=>{
 if(graphics){
  await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('graphics_seek_timeout')),15000);graphics.seek(Math.min(elapsed,graphics.getDuration()),()=>{clearTimeout(timer);resolve()})});
 }
 if(terminal){while(index<events.length&&events[index].elapsedMs<=elapsed){const e=events[index++];if(e.kind==='resize'){const s=new TextDecoder().decode(e.payload);if(!/^\d+,\d+$/.test(s))throw Error('invalid_size');const [cols,rows]=s.split(',').map(Number);if(cols<2||cols>500||rows<2||rows>300)throw Error('invalid_size');terminal.resize(cols,rows)}else await new Promise(resolve=>terminal.write(e.payload,resolve))}}
 ctx.fillStyle='#1e1e1e';ctx.fillRect(0,0,1280,720);
 if(graphics){const image=graphics.getDisplay().flatten();if(image.width>8192||image.height>8192)throw Error('invalid_size');if(image.width&&image.height){const scale=Math.min(1280/image.width,720/image.height);ctx.drawImage(image,(1280-image.width*scale)/2,(720-image.height*scale)/2,image.width*scale,image.height*scale)}}
 if(terminal){const w=Math.min(1280/terminal.cols,720/terminal.rows/2),h=w*2;ctx.font=`${h*.8}px monospace`;ctx.textBaseline='top';const b=terminal.buffer.active;for(let y=0;y<terminal.rows;y++){const line=b.getLine(b.viewportY+y);for(let x=0;x<terminal.cols;x++){const c=line?.getCell(x);if(!c||!c.getWidth())continue;const fg=color(c,!c.isInverse());ctx.fillStyle=color(c,!!c.isInverse());ctx.fillRect(x*w,y*h,c.getWidth()*w,h);ctx.fillStyle=fg;if(!c.isInvisible())ctx.fillText(c.getChars(),x*w,y*h)}}}
 return canvas.toDataURL('image/jpeg',0.85).split(',')[1];
};
