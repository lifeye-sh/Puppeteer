import {useEffect, useState, type ReactNode} from 'react';
import {Link, useLocation} from 'react-router-dom';
import {Alert, Breadcrumb, Button, ConfigProvider, Dropdown, Menu, theme} from 'antd';
import zhCN from 'antd/locale/zh_CN';
import {ChevronDown, ChevronLeft, ChevronRight, CircleGauge, FolderClosed, Languages, LogOut, Monitor, Moon, ScrollText, Settings, ShieldCheck, SquareTerminal, Sun, UserRound, Users} from 'lucide-react';
import {useBackendBranding} from '@/hook/useBackendBranding';
import {useQueryClient} from '@tanstack/react-query';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import accountApi from '@/api/account-api';
import {clearCurrentUser} from '@/utils/permission';
import './backend-layout.css';
import {navigationLocation, visibleNavigation} from './backend-navigation';
import BackendAIFloating from './BackendAIFloating';

const titles:Record<string,string>={'/':'数据概览','/info':'我的账号','/x-info':'我的账号','/oidc/server/consent':'应用授权','/graphics-playback':'录屏回放'};

export default function BackendLayout({children}:{children:ReactNode}) {
    const {pathname}=useLocation();
    if(['/login','/setup','/oidc/callback','/wechat-work/callback','/website-login'].includes(pathname))return <>{children}</>;
    if(['/terminal','/access','/graphics','/session-observer'].includes(pathname))return <>{children}<BackendAIFloating/></>;
    return <ManagementShell>{children}</ManagementShell>;
}
function ManagementShell({children}:{children:ReactNode}) {
    const {account,runtime,capabilities}=useBackendRuntime();
    const branding=useBackendBranding();
    const brandName=branding.data?.name??'Puppeteer';
    const {pathname}=useLocation();const client=useQueryClient();
    const [collapsed,setCollapsed]=useState(()=>window.innerWidth<800);
    useEffect(()=>{const media=window.matchMedia('(max-width:800px)');const change=()=>{if(media.matches)setCollapsed(true)};media.addEventListener('change',change);return ()=>media.removeEventListener('change',change)},[]);
    const [dark,setDark]=useState(()=>localStorage.getItem('nt-management-theme')==='dark');
    const [error,setError]=useState('');const [leaving,setLeaving]=useState(false);
    const groups=visibleNavigation(account.actions??[],capabilities.features);
    const current=navigationLocation(pathname,groups);
    const groupIcons:Record<string,ReactNode>={workspace:<Monitor size={16}/>,resources:<FolderClosed size={16}/>,connectivity:<SquareTerminal size={16}/>,access:<ShieldCheck size={16}/>,audit:<ScrollText size={16}/>,identity:<Users size={16}/>,operations:<Settings size={16}/>};
    const items=[{key:'/',label:<Link to="/">数据概览</Link>,icon:<CircleGauge size={16}/>},...groups.map(group=>({key:group.key,label:group.label,icon:groupIcons[group.key],children:group.pages.map(page=>({key:page.path,label:<Link to={page.path}>{page.label}</Link>}))}))];
    const currentGroup=current?.group.key;
    const [openKeys,setOpenKeys]=useState<string[]>(currentGroup?[currentGroup]:[]);
    useEffect(()=>{setOpenKeys(currentGroup?[currentGroup]:[])},[currentGroup,pathname]);
    const breadcrumbs=[{title:<Link to="/">首页</Link>},...(current?[{title:current.group.label}]:[]),{title:current?.page.label??titles[pathname]??'管理控制台'}];
    const logout=async()=>{setLeaving(true);setError('');try{await accountApi.logout();clearCurrentUser();client.clear();location.replace('/login')}catch(e){setError((e as Error).message);setLeaving(false)}};
    return <ConfigProvider locale={zhCN} theme={{algorithm:dark?theme.darkAlgorithm:theme.defaultAlgorithm,token:{colorPrimary:'#377dff',borderRadius:7,fontSize:13,fontFamily:'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "Microsoft YaHei", sans-serif'},components:{Menu:{itemHeight:42,itemSelectedBg:dark?'#1d3554':'#e8f3ff',itemSelectedColor:'#377dff',itemMarginInline:4},Table:{headerBg:dark?'#232b38':'#f8f9fb',headerColor:dark?'#d6deeb':'#253044',cellPaddingBlock:14,cellPaddingInline:12}}}}>
        <div className={`nt-management ${collapsed?'is-collapsed':''} ${dark?'is-dark':''}`}>
            <aside className="nt-management-sidebar" aria-label="主导航">
                <Link className="nt-management-brand" to="/" title={brandName}><span className="nt-brand-mark">{branding.data?.logo?<img src={branding.data.logo} alt="系统 Logo" style={{width:28,height:28,objectFit:"contain"}}/>:<SquareTerminal size={23}/>}</span>{!collapsed&&<strong style={{overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{brandName}</strong>}</Link>
                <Menu mode="inline" inlineCollapsed={collapsed} selectedKeys={[current?.page.path??pathname]} openKeys={openKeys} onOpenChange={keys=>setOpenKeys(keys.length?[keys[keys.length-1]]:[])} items={items}/>
                <button className="nt-collapse-navigation" aria-label={collapsed?'展开导航':'收起导航'} onClick={()=>setCollapsed(v=>!v)}>{collapsed?<ChevronRight size={17}/>:<ChevronLeft size={17}/>}</button>
            </aside>
            <div className="nt-management-body">
                <header className="nt-management-header">
                    <Breadcrumb items={breadcrumbs}/>
                    <div className="nt-management-header-actions"><span className="nt-language"><Languages size={15}/>简体中文</span><Link to="/x-asset" title="我的资产" aria-label="打开我的资产"><Monitor size={17}/></Link>
                        <Button type="text" aria-label={dark?'切换浅色模式':'切换深色模式'} icon={dark?<Sun size={17}/>:<Moon size={17}/>} onClick={()=>setDark(v=>{localStorage.setItem('nt-management-theme',v?'light':'dark');return !v})}/>
                        <Dropdown trigger={['click']} menu={{items:[{key:'account',icon:<UserRound size={14}/>,label:<Link to="/info">我的账号与 MFA</Link>},{key:'logout',icon:<LogOut size={14}/>,label:'退出登录',disabled:leaving,onClick:()=>void logout()}]}}><button className="nt-account-menu"><span className="nt-account-avatar">{(account.nickname||account.username).slice(0,1).toUpperCase()}</span><span>{account.nickname||account.username}</span><ChevronDown size={13}/></button></Dropdown>
                    </div>
                </header>
                <div className="nt-management-content">{error&&<Alert type="error" title={error} closable onClose={()=>setError('')}/>} {children}</div>
                <footer className="nt-management-footer">{brandName} <span>·</span> {branding.data?.copyright??'运维管理平台'} {branding.data?.icp&&<><span>·</span> {branding.data.icp}</>} <span>·</span> {runtime.release}</footer>
            </div>
            <BackendAIFloating/>
        </div>
    </ConfigProvider>;
}
