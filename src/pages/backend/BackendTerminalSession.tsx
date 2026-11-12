import useAccessMFA from './useAccessMFA';
import SessionWatermark from '@/pages/access/SessionWatermark';
import type {SessionWatermark as WatermarkConfig} from '@/api/session-api';
import Modal from './BackendFormModal';
import {useEffect, useRef, useState} from 'react';
import {Eraser, BookOpen, FolderOpen, History, Search, SquareTerminal, Unplug, X} from 'lucide-react';
import {ConfigProvider} from 'antd';
import zhCN from 'antd/locale/zh_CN';
import BackendFiles from './BackendFiles';
import BackendSnippets from './BackendSnippets';
import {SearchAddon} from '@xterm/addon-search';
import requests from '@/api/core/requests';
import {TerminalRuntime} from '@/pages/access/terminal/terminal-runtime';
import {Message} from '@/pages/access/Terminal';
import type {TerminalAsset} from './BackendTerminalWorkspace';
import '@xterm/xterm/css/xterm.css';

export default function BackendTerminalSession({assetId,asset,connectRequest,active,fontSize,onStatus}:{assetId:string;asset?:TerminalAsset;connectRequest:number;active:boolean;fontSize:number;onStatus:(id:string,state:string)=>void}) {
 const accessMFA=useAccessMFA();
 const [watermark,setWatermark]=useState<WatermarkConfig>();
    const container=useRef<HTMLDivElement>(null);
    const runtime=useRef<TerminalRuntime|null>(null);
    const searchAddon=useRef<SearchAddon|null>(null);
    const current=useRef('');
    const attached=useRef(false);
    const generation=useRef(0);
    const visible=useRef(active);visible.current=active;
    const [id,setId]=useState('');
    const [state,setState]=useState('尚未连接');
    const [error,setError]=useState('');
    const [readonly]=useState(false);
    const [busy,setBusy]=useState(false);
    const [command,setCommand]=useState('');
    const [executing,setExecuting]=useState(false);
    const [result,setResult]=useState('');
    const [commandOpen,setCommandOpen]=useState(false);
    const [searchOpen,setSearchOpen]=useState(false);
    const [search,setSearch]=useState('');
    const [ping,setPing]=useState<number>();
    const [filesOpen,setFilesOpen]=useState(false);
    const [snippetsOpen,setSnippetsOpen]=useState(false);
    useEffect(()=>{if(state!=='已连接')setFilesOpen(false)},[state]);
    useEffect(()=>onStatus(assetId,state),[assetId,state,onStatus]);
    useEffect(()=>()=>{generation.current++;if(current.current&&!attached.current)void requests.post(`/sessions/${current.current}/terminate`,undefined,{errorMode:'local'}).catch(()=>{});runtime.current?.dispose()},[]);
    useEffect(()=>{if(!active)return;const frame=requestAnimationFrame(()=>{runtime.current?.fit();runtime.current?.focus()});return ()=>cancelAnimationFrame(frame)},[active,commandOpen]);
    useEffect(()=>{if(runtime.current){runtime.current.terminal.options.fontSize=fontSize;if(active)runtime.current.fit()}},[fontSize,active]);
    const connect=async()=>{
        const attempt=++generation.current;
        setBusy(true);setError('');setResult('');setState('预约中');setPing(undefined);
        runtime.current?.dispose();runtime.current=null;attached.current=false;
        try{
            await accessMFA.verify();
 if(attempt!==generation.current)return;
 const reservation=await requests.post<{id:string;ticket:string;watermark:WatermarkConfig}>('/sessions',{assetId,readonly},{errorMode:'local'});
            if(attempt!==generation.current){void requests.post(`/sessions/${reservation.id}/terminate`,undefined,{errorMode:'local'}).catch(()=>{});return}
            current.current=reservation.id;setId(reservation.id);setWatermark(reservation.watermark);
            if(!container.current)return;
            container.current.replaceChildren();
            const terminal=new TerminalRuntime({container:container.current,readOnly:readonly,sendResize:false,
                terminalOptions:{fontSize,cursorBlink:!readonly,scrollback:5000,fontFamily:'Consolas, "Liberation Mono", monospace',theme:{background:'#1e1e1e',foreground:'#e5e7eb',cursor:'#d4d4d4',selectionBackground:'#264f78'}}});
            const finder=new SearchAddon();terminal.terminal.loadAddon(finder);searchAddon.current=finder;
            runtime.current=terminal;let ready=false;
            terminal.setInputHandler(data=>{if(ready&&!readonly&&attempt===generation.current)terminal.sendMessage(1,data)});
            const resize=()=>{if(ready&&!readonly&&visible.current){const {cols,rows}=terminal.terminal;if(cols>=2&&rows>=2)terminal.sendMessage(2,`${Math.min(cols,500)},${Math.min(rows,300)}`)}};
            terminal.terminal.onResize(resize);
            setState('连接中');
            terminal.connect(`${location.protocol==='https:'?'wss:':'ws:'}//${location.host}/api/sessions/${reservation.id}/ws`,{
                onOpen:()=>{terminal.sendMessage(3,reservation.ticket);reservation.ticket=''},
                onMessage:event=>{
                    if(attempt!==generation.current)return;
                    const message=Message.parse(String(event.data));
                    if(message.type===3){ready=true;attached.current=true;setState('已连接');setBusy(false);if(visible.current){terminal.fit();terminal.focus();resize()}}
                    else if(message.type===6)terminal.terminal.write(Uint8Array.from(atob(message.content),ch=>ch.charCodeAt(0)));
                    else if(message.type===1)terminal.terminal.write(message.content);
                    else if(message.type===0)setError(message.content);
                    else if(message.type===4){ready=false;setState('已结束')}
                    else if(message.type===9){const sent=Number(message.content);if(Number.isFinite(sent))setPing(Math.max(0,Date.now()-sent))}
                },
                onError:()=>{if(attempt===generation.current)setError('终端连接失败，请查看访问记录。')},
                onClose:()=>{ready=false;if(attempt===generation.current){current.current='';attached.current=false;setState('已断开');setBusy(false)}},
            });
        }catch(e){if(attempt===generation.current){setError((e as Error).message);setState('连接失败');setBusy(false)}}
    };
    const disconnect=async()=>{
        const attempt=++generation.current;const terminal=runtime.current;setState('断开中');setBusy(true);
        const sessionId=current.current;current.current='';
        try{if(sessionId)await requests.post(`/sessions/${sessionId}/terminate`,undefined,{errorMode:'local'})}catch(e){if(attempt===generation.current)setError((e as Error).message)}
        finally{terminal?.closeSocket();if(attempt===generation.current){attached.current=false;setState('已断开');setBusy(false)}}
    };
    const execute=async()=>{
        setExecuting(true);setError('');
        try{const response=await requests.post<{message:string}>(`/sessions/${id}/commands`,{command},{errorMode:'local'});setResult(response.message);setCommand('')}
        catch(e){setError((e as Error).message)}finally{setExecuting(false)}
    };
    useEffect(()=>{if(busy||state==='已连接')return;const timer=setTimeout(()=>void connect(),0);return ()=>clearTimeout(timer)},[connectRequest]);
    const connected=state==='已连接';
    return <section id={`terminal-panel-${assetId}`} role="tabpanel" aria-labelledby={`terminal-tab-${assetId}`} className="nt-session-panel" style={{display:active?'flex':'none'}}>
 {accessMFA.dialog}
        {error&&<div role="alert" className="nt-session-error"><span>{error}</span><button aria-label="关闭错误提示" onClick={()=>setError('')}><X size={14}/></button></div>}
        <div className="nt-terminal-body"><div className="nt-terminal-main">
            <div ref={container} className="nt-terminal-canvas"/><SessionWatermark watermark={watermark} zIndex={2}/>
            {!id&&!busy&&<div className="nt-terminal-welcome"><SquareTerminal size={26}/><span>{asset?.name??'SSH 终端'}</span><p>双击资产或标签开始连接；Telnet 连接后手动登录</p></div>}
            {connected&&ping!==undefined&&<span className="nt-terminal-ping">Ping <b>{ping} ms</b></span>}
            {searchOpen&&<form className="nt-terminal-search" onSubmit={e=>{e.preventDefault();searchAddon.current?.findNext(search)}}><Search size={14}/><input autoFocus aria-label="搜索终端内容" placeholder="搜索终端内容" value={search} onChange={e=>setSearch(e.target.value)}/><button type="submit">下一个</button><button type="button" aria-label="关闭终端搜索" onClick={()=>{setSearchOpen(false);searchAddon.current?.clearDecorations();runtime.current?.focus()}}><X size={14}/></button></form>}
        </div><div className="nt-terminal-tools" aria-label="终端工具栏">
            <button title="SFTP 文件管理" aria-label="打开文件管理" disabled={!connected||asset?.protocol!=='ssh'} onClick={()=>setFilesOpen(true)}><FolderOpen size={17}/></button>
            <button className={searchOpen?'is-selected':''} title="搜索终端内容" aria-label="搜索终端内容" onClick={()=>setSearchOpen(v=>!v)}><Search size={17}/></button>
            <button title="清屏（不删除审计记录）" aria-label="清屏" onClick={()=>runtime.current?.terminal.clear()}><Eraser size={17}/></button>
            <button title="命令片段" aria-label="选择命令片段" disabled={asset?.protocol!=='ssh'} onClick={()=>setSnippetsOpen(true)}><BookOpen size={17}/></button>
            <button className={commandOpen?'is-selected':''} title="执行命令" aria-label="打开命令面板" disabled={asset?.protocol!=='ssh'} onClick={()=>setCommandOpen(v=>!v)}><SquareTerminal size={17}/></button>
            {id&&<a title="本次访问记录" aria-label="本次访问记录" href={`/access-log?sessionId=${id}`} target="_blank" rel="noopener noreferrer"><History size={17}/></a>}
            <button title="断开连接" aria-label="断开连接" disabled={!connected&&!busy} onClick={()=>void disconnect()}><Unplug size={17}/></button>
        </div></div>
        {commandOpen&&<div className="nt-command-panel"><div><strong>执行命令</strong><button className="nt-icon-button" aria-label="关闭命令面板" onClick={()=>setCommandOpen(false)}><X size={15}/></button></div><p>独立执行并记录退出码，不继承终端的当前目录。限 10 秒，请勿输入秘密。</p><form onSubmit={e=>{e.preventDefault();void execute()}}><textarea rows={3} aria-label="审计命令" value={command} onChange={e=>setCommand(e.target.value)} maxLength={4096} placeholder="例如：whoami; pwd" disabled={readonly||!connected}/><button className="nt-connect-button" disabled={executing||readonly||!connected||!command.trim()}>{executing?'执行中…':'执行命令'}</button></form>{result&&<p role="status">{result}</p>}</div>}
        <ConfigProvider locale={zhCN}><Modal open={snippetsOpen} title="选择命令片段" width={850} footer={null} onCancel={()=>setSnippetsOpen(false)} destroyOnHidden>{snippetsOpen&&<BackendSnippets onInsert={text=>{setCommand(text);setCommandOpen(true);setSnippetsOpen(false);setResult('片段已填入，请检查后手动执行。')}}/>}</Modal><Modal open={filesOpen} title={`文件管理 · ${asset?.name??'SSH'}`} width={1150} footer={null} onCancel={()=>setFilesOpen(false)} destroyOnHidden>{filesOpen&&connected&&<BackendFiles key={id} sessionId={id} readonly={readonly}/>}</Modal></ConfigProvider>
    </section>;
}
