import {useEffect,useRef,useState} from 'react';
import {Button,Slider,Space,Alert} from 'antd';
import Guacamole from '@dushixiang/guacamole-common-js';

export default function BackendGraphicsPlayback({events}:{events:{payload:Uint8Array}[]}){
 const container=useRef<HTMLDivElement>(null),player=useRef<Guacamole.SessionRecording|null>(null);
 const [position,setPosition]=useState(0),[duration,setDuration]=useState(0),[playing,setPlaying]=useState(false),[error,setError]=useState('');
 useEffect(()=>{
  const url=URL.createObjectURL(new Blob(events.map(e=>new Uint8Array(e.payload)),{type:'application/octet-stream'}));
  const recording=new Guacamole.SessionRecording(new Guacamole.StaticHTTPTunnel(url));player.current=recording;
  container.current!.replaceChildren(recording.getDisplay().getElement());
  recording.onprogress=()=>setDuration(recording.getDuration());recording.onseek=(ms:number)=>setPosition(ms);
  recording.onplay=()=>setPlaying(true);recording.onpause=()=>setPlaying(false);recording.onerror=()=>setError('图形录屏无法解析');
  recording.connect();
  return ()=>{recording.pause();recording.disconnect();player.current=null;URL.revokeObjectURL(url)};
 },[events]);
 return <div style={{marginTop:16}}>{error&&<Alert type="error" title={error}/>}<Space><Button disabled={!duration||!!error} onClick={()=>playing?player.current?.pause():player.current?.play()}>{playing?'暂停':'播放'}</Button><Button onClick={()=>{player.current?.pause();player.current?.seek(0)}}>回到开头</Button><span>{(position/1000).toFixed(1)} / {(duration/1000).toFixed(1)} 秒</span></Space><Slider min={0} max={Math.max(1,duration)} value={position} onChange={v=>{player.current?.pause();setPosition(v)}} onChangeComplete={v=>player.current?.seek(v)}/><div ref={container} aria-label="图形录屏" style={{isolation:'isolate',overflow:'auto',background:'#1e1e1e',maxHeight:'60vh',minHeight:250}}/></div>;
}
