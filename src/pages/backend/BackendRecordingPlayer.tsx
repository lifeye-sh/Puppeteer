import {useEffect, useRef, useState} from 'react';
import {Alert, Button, Empty, Progress, Select, Slider, Space} from 'antd';
import {Terminal} from '@xterm/xterm';
import BackendGraphicsPlayback from './BackendGraphicsPlayback';
import NativeMediaPlayer, {type MediaSegment} from './NativeMediaPlayer';
import requests from '@/api/core/requests';
import '@xterm/xterm/css/xterm.css';

type RecordingEvent={sequence:number;elapsedMs:number;kind:string;payload:string};
type RecordingPage={format:string;state:string;bytes:number;total:number;durationMs:number;next:number;events:RecordingEvent[];segments?:MediaSegment[]};
type LoadedRecording=Omit<RecordingPage,'events'> & {events:(Omit<RecordingEvent,'payload'> & {payload:Uint8Array})[]};
const time=(ms:number)=>`${Math.floor(ms/60000).toString().padStart(2,'0')}:${Math.floor(ms/1000%60).toString().padStart(2,'0')}`;

export default function BackendRecordingPlayer({sessionId}:{sessionId:string}) {
    const [recording,setRecording]=useState<LoadedRecording>();
    const [error,setError]=useState('');const [progress,setProgress]=useState(0);const [attempt,setAttempt]=useState(0);
    useEffect(()=>{
        let cancelled=false;
        setRecording(undefined);setError('');setProgress(0);
        void (async()=>{
            let after=0,bytes=0;const events:LoadedRecording['events']=[];
            do {
                const page=await requests.get<RecordingPage>(`/sessions/${sessionId}/recording?after=${after}`,{errorMode:'local'});
                if(cancelled)return;
                if(page.format==='native-rdp-mp4-v1'){
                    setRecording({...page,events:[]});return;
                }
                if(page.bytes>67108864||page.total>200000)throw new Error('录屏超过浏览器回放上限（64 MiB / 20 万事件），请联系管理员。');
                for(const event of page.events){
                    if(event.sequence!==after+1||!(page.format==='guacamole-events-v1'?['graphics']:['output','exec-output','resize']).includes(event.kind))throw new Error('录屏事件不连续或格式不受支持，无法完整回放。');
                    const payload=Uint8Array.from(atob(event.payload),c=>c.charCodeAt(0));bytes+=payload.length;
                    if(bytes>67108864)throw new Error('录屏超过浏览器内存上限。');
                    events.push({...event,payload});after=event.sequence;
                }
                setProgress(page.total?Math.round(after/page.total*100):100);
                if(after===page.total){
                    if(bytes!==page.bytes)throw new Error('录屏数据长度不一致，无法完整回放。');
                    setRecording({...page,events});return;
                }
                if(!page.events.length||page.next!==after)throw new Error('录屏数据缺失，请刷新后重试。');
            }while(!cancelled);
        })().catch(e=>{if(!cancelled)setError((e as Error).message)});
        return ()=>{cancelled=true};
    },[sessionId,attempt]);
    if(error)return <Alert type="error" title="无法加载录屏" description={error} action={<Button onClick={()=>setAttempt(v=>v+1)}>重试</Button>}/>;
    if(!recording)return <div style={{padding:24}}>正在加载录屏…<Progress percent={progress}/></div>;
    if(recording.segments?.length)return <NativeMediaPlayer sessionId={sessionId} segments={recording.segments} interrupted={recording.state==='interrupted'}/>;
    if(!recording.events.length)return <Empty description="此会话没有录屏内容（可能未成功连接）"/>;
    return <><Alert type={recording.state==='interrupted'?'warning':'info'} showIcon title={recording.state==='interrupted'?'录屏中断：仅回放已保存的部分':'只读录屏回放'} description="按原始时间还原会话输出；回放操作不会连接主机或执行命令。"/>{recording.format==='guacamole-events-v1'?<BackendGraphicsPlayback key={`${sessionId}-${attempt}`} events={recording.events}/>:<Playback key={`${sessionId}-${attempt}`} recording={recording}/>}</>;
}

function Playback({recording}:{recording:LoadedRecording}) {
    const container=useRef<HTMLDivElement>(null);
    const seek=useRef<(ms:number)=>Promise<void>>(async()=>{});
    const [position,setPosition]=useState(0);const positionRef=useRef(0);
    const [playing,setPlaying]=useState(false);const [speed,setSpeed]=useState(1);const [seeking,setSeeking]=useState(false);
    const [error,setError]=useState('');
    const duration=recording.durationMs;
    useEffect(()=>{
        const terminal=new Terminal({cols:80,rows:24,disableStdin:true,cursorBlink:false,scrollback:5000,fontSize:14,fontFamily:'Consolas, "Liberation Mono", monospace',theme:{background:'#1e1e1e',foreground:'#e5e7eb'},allowProposedApi:false});
        // Recorded escape sequences must never write to the viewer's clipboard.
        terminal.parser.registerOscHandler(52,()=>true);
        terminal.open(container.current!);
        let disposed=false,index=0,last=-1,queue=Promise.resolve();
        const render=async(target:number)=>{
            if(disposed)return;
            if(target<last){terminal.reset();terminal.resize(80,24);index=0}
            let pending:Uint8Array[]=[];let size=0;
            const flush=async()=>{
                if(!size||disposed)return;
                const data=new Uint8Array(size);let offset=0;for(const chunk of pending){data.set(chunk,offset);offset+=chunk.length}
                pending=[];size=0;
                await new Promise<void>(resolve=>terminal.write(data,resolve));
            };
            while(index<recording.events.length&&recording.events[index].elapsedMs<=target&&!disposed){
                const event=recording.events[index++];
                if(event.kind==='resize'){
                    await flush();if(disposed)return;
                    const value=new TextDecoder().decode(event.payload);
                    if(!/^\d+,\d+$/.test(value))throw new Error('录屏窗口尺寸无效');
                    const [cols,rows]=value.split(',').map(Number);
                    if(cols<2||cols>500||rows<2||rows>300)throw new Error('录屏窗口尺寸超出范围');
                    terminal.resize(cols,rows);
                }else{pending.push(event.payload);size+=event.payload.length;if(size>=262144)await flush()}
            }
            await flush();last=target;
        };
        seek.current=(ms)=>{queue=queue.then(()=>render(ms));return queue};
        void seek.current(0).catch(e=>setError((e as Error).message));
        return ()=>{disposed=true;seek.current=async()=>{};terminal.dispose()};
    },[recording]);
    useEffect(()=>{
        if(!playing)return;
        let cancelled=false,frame=0;const start=performance.now(),from=positionRef.current;
        const tick=async()=>{
            const next=Math.min(duration,from+(performance.now()-start)*speed);
            try{await seek.current(next)}catch(e){if(!cancelled){setError((e as Error).message);setPlaying(false)}return}
            if(cancelled)return;
            positionRef.current=next;setPosition(next);
            if(next>=duration)setPlaying(false);else frame=requestAnimationFrame(()=>void tick());
        };
        frame=requestAnimationFrame(()=>void tick());
        return ()=>{cancelled=true;cancelAnimationFrame(frame)};
    },[playing,speed,duration]);
    const jump=async(ms:number)=>{
        setPlaying(false);setSeeking(true);
        try{await seek.current(ms);positionRef.current=ms;setPosition(ms)}catch(e){setError((e as Error).message)}finally{setSeeking(false)}
    };
    return <div style={{marginTop:16}}>
        {error&&<Alert type="error" title="回放失败" description={error}/>}
        <Space wrap style={{marginBottom:12}}>
            <Button type="primary" disabled={seeking||!!error} onClick={()=>{if(playing)setPlaying(false);else if(position>=duration)void jump(0).then(()=>setPlaying(true));else setPlaying(true)}}>{playing?'暂停':'播放'}</Button>
            <Button disabled={seeking||!!error} onClick={()=>void jump(0)}>回到开头</Button>
            <Select aria-label="回放速度" value={speed} onChange={setSpeed} options={[0.5,1,2,4,8].map(value=>({value,label:`${value}×`}))} style={{width:85}}/>
            <span role="timer">{time(position)} / {time(duration)}</span>{seeking&&<span>正在定位…</span>}
        </Space>
        <Slider aria-label="录屏进度" min={0} max={Math.max(1,duration)} step={1} value={position} disabled={seeking||!!error} tooltip={{formatter:v=>time(v??0)}} onChange={v=>{setPlaying(false);setPosition(v)}} onChangeComplete={v=>void jump(v)}/>
        <div aria-label="录屏终端" style={{background:'#1e1e1e',padding:12,overflow:'auto',maxHeight:'60vh',minHeight:250,borderRadius:8}}><div ref={container}/></div>
    </div>;
}
