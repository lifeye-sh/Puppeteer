import {useEffect,useState} from 'react';
import {Alert,Button,Input,Space} from 'antd';
import {Clipboard,FolderOpen,Keyboard,PanelRightClose,Wrench,X} from 'lucide-react';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import BackendUserFiles from './BackendUserFiles';
const combos:[string,number[]][]=[['Win + E · 文件资源管理器',[0xffeb,101]],['Win + R · 运行',[0xffeb,114]],['Win + D · 显示桌面',[0xffeb,100]],['Win + L · 锁屏',[0xffeb,108]],['Ctrl + Shift + Esc · 任务管理器',[0xffe3,0xffe1,0xff1b]],['Alt + Tab · 切换窗口',[0xffe9,0xff09]],['Ctrl + Alt + Delete',[0xffe3,0xffe9,0xffff]],['Print Screen',[0xff61]]];
export default function BackendRDPToolbox({sessionId,connected,readonly,remoteClipboard,clipboardError,onPaste,onKeys}:{sessionId:string;connected:boolean;readonly:boolean;remoteClipboard:{text:string;sequence:number};clipboardError:{text:string;sequence:number};onPaste:(text:string)=>boolean;onKeys:(keys:number[])=>void}){
 const {capabilities}=useBackendRuntime();
 const [policy,setPolicy]=useState<{upload:boolean;download:boolean;clipboardCopy:boolean;clipboardPaste:boolean}>();
 const [panel,setPanel]=useState(''),[collapsed,setCollapsed]=useState(false),[text,setText]=useState(''),[message,setMessage]=useState('');
 useEffect(()=>{void requests.get<{policy:{upload:boolean;download:boolean;clipboardCopy:boolean;clipboardPaste:boolean}}>('/storage/spaces',{errorMode:'local'}).then(v=>setPolicy(v.policy)).catch(()=>setPolicy(undefined))},[connected,panel]);
 useEffect(()=>{setText(remoteClipboard.text);setMessage(remoteClipboard.sequence>0&&remoteClipboard.text?'已收到远端复制的文本':'')},[remoteClipboard]);
 useEffect(()=>{if(clipboardError.text)setMessage(clipboardError.text)},[clipboardError]);
 const read=async()=>{try{setText(await navigator.clipboard.readText());setMessage('已读取本机剪贴板，点击“发送到远端”生效')}catch{setMessage('浏览器未授予读取权限，请直接在文本框粘贴')}};
 const copy=async()=>{try{await navigator.clipboard.writeText(text);setMessage('已复制到本机')}catch{setMessage('浏览器未授予写入权限，请选中文本手动复制')}};
 return <div className="nt-rdp-toolbox" style={{position:'absolute',right:12,top:12,zIndex:5,display:'flex',gap:8,alignItems:'flex-start',color:'#253044'}} onMouseDown={e=>e.stopPropagation()}>
  {panel&&<div role="dialog" aria-label="RDP 工具箱" style={{width:470,maxWidth:'calc(100vw - 85px)',maxHeight:'calc(100vh - 130px)',overflow:'auto',background:'#fff',padding:16,borderRadius:10,boxShadow:'0 4px 24px #0005'}}>
   <div style={{display:'flex',justifyContent:'space-between',marginBottom:12}}><strong>{panel==='files'?'文件系统':panel==='clipboard'?'剪贴板':'Windows 组合键'}</strong><Button size="small" aria-label="关闭工具面板" onClick={()=>setPanel('')}><X size={14}/></Button></div>
   {panel==='files'&&connected&&<BackendUserFiles key={sessionId} sessionId={sessionId}/>}
   {panel==='clipboard'&&<Space orientation="vertical" style={{width:'100%'}}><Alert type="info" title="双向纯文本剪贴板，最多 16 KiB" description="发送后请点击远端输入框并按 Ctrl+V。剪贴板传输内容不单独录制，粘贴后显示在桌面的文字仍会进入录屏。"/><Input.TextArea aria-label="剪贴板文本" rows={9} value={text} onChange={e=>setText(e.target.value)} maxLength={16384}/><Space wrap><Button onClick={()=>void read()}>读取本机</Button><Button onClick={()=>void copy()}>复制到本机</Button><Button type="primary" disabled={!connected||readonly||!policy?.clipboardPaste} onClick={()=>{if(new TextEncoder().encode(text).length>16384){setMessage('文本超过 16 KiB');return}setMessage(onPaste(text)?'已提交剪贴板文本，请点击远端输入框并按 Ctrl+V；此提示不代表远端已完成粘贴':'发送失败：连接已断开、只读或文本过长')}}>发送到远端</Button></Space>{message&&<span role="status">{message}</span>}</Space>}
   {panel==='keys'&&<Space orientation="vertical">{combos.map(([label,keys])=><Button key={label} disabled={!connected||readonly} onClick={()=>onKeys(keys)}>{label}</Button>)}{readonly&&<span>只读会话禁止发送组合键</span>}</Space>}
  </div>}
  <Space orientation="vertical" style={{padding:6,background:'#ffffffed',borderRadius:20,boxShadow:'0 2px 12px #0004'}}>
   <Button shape="circle" aria-label={collapsed?'展开 RDP 工具箱':'收起 RDP 工具箱'} title="工具箱" onClick={()=>{setCollapsed(!collapsed);setPanel('')}}>{collapsed?<Wrench size={17}/>:<PanelRightClose size={17}/>}</Button>
   {!collapsed&&<><Button shape="circle" disabled={!capabilities.protocols?.rdp?.files} aria-label="RDP 文件系统" title="文件系统" onClick={()=>setPanel(panel==='files'?'':'files')}><FolderOpen size={17}/></Button><Button shape="circle" disabled={!capabilities.protocols?.rdp?.clipboard} aria-label="RDP 剪贴板" title="剪贴板" onClick={()=>setPanel(panel==='clipboard'?'':'clipboard')}><Clipboard size={17}/></Button><Button shape="circle" aria-label="RDP 组合键" title="组合键" onClick={()=>setPanel(panel==='keys'?'':'keys')}><Keyboard size={17}/></Button></>}
  </Space>
 </div>;
}
