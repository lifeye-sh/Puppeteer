import SessionWatermark from '@/pages/access/SessionWatermark';
import type {SessionWatermark as WatermarkConfig} from '@/api/session-api';
import {useEffect,useRef,useState} from 'react';
import {Alert,Button,Space,Typography} from 'antd';
import {useSearchParams} from 'react-router-dom';
import {Terminal} from '@xterm/xterm';
import Guacamole from '@dushixiang/guacamole-common-js';
import requests from '@/api/core/requests';
import '@xterm/xterm/css/xterm.css';

type Page={format:string;state:string;bytes:number;total:number;next:number;events:{sequence:number;kind:string;payload:string}[]};
export default function BackendObserverPage(){
 const [watermark,setWatermark]=useState<WatermarkConfig>();
 const [params]=useSearchParams(),id=params.get('sessionId')??'';
 const [ticket]=useState(()=>new URLSearchParams(location.hash.slice(1)).get('ticket')??'');
 const [running,setRunning]=useState(false),[used,setUsed]=useState(false),[error,setError]=useState(''),[status,setStatus]=useState('尚未观察');
 const container=useRef<HTMLDivElement>(null);
 useEffect(()=>{if(location.hash)history.replaceState(null,'',location.pathname+location.search)},[]);
 useEffect(()=>{
  if(!running)return;
  let disposed=false,observer='',timer:ReturnType<typeof setTimeout>|undefined,terminal:Terminal|undefined,client:Guacamole.Client|undefined;
  let after=0,bytes=0;setError('');setStatus('正在验证观察权限…');container.current!.replaceChildren();
  const close=()=>{if(observer){const value=observer;observer='';void requests.delete(`/sessions/${id}/observers/${value}`,{errorMode:'local'}).catch(()=>{})}};
  const start=async()=>{
   const joined=await requests.post<{id:string;protocol:string;watermark:WatermarkConfig}>(`/sessions/${id}/observers`,{ticket},{errorMode:'local'});observer=joined.id;
   if(disposed){close();return}setUsed(true);setWatermark(joined.watermark);
   const graphics=['rdp','vnc'].includes(joined.protocol);let parser:any;
   if(graphics){const tunnel=new Guacamole.Tunnel();tunnel.connect=()=>tunnel.setState(Guacamole.Tunnel.State.OPEN);tunnel.disconnect=()=>tunnel.setState(Guacamole.Tunnel.State.CLOSED);tunnel.sendMessage=()=>{};client=new Guacamole.Client(tunnel);parser=new Guacamole.Parser();parser.oninstruction=(op:string,args:string[])=>tunnel.oninstruction?.(op,args);container.current!.appendChild(client.getDisplay().getElement());client.connect()}
   else {terminal=new Terminal({cols:80,rows:24,disableStdin:true,cursorBlink:false,scrollback:5000,theme:{background:'#1e1e1e'}});terminal.parser.registerOscHandler(52,()=>true);terminal.open(container.current!)}
   const poll=async()=>{
    try{
     const page=await requests.get<Page>(`/sessions/${id}/observers/${observer}/events?after=${after}`,{errorMode:'local'});if(disposed)return;
     if(page.bytes>67108864||page.total>200000||page.format!==(graphics?'guacamole-events-v1':'terminal-events-v1'))throw Error('输出超过观察上限或格式不匹配');
     for(const event of page.events){
      if(event.sequence!==after+1)throw Error('观察输出不连续，已停止');const data=Uint8Array.from(atob(event.payload),c=>c.charCodeAt(0));bytes+=data.length;if(bytes>67108864)throw Error('观察输出超过 64 MiB');
      if(graphics){if(event.kind!=='graphics')throw Error('图形输出格式错误');parser.receive(new TextDecoder().decode(data))}
      else if(event.kind==='resize'){const size=new TextDecoder().decode(data);if(!/^\d+,\d+$/.test(size))throw Error('窗口尺寸无效');const [cols,rows]=size.split(',').map(Number);if(cols<2||cols>500||rows<2||rows>300)throw Error('窗口尺寸超限');terminal!.resize(cols,rows)}
      else if(['output','exec-output'].includes(event.kind))await new Promise<void>(resolve=>terminal!.write(data,resolve));else throw Error('输出格式错误');
      if(disposed)return;after=event.sequence;
     }
     if(page.next!==after||(!page.events.length&&after<page.total))throw Error('观察输出缺失');
     if(page.state!=='writing'&&after===page.total){setStatus('源会话已结束');close();return}
     setStatus(after<page.total?'正在同步会话历史…':'只读观察中（约 1 秒延迟）');timer=setTimeout(()=>void poll(),after<page.total?50:1000);
    }catch(e){if(!disposed){setError((e as Error).message);setStatus('观察已停止');container.current?.replaceChildren();close()}}
   };await poll();
  };
  void start().catch(e=>{if(!disposed){setError((e as Error).message);setStatus('无法加入观察');close()}});
  return ()=>{disposed=true;clearTimeout(timer);close();client?.disconnect();terminal?.dispose()};
 },[running,id,ticket]);
 return <main style={{padding:20,minHeight:'100vh',background:'#15181e',color:'#ddd'}}><Space wrap><Typography.Title level={4} style={{color:'#eee',margin:0}}>会话只读观察</Typography.Title><Button disabled={running||!!ticket&&used||!id} onClick={()=>setRunning(true)}>开始观察</Button><Button disabled={!running} onClick={()=>{setRunning(false);setStatus('已退出观察')}}>退出观察</Button><Button href="/online-session">在线会话</Button><span>{status}</span></Space><Alert style={{margin:'16px 0'}} type="info" title="只读输出，不接管远端" description="从会话开始同步已保存的画面；无法发送键鼠、命令、文件或剪贴板。分享需要指定账号登录及资产授权，单次领取，撤销后停止输出。"/>{error&&<Alert type="error" title={error}/>}<div style={{position:'relative',isolation:'isolate'}}><SessionWatermark watermark={watermark}/><div ref={container} aria-label="只读观察画面" style={{isolation:'isolate',overflow:'auto',background:'#1e1e1e',minHeight:400,maxHeight:'75vh'}}/></div></main>;
}
