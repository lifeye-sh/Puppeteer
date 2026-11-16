import {graphicsQualities, graphicsSize, type GraphicsQuality} from './graphics-quality';
import useAccessMFA from './useAccessMFA';
import {coalesceMouse} from './graphics-mouse';
import SessionWatermark from '@/pages/access/SessionWatermark';
import type {SessionWatermark as WatermarkConfig} from '@/api/session-api';
import {useEffect,useRef,useState} from 'react';
import Guacamole from '@puppeteer/guacamole-common-js';
import requests from '@/api/core/requests';
import BackendRDPToolbox from './BackendRDPToolbox';
import type {TerminalAsset} from './BackendTerminalWorkspace';

const encode=(parts:unknown[])=>parts.map(p=>{const s=String(p);return `${Array.from(s).length}.${s}`}).join(',')+';';
const b64=(s:string)=>btoa(Array.from(new TextEncoder().encode(s),c=>String.fromCharCode(c)).join(''));
export default function BackendGraphicsSession({assetId,asset,connectRequest,onStatus}:{assetId:string;asset?:TerminalAsset;connectRequest:number;onStatus:(id:string,state:string)=>void}){
 const accessMFA=useAccessMFA();
 const [quality,setQuality]=useState<GraphicsQuality>('balanced');
 const [activeQuality,setActiveQuality]=useState<GraphicsQuality>('balanced');
 const [stages,setStages]=useState<{stage:string;label:string;status:string;elapsedMs:number}[]>([]);
 const [watermark,setWatermark]=useState<WatermarkConfig>();
 const container=useRef<HTMLDivElement>(null),close=useRef<()=>void>(()=>{}),generation=useRef(0);
 const clipboardSend=useRef<(text:string)=>boolean>(()=>false),keySend=useRef<(keys:number[])=>void>(()=>{});
 const [sessionId,setSessionId]=useState('');
 const [clipboardError,setClipboardError]=useState({text:'',sequence:0});
 const [remoteClipboard,setRemoteClipboard]=useState({text:'',sequence:0});
 const [state,setState]=useState('尚未连接'),[error,setError]=useState(''),[readonly]=useState(false),[busy,setBusy]=useState(false);
 useEffect(()=>onStatus(assetId,state),[assetId,state,onStatus]);
 useEffect(()=>()=>{generation.current++;close.current()},[]);
 const connect=async()=>{
  const attempt=++generation.current;close.current();setBusy(true);setError('');setStages([]);setActiveQuality(quality);setState('连接中');
  try{
   await accessMFA.verify();
 if(attempt!==generation.current)return;
 const reservation=await requests.post<{id:string;ticket:string;watermark:WatermarkConfig}>('/sessions',{assetId,readonly},{errorMode:'local'});
   if(attempt!==generation.current){void requests.post(`/sessions/${reservation.id}/terminate`,undefined,{errorMode:'local'}).catch(()=>{});return}
   setSessionId(reservation.id);setWatermark(reservation.watermark);
   const tunnel=new Guacamole.Tunnel(),client=new Guacamole.Client(tunnel),parser=new Guacamole.Parser();
   let socket:WebSocket|undefined,heartbeat:ReturnType<typeof setInterval>|undefined,ready=false,disposed=false,rawTransport=false;
   let resizeRemote=()=>{},flushMouse=()=>{};
   parser.oninstruction=(opcode:string,params:string[])=>{tunnel.oninstruction?.(opcode,params);if(opcode==='sync'){setBusy(false);setState('已连接');resizeRemote()}};
   tunnel.connect=()=>{
    socket=new WebSocket(`${location.protocol==='https:'?'wss:':'ws:'}//${location.host}/api/sessions/${reservation.id}/ws?graphicsTransport=raw&graphicsDiagnostics=1${asset?.protocol==='rdp'?`&graphicsQuality=${quality}`:''}`);
    socket.onopen=()=>{socket?.send('3'+reservation.ticket);reservation.ticket='';heartbeat=setInterval(()=>{if(socket?.readyState===WebSocket.OPEN)socket.send('9'+Date.now())},5000)};
    socket.onmessage=event=>{
     if(disposed||attempt!==generation.current)return;const data=String(event.data);
     if(data[0]==='3'){ready=true;tunnel.setState(Guacamole.Tunnel.State.OPEN);setState('等待桌面')}
     else if(data[0]==='P'){try{const step=JSON.parse(data.slice(1));if(typeof step.stage==='string'&&typeof step.label==='string'&&typeof step.status==='string'&&typeof step.elapsedMs==='number')setStages(prev=>[...prev.filter(p=>p.stage!==step.stage),step])}catch{}}
     else if(data[0]==='G'){rawTransport=true;try{parser.receive(data.slice(1))}catch{setError('图形数据解析失败');close.current()}}
     else if(data[0]==='7'){try{parser.receive(new TextDecoder().decode(Uint8Array.from(atob(data.slice(1)),c=>c.charCodeAt(0))))}catch{setError('图形数据解析失败');close.current()}}
     else if(data[0]==='8'){try{const text=new TextDecoder('utf-8',{fatal:true}).decode(Uint8Array.from(atob(data.slice(1)),c=>c.charCodeAt(0)));setRemoteClipboard(v=>({text,sequence:v.sequence+1}))}catch{setError('剪贴板文本格式无效')}}
     else if(data[0]==='6')setClipboardError(v=>({text:data.slice(1),sequence:v.sequence+1}));
     else if(data[0]==='0')setError(data.slice(1));
    };
    socket.onclose=()=>{ready=false;clearInterval(heartbeat);tunnel.setState(Guacamole.Tunnel.State.CLOSED);if(!disposed&&attempt===generation.current){setState('已断开');setBusy(false)}};
    socket.onerror=()=>{if(!disposed)setError('图形连接失败，请检查访问记录。')};
   };
   tunnel.disconnect=()=>{ready=false;clearInterval(heartbeat);socket?.close();tunnel.setState(Guacamole.Tunnel.State.CLOSED)};
   tunnel.sendMessage=(...parts:unknown[])=>{if(parts[0]==='key')flushMouse();if(ready&&socket?.readyState===WebSocket.OPEN)socket.send(rawTransport?'G'+encode(parts):'7'+b64(encode(parts)))};
   const display=client.getDisplay(),element=display.getElement() as HTMLElement;element.tabIndex=0;element.style.outline='none';
   const viewport=container.current!;viewport.replaceChildren(element);
   // Fit while waiting for RDP resolution updates, and for fixed-size VNC targets.
   // Hidden tabs have zero size; retain their scale until they become visible again.
   const fit=()=>{const w=viewport.clientWidth,h=viewport.clientHeight,dw=display.getWidth(),dh=display.getHeight();if(!disposed&&w>0&&h>0&&dw>0&&dh>0)display.scale(Math.min(w/dw,h/dh))};
   let resizeTimer:ReturnType<typeof setTimeout>|undefined,lastSize='',lastResizeAt=0,resizeAttempts=0;
   resizeRemote=()=>{if(disposed||!ready||readonly||asset?.protocol!=='rdp')return;const {width:w,height:h}=graphicsSize(quality,viewport.clientWidth,viewport.clientHeight);if(w<200||h<200)return;const key=`${w}x${h}`;if(key===lastSize){if(Math.abs(display.getWidth()-w)<4&&Math.abs(display.getHeight()-h)<2)return;if(Date.now()-lastResizeAt<2000||resizeAttempts>=5)return}else resizeAttempts=0;lastSize=key;lastResizeAt=Date.now();resizeAttempts++;clearTimeout(resizeTimer);resizeTimer=setTimeout(()=>{if(!disposed&&ready&&viewport.clientWidth>0&&viewport.clientHeight>0)client.sendSize(w,h);else lastSize=''},250)};
   const resizeObserver=new ResizeObserver(()=>{fit();resizeRemote()});resizeObserver.observe(viewport);display.onresize=fit;fit();
   const keyboard=new Guacamole.Keyboard(element),mouse=new Guacamole.Mouse(element);
   keyboard.onkeydown=(keysym:number)=>{if(!readonly&&ready)client.sendKeyEvent(1,keysym);return false};
   keyboard.onkeyup=(keysym:number)=>{if(!readonly&&ready)client.sendKeyEvent(0,keysym)};
   const sendMouse=(s:Guacamole.Mouse.State)=>{
    if(readonly||!ready)return;
    const scale=display.getScale(),width=display.getWidth(),height=display.getHeight();
    if(!Number.isFinite(scale)||scale<=0||width<1||height<1||!Number.isFinite(s.x)||!Number.isFinite(s.y))return;
    // Fractional layout offsets and dragging outside the display can yield -1.
    // Clamp in remote pixels, preserving button releases at the display edge.
    client.sendMouseState({...s,x:Math.max(0,Math.min(width-1,8192,Math.floor(s.x/scale))),y:Math.max(0,Math.min(height-1,8192,Math.floor(s.y/scale)))},false);
   };
   const moves=coalesceMouse<Guacamole.Mouse.State>(s=>{if(!disposed)sendMouse(s)});
   flushMouse=moves.flush;
   mouse.onmousemove=(s:Guacamole.Mouse.State)=>moves.move({...s});
   mouse.onmousedown=mouse.onmouseup=(s:Guacamole.Mouse.State)=>{flushMouse();sendMouse(s)};
   const focus=()=>element.focus();const blur=()=>{flushMouse();keyboard.reset()};element.addEventListener('mousedown',focus);element.addEventListener('blur',blur);
   client.onerror=()=>{if(!disposed)setError('远程桌面连接已中断，请查看访问记录。')};
   close.current=()=>{if(disposed)return;disposed=true;moves.clear();clipboardSend.current=()=>false;keySend.current=()=>{};setRemoteClipboard(v=>({text:'',sequence:v.sequence+1}));clearTimeout(resizeTimer);resizeObserver.disconnect();display.onresize=null;keyboard.reset();keyboard.onkeydown=keyboard.onkeyup=null;mouse.onmousedown=mouse.onmouseup=mouse.onmousemove=null;element.removeEventListener('mousedown',focus);element.removeEventListener('blur',blur);client.disconnect();tunnel.disconnect();void requests.post(`/sessions/${reservation.id}/terminate`,undefined,{errorMode:'local'}).catch(()=>{})};
   clipboardSend.current=text=>{if(!ready||readonly||socket?.readyState!==WebSocket.OPEN||new TextEncoder().encode(text).length>16384)return false;try{socket.send('8'+b64(text));return true}catch{return false}};
   keySend.current=keys=>{if(!ready||readonly)return;try{for(const key of keys)client.sendKeyEvent(1,key)}finally{for(const key of [...keys].reverse())client.sendKeyEvent(0,key)}};
   client.connect();
  }catch(e){if(attempt===generation.current){setError((e as Error).message);setState('连接失败');setBusy(false)}}
 };
    useEffect(()=>{if(busy||state==='已连接')return;const timer=setTimeout(()=>void connect(),0);return ()=>clearTimeout(timer)},[connectRequest]);
 return <div style={{height:'100%',minHeight:0,minWidth:0,display:'flex',flexDirection:'column',background:'#1e1e1e',color:'#ddd'}}>
 {accessMFA.dialog}

  {asset?.protocol==='rdp'&&<div style={{padding:'6px 12px',display:'flex',alignItems:'center',gap:8,flexWrap:'wrap'}}>
   <label>画质 <select aria-label="远程桌面画质" value={quality} disabled={busy} onChange={e=>setQuality(e.target.value as GraphicsQuality)} style={{color:'#222'}}>{Object.entries(graphicsQualities).map(([key,q])=><option key={key} value={key}>{q.label}</option>)}</select></label>
   <span>{graphicsQualities[quality].detail}；重连生效</span>
   <button disabled={busy} onClick={()=>void connect()} style={{color:'#222'}}>重连{quality!==activeQuality?'应用画质':''}</button>
  </div>}
  <details style={{padding:'4px 12px'}}><summary aria-live="polite">{state} · 连接诊断</summary>{stages.map(s=><div key={s.stage}>{s.label}：{s.status==='running'?'进行中':s.status==='timeout'?'超时':s.status==='failed'?'失败':'完成'}{s.status!=='running'&&`（${s.elapsedMs} ms）`}</div>)}<div>协议协商完成后仍需等待远端认证和首帧；失败请结合访问记录与图形服务日志检查。</div></details>
  {error&&<div role="alert" style={{padding:12,color:'#ff9b9b'}}>{error}</div>}
  <div aria-label="远程桌面" style={{position:'relative',isolation:'isolate',flex:1,overflow:'hidden',minHeight:0,minWidth:0}}><div ref={container} style={{position:'absolute',inset:0,overflow:'hidden'}}/><SessionWatermark watermark={watermark} zIndex={2}/>{asset?.protocol==='rdp'&&<BackendRDPToolbox sessionId={sessionId} connected={state==='已连接'} readonly={readonly} remoteClipboard={remoteClipboard} clipboardError={clipboardError} onPaste={text=>clipboardSend.current(text)} onKeys={keys=>keySend.current(keys)}/>}</div>
 </div>;
}
