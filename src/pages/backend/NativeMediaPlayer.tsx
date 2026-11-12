import {useRef, useState} from 'react';
import {Alert, Button, Slider, Space} from 'antd';

export type MediaSegment = {sequence:number; startMs:number; durationMs:number; bytes:number};
type Props = {sessionId:string; segments:MediaSegment[]; interrupted:boolean};

const stamp = (ms:number) => {
    const seconds = Math.floor(ms/1000);
    return [Math.floor(seconds/3600),Math.floor(seconds/60)%60,seconds%60].map(v=>String(v).padStart(2,'0')).join(':');
};

export default function NativeMediaPlayer({sessionId,segments,interrupted}:Props) {
    const [index,setIndex]=useState(0);
    const [position,setPosition]=useState(0);
    const [error,setError]=useState(false);
    const player=useRef<HTMLVideoElement>(null);
    const pending=useRef({offset:0,play:false});
    const segment=segments[index];
    const last=segments[segments.length-1];
    const duration=last.startMs+last.durationMs;
    const url='/api/sessions/'+encodeURIComponent(sessionId)+'/recording/segments/'+segment.sequence;

    function seek(ms:number, play:boolean) {
        const found=segments.findIndex(s=>ms<s.startMs+s.durationMs);
        const target=found<0?segments.length-1:found;
        const offset=(ms-segments[target].startMs)/1000;
        setPosition(ms);
        pending.current={offset,play};
        if(target===index && player.current && player.current.readyState>=1) {
            player.current.currentTime=offset;
            if(play)void player.current.play().catch(()=>{});
        } else {
            setError(false);
            setIndex(target);
        }
    }

    return <section aria-label="原生 RDP 录像">
        {interrupted && <Alert type="warning" showIcon title="录屏中断，可回放已保存的部分"/>}
        <video key={url} ref={player} src={url} controls preload="metadata" playsInline
            style={{width:'100%',maxHeight:'65vh',background:'#111'}}
            onLoadedMetadata={e=>{
                e.currentTarget.currentTime=pending.current.offset;
                if(pending.current.play)void e.currentTarget.play().catch(()=>{});
            }}
            onTimeUpdate={e=>setPosition(segment.startMs+e.currentTarget.currentTime*1000)}
            onEnded={()=>{if(index+1<segments.length)seek(segments[index+1].startMs,true)}}
            onError={()=>setError(true)}/>
        {error && <Alert type="error" title="该 MP4 分段无法播放，请检查访问权限或录像完整性"/>}
        <Slider ariaLabelForHandle="整段录像进度" min={0} max={duration} step={100} value={position}
            tooltip={{formatter:v=>stamp(v??0)}} onChange={ms=>seek(ms,player.current?.paused===false)}/>
        <Space wrap><span>{stamp(position)} / {stamp(duration)}</span>
            <label>分段 <select aria-label="选择录像分段" value={index}
                onChange={e=>seek(segments[Number(e.target.value)].startMs,player.current?.paused===false)}>
                {segments.map((s,i)=><option key={s.sequence} value={i}>{i+1} · {stamp(s.startMs)}</option>)}
            </select></label>
            <Button href={url+'?download=1'} download>下载当前分段 MP4</Button>
        </Space>
    </section>;
}
