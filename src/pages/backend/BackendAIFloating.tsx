import {lazy, Suspense, useEffect, useRef, useState} from 'react';
import {Maximize2, Minimize2, X} from 'lucide-react';
import {Spin} from 'antd';
import {useLocation} from 'react-router-dom';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import './backend-ai-floating.css';

const Chat = lazy(() => import('./BackendAIPage'));

/** Small, scalable interface illustration; no external image request. */
function ComputerMascot(){
 return <svg viewBox="0 0 112 112" fill="none" aria-hidden="true" focusable="false">
  <ellipse cx="56" cy="102" rx="33" ry="5" fill="#193984" opacity=".13"/>
  <g className="nt-ai-mascot-body">
   <path d="M19 57 11 63 13 74M93 57 101 51 104 40" stroke="#6377C9" strokeWidth="6" strokeLinecap="round" strokeLinejoin="round"/>
   <path d="m100 40-1-6m6 5 3-4" stroke="#6377C9" strokeWidth="3" strokeLinecap="round"/>
   <path d="M48 80h17l3 16H44l4-16Z" fill="#C5CDF1" stroke="#6377C9" strokeWidth="2.5"/>
   <rect x="34" y="92" width="45" height="8" rx="4" fill="#E8ECFF" stroke="#6377C9" strokeWidth="2.5"/>
   <path d="M56 23V14" stroke="#6377C9" strokeWidth="3" strokeLinecap="round"/>
   <circle cx="56" cy="11" r="5" fill="#FFC96B" stroke="#DAA14A" strokeWidth="2"/>
   <rect x="20" y="23" width="73" height="60" rx="16" fill="#F1F3FF" stroke="#6377C9" strokeWidth="3"/>
   <path d="M30 28h49" stroke="white" strokeWidth="3" strokeLinecap="round"/>
   <rect x="27" y="31" width="59" height="40" rx="10" fill="#263B72"/>
   <path d="M32 41a5 5 0 0 1 5-5h12" stroke="#5A6C9F" strokeWidth="2" strokeLinecap="round"/>
   <g className="nt-ai-mascot-eyes" fill="#B3F5EF"><rect x="40" y="44" width="6" height="9" rx="3"/><rect x="66" y="44" width="6" height="9" rx="3"/></g>
   <path d="M49 57q7 8 14 0" stroke="#B3F5EF" strokeWidth="3" strokeLinecap="round"/>
   <ellipse cx="35" cy="56" rx="4" ry="2.5" fill="#F4A9C2" opacity=".85"/><ellipse cx="77" cy="56" rx="4" ry="2.5" fill="#F4A9C2" opacity=".85"/>
   <path d="M31 77h11" stroke="#A8B5E2" strokeWidth="2.5" strokeLinecap="round"/>
   <circle cx="80" cy="77" r="2" fill="#55CBA9"/>
  </g>
  <path className="nt-ai-mascot-spark" d="m91 16 2-5 2 5 5 2-5 2-2 5-2-5-5-2 5-2Z" fill="#FFCC77"/>
 </svg>;
}

export default function BackendAIFloating(){
 const {account,capabilities}=useBackendRuntime();
 const {pathname}=useLocation();
 if(capabilities.features['ai.chat']?.state!=='available')return null;
 return <FloatingChat key={account.id} hidden={pathname==='/ai'}/>;
}

function FloatingChat({hidden}:{hidden:boolean}){
 const [open,setOpen]=useState(false),[mounted,setMounted]=useState(false),[expanded,setExpanded]=useState(false);
 const launcher=useRef<HTMLButtonElement>(null),panel=useRef<HTMLElement>(null);
 const close=()=>{setOpen(false);launcher.current?.focus({preventScroll:true})};
 useEffect(()=>{if(open)panel.current?.focus({preventScroll:true})},[open]);
 return <div className="nt-ai-floating" hidden={hidden}>
  <section ref={panel} id="nt-ai-floating-dialog" role="dialog" aria-modal="false" aria-labelledby="nt-ai-floating-title" tabIndex={-1} hidden={!open} className={`nt-ai-floating-panel ${expanded?'is-expanded':''}`} onKeyDown={event=>{if(event.key==='Escape'&&!event.defaultPrevented&&panel.current?.contains(event.target as Node)){event.stopPropagation();close()}}}>
   <header className="nt-ai-floating-header">
    <span className="nt-ai-floating-avatar"><ComputerMascot/></span>
    <div className="nt-ai-floating-title"><strong id="nt-ai-floating-title">小终端 <span>AI 助手</span></strong><small>随时聊聊你的运维问题</small></div>
    <button type="button" aria-label={expanded?'缩小对话窗口':'放大对话窗口'} title={expanded?'缩小':'放大'} onClick={()=>setExpanded(v=>!v)}>{expanded?<Minimize2 size={17}/>:<Maximize2 size={17}/>}</button>
    <button type="button" aria-label="收起 AI 对话" title="收起，保留当前对话" onClick={close}><X size={19}/></button>
   </header>
   <div className="nt-ai-floating-content">{mounted&&<Suspense fallback={<div className="nt-ai-floating-loading"><Spin/> 正在打开对话…</div>}><Chat embedded/></Suspense>}</div>
  </section>
  <button ref={launcher} type="button" className={`nt-ai-mascot ${open?'is-open':''}`} aria-label={open?'收起 AI 助手':'打开 AI 助手'} aria-expanded={open} aria-controls="nt-ai-floating-dialog" aria-haspopup="dialog" onClick={()=>{if(open)close();else{setMounted(true);setOpen(true)}}}>
   <span className="nt-ai-mascot-hint">嗨，需要帮忙吗？</span>
   <ComputerMascot/>
   <span className="nt-ai-mascot-label">{open?'收起对话':'AI 助手'}</span>
  </button>
 </div>;
}
