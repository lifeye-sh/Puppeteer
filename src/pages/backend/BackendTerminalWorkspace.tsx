import {useBackendBranding} from '@/hook/useBackendBranding';
import {useCallback, useEffect, useRef, useState} from 'react';
import {useInfiniteQuery, useQuery} from '@tanstack/react-query';
import {useSearchParams} from 'react-router-dom';
import {ChevronDown, Folder, Maximize, PanelLeftClose, PanelLeftOpen, RefreshCw, Search, Settings2, SquareTerminal, X} from 'lucide-react';
import requests from '@/api/core/requests';
import BackendGraphicsSession from './BackendGraphicsSession';
import BackendTerminalSession from './BackendTerminalSession';
import './backend-terminal.css';

export type TerminalAsset = {protocol:string;id:string; name:string; host:string; port:number; groupId:string; alias:string};
type AssetPage = {items:TerminalAsset[]; total:number};
export default function BackendTerminalWorkspace() {
    const [params] = useSearchParams();
    const initialId = params.get('assetId') ?? '';
    const [tabs,setTabs] = useState<string[]>(initialId ? [initialId] : []);
    const [activeId,setActiveId] = useState(initialId);
    const [connections,setConnections]=useState<Record<string,number>>({});
    const [statuses,setStatuses] = useState<Record<string,string>>({});
    const [sidebar,setSidebar] = useState(true);
    const [displayFullscreen,setDisplayFullscreen]=useState(false);
    useEffect(()=>setDisplayFullscreen(false),[activeId]);
    useEffect(()=>{if(!displayFullscreen)return;const exit=(e:KeyboardEvent)=>{if(e.key==="Escape"){e.preventDefault();e.stopPropagation();setDisplayFullscreen(false)}};window.addEventListener("keydown",exit,true);return ()=>window.removeEventListener("keydown",exit,true)},[displayFullscreen]);
    const [settings,setSettings] = useState(false);
    const [fontSize,setFontSize] = useState(14);
    const [search,setSearch] = useState('');
    const [filter,setFilter] = useState('');
    const [notice,setNotice] = useState('');
    const [collapsed,setCollapsed] = useState<Record<string,boolean>>({});
    const [known,setKnown] = useState<Record<string,TerminalAsset>>({});
    const displayPanels = useRef<HTMLDivElement>(null);
    useEffect(() => {const timer=setTimeout(()=>setFilter(search),250);return ()=>clearTimeout(timer)},[search]);
    const assets = useInfiniteQuery({
        queryKey:['r04-workspace-assets',filter],initialPageParam:1,
        queryFn:({pageParam})=>requests.get<AssetPage>(`/portal/assets?current=${pageParam}&pageSize=100&search=${encodeURIComponent(filter)}`,{errorMode:'local'}),
        getNextPageParam:(last,pages)=>pages.reduce((n,p)=>n+p.items.length,0)<last.total ? pages.length+1 : undefined,
        refetchInterval:10000,retry:false,
    });
    const facets = useQuery({queryKey:['r04-workspace-facets'],queryFn:()=>requests.get<{groups:{id:string;name:string}[]}>('/portal/facets',{errorMode:'local'}),refetchInterval:10000,retry:false});
    const initial = useQuery({queryKey:['r04-workspace-initial',initialId],enabled:!!initialId,queryFn:()=>requests.get<TerminalAsset>(`/portal/assets/${encodeURIComponent(initialId)}`,{errorMode:'local'}),retry:false});
    useEffect(()=>{
        const rows=assets.data?.pages.flatMap(p=>p.items)??[];
        if(initial.data)rows.push(initial.data);
        if(rows.length)setKnown(previous=>({...previous,...Object.fromEntries(rows.map(v=>[v.id,v]))}));
    },[assets.data,initial.data]);
    const rows=assets.data?.pages.flatMap(p=>p.items)??[];
    const groups=Array.from(new Set(rows.map(v=>v.groupId))).map(id=>({id,name:facets.data?.groups.find(g=>g.id===id)?.name??(id?'已授权分组':'未分组'),assets:rows.filter(v=>v.groupId===id)}));
    const open=(id:string)=>{setNotice('');setTabs(previous=>previous.includes(id)?previous:[...previous,id]);setActiveId(id)};
    const start=(id:string)=>{open(id);setConnections(v=>({...v,[id]:(v[id]??0)+1}))};
    const close=(id:string)=>{const next=tabs.filter(v=>v!==id);setTabs(next);if(activeId===id)setActiveId(next[Math.max(0,tabs.indexOf(id)-1)]??'');setStatuses(previous=>{const result={...previous};delete result[id];return result})};
    const statusChanged=useCallback((id:string,state:string)=>setStatuses(previous=>previous[id]===state?previous:{...previous,[id]:state}),[]);
    const fullscreen=async()=>{try{if(document.fullscreenElement)await document.exitFullscreen();else {const panel=displayPanels.current?.querySelector(`[id="terminal-panel-${activeId}"]`);const display=panel?.querySelector<HTMLElement>('[aria-label="远程桌面"], .nt-terminal-main');await display?.requestFullscreen()}}catch{setDisplayFullscreen(true)}};
    return <main className={`nt-terminal-workspace ${displayFullscreen?'is-display-fullscreen':''} ${sidebar?'':'is-sidebar-collapsed'}`}>
        <header className="nt-workspace-header">
            <a className="nt-workspace-brand" href="/x-asset" target="_blank" rel="noopener noreferrer"><TerminalBrand/><span>工作区</span></a>
            <div className="nt-header-tools">
                <button className="nt-icon-button" aria-label={sidebar?'收起我的资产':'展开我的资产'} title={sidebar?'收起我的资产':'展开我的资产'} onClick={()=>setSidebar(!sidebar)}>{sidebar?<PanelLeftClose size={17}/>:<PanelLeftOpen size={17}/>}</button>
                <button className={`nt-icon-button ${settings?'is-selected':''}`} aria-label="终端设置" title="终端设置" onClick={()=>setSettings(!settings)}><Settings2 size={17}/></button>
                <button className="nt-icon-button" aria-label="切换全屏" title="终端显示区全屏（Esc 退出）" disabled={!activeId||!known[activeId]} onClick={()=>void fullscreen()}><Maximize size={17}/></button>
                {settings&&<div className="nt-font-settings"><span>字号</span><button aria-label="减小字号" disabled={fontSize<=11} onClick={()=>setFontSize(v=>v-1)}>−</button><span>{fontSize}</span><button aria-label="增大字号" disabled={fontSize>=24} onClick={()=>setFontSize(v=>v+1)}>+</button></div>}
            </div>
            <a className="nt-header-history" href="/online-session" target="_blank" rel="noopener noreferrer">在线会话 ↗</a>
        </header>
        {sidebar&&<aside className="nt-asset-sidebar" aria-label="我的资产">
            <div className="nt-sidebar-heading"><strong>我的资产</strong><button className="nt-icon-button" aria-label="收起资产面板" title="收起资产面板" onClick={()=>setSidebar(false)}><PanelLeftClose size={14}/></button><button className="nt-icon-button" aria-label="刷新我的资产" title="刷新我的资产" onClick={()=>{void assets.refetch();void facets.refetch()}}><RefreshCw size={14} className={assets.isFetching?'nt-spinning':''}/></button></div>
            <div className="nt-asset-search"><Search size={15}/><input aria-label="搜索我的资产" placeholder="搜索名称、别名或主机" value={search} onChange={e=>setSearch(e.target.value)}/>{search&&<button className="nt-icon-button" aria-label="清除资产搜索" onClick={()=>setSearch('')}><X size={13}/></button>}</div>
            <nav className="nt-asset-tree" aria-label="授权资产列表">
                {assets.isError&&<div role="alert" className="nt-sidebar-message nt-error-text">{assets.error.message}</div>}
                {assets.isPending&&<p className="nt-sidebar-message">正在加载资产…</p>}
                {!assets.isPending&&!assets.isError&&!rows.length&&<p className="nt-sidebar-message">{filter?'没有匹配的资产':'暂无已授权资产'}</p>}
                {groups.map(group=><section key={group.id} className="nt-asset-group">
                    <button className="nt-group-toggle" aria-expanded={!collapsed[group.id]} onClick={()=>setCollapsed(v=>({...v,[group.id]:!v[group.id]}))}><ChevronDown size={12} className={collapsed[group.id]?'is-closed':''}/><Folder size={14}/><span>{group.name}</span><small>{group.assets.length}</small></button>
                    {!collapsed[group.id]&&group.assets.map(asset=><button key={asset.id} className={`nt-asset-item ${activeId===asset.id?'is-active':''}`} onClick={()=>{if(tabs.includes(asset.id))setActiveId(asset.id)}} onDoubleClick={()=>start(asset.id)} onKeyDown={e=>{if(e.key==='Enter'){e.preventDefault();start(asset.id)}}} title={`${asset.name} · ${asset.host}:${asset.port}`}><SquareTerminal size={14}/><span>{asset.name}</span>{statuses[asset.id]==='已连接'&&<i className="nt-connected-dot"/>}</button>)}
                </section>)}
                {assets.hasNextPage&&<button className="nt-load-assets" disabled={assets.isFetchingNextPage} onClick={()=>void assets.fetchNextPage()}>{assets.isFetchingNextPage?'加载中…':'加载更多资产'}</button>}
            </nav>
            <footer className="nt-sidebar-footer">{assets.data?.pages[0]?.total??0} 台授权资产<span>SSH / Telnet / RDP / VNC</span></footer>
        </aside>}
        <section className="nt-session-workspace" aria-label="终端操作区">
            <div className="nt-session-tabs" role="tablist" aria-label="终端会话">
                {tabs.map(id=><div key={id} className={`nt-session-tab ${activeId===id?'is-active':''}`}><button role="tab" aria-selected={activeId===id} aria-controls={`terminal-panel-${id}`} id={`terminal-tab-${id}`} onClick={()=>setActiveId(id)} onDoubleClick={()=>start(id)} title={statuses[id]??'等待连接'}><SquareTerminal size={13}/><span>{known[id]?.name??'SSH 终端'}</span><i className={`nt-tab-dot ${statuses[id]==='已连接'?'is-connected':''}`}/></button><button className="nt-close-tab" aria-label={`关闭 ${known[id]?.name??'SSH 终端'}`} title="关闭并断开此会话" onClick={()=>close(id)}><X size={14}/></button></div>)}
                {!tabs.length&&<span className="nt-tabs-empty">终端工作区</span>}
            </div>
            {notice&&<div role="status" className="nt-workspace-notice">{notice}</div>}
            {initial.isError&&tabs.includes(initialId)&&<div role="alert" className="nt-session-error">{initial.error.message}</div>}
            <div ref={displayPanels} className="nt-session-panels">{tabs.map(id=>!known[id]?null:['rdp','vnc'].includes(known[id]?.protocol)?<section key={id} id={`terminal-panel-${id}`} role="tabpanel" aria-labelledby={`terminal-tab-${id}`} className="nt-session-panel" style={{display:activeId===id?'flex':'none'}}><BackendGraphicsSession assetId={id} connectRequest={connections[id]??0} asset={known[id]} onStatus={statusChanged}/></section>:<BackendTerminalSession key={id} assetId={id} connectRequest={connections[id]??0} asset={known[id]} active={activeId===id} fontSize={fontSize} onStatus={statusChanged}/>)}
                {!tabs.length&&<div className="nt-workspace-empty"><SquareTerminal size={40}/><h2>选择资产，开始连接</h2><p>双击左侧“我的资产”开始连接，双击标签可重新连接</p>{!sidebar&&<button onClick={()=>setSidebar(true)}>展开我的资产</button>}</div>}
            </div>
        </section>
    </main>;
}

function TerminalBrand(){const branding=useBackendBranding();return <>{branding.data?.logo?<img src={branding.data.logo} alt="系统 Logo" style={{width:22,height:22,objectFit:'contain'}}/>:<SquareTerminal size={20}/>}<strong>{branding.data?.name??'Puppeteer'}</strong></>}
