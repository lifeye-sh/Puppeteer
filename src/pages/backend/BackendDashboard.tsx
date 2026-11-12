import {Alert, Button, Empty, Table, Tag} from 'antd';
import {useQuery} from '@tanstack/react-query';
import {Area, AreaChart, CartesianGrid, Legend, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis} from 'recharts';
import {FolderClosed, History, KeyRound, Server, SquareTerminal, Users} from 'lucide-react';
import {statisticsTimezone, type SessionStatistics} from './BackendAccessStatistics';
import {Link} from 'react-router-dom';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';

type SessionRow={session:{id:string;actorId:string;assetId:string;state:string;protocol:string;createdAt:string;connectedAt:string|null;closedAt:string|null};access:{username:string;assetName:string;target:string}};
const states:Record<string,string>={connected:'已连接',closed:'已关闭',failed:'失败',interrupted:'已中断',reserved:'待连接',connecting:'连接中'};
function duration(row:SessionRow){if(!row.session.connectedAt)return '—';const seconds=Math.max(0,Math.floor(((row.session.closedAt?new Date(row.session.closedAt).getTime():Date.now())-new Date(row.session.connectedAt??row.session.createdAt).getTime())/1000));return seconds<60?`${seconds} 秒`:seconds<3600?`${Math.floor(seconds/60)} 分 ${seconds%60} 秒`:`${Math.floor(seconds/3600)} 时 ${Math.floor(seconds%3600/60)} 分`}
export default function BackendDashboard(){
    const {account}=useBackendRuntime();const can=(permission:string)=>!!account.actions?.includes(permission);
    const all=can('session.read');const assetAdmin=can('asset.read');
    const assets=useQuery({queryKey:['r03-dashboard-assets',assetAdmin],queryFn:()=>requests.get<{total:number}>(`${assetAdmin?'/admin/assets/paging':'/portal/assets'}?pageSize=1`,{errorMode:'local'}),refetchInterval:10000});
    const sessions=useQuery({queryKey:['r04-dashboard-sessions',all],queryFn:()=>requests.get<{items:SessionRow[];total:number}>(`/sessions?current=1&all=${all}`,{errorMode:'local'}),refetchInterval:10000});
    const online=useQuery({queryKey:['r04-dashboard-online',all],queryFn:()=>requests.get<{total:number}>(`/sessions?online=true&all=${all}`,{errorMode:'local'}),refetchInterval:5000});
    const groups=useQuery({queryKey:['r03-dashboard-groups',assetAdmin],queryFn:async()=>assetAdmin?await requests.get<unknown[]>('/admin/resource-groups',{errorMode:'local'}):(await requests.get<{groups:unknown[]}>('/portal/facets',{errorMode:'local'})).groups});
    const credentials=useQuery({queryKey:['r03-dashboard-credentials'],enabled:can('credential.read'),queryFn:()=>requests.get<unknown[]>('/admin/credentials',{errorMode:'local'})});
    const users=useQuery({queryKey:['r02-dashboard-users'],enabled:can('user.read'),queryFn:()=>requests.get<{total:number}>('/admin/users/paging?pageSize=1',{errorMode:'local'})});
    const rows=sessions.data?.items??[];
    const aggregate=useQuery({queryKey:['r16-dashboard-statistics',all],queryFn:()=>requests.get<SessionStatistics>(`/statistics/sessions?all=${all}&timezone=${encodeURIComponent(statisticsTimezone)}`,{errorMode:'local'}),refetchInterval:30000});
    const assetTypes=useQuery({queryKey:['r16-dashboard-protocols',assetAdmin],queryFn:()=>requests.get<{protocols:{protocol:string;count:number}[]}>(`/statistics/assets?all=${assetAdmin}`,{errorMode:'local'}),refetchInterval:30000});
    const trend=aggregate.data?.days??[];
    const distribution=(assetTypes.data?.protocols??[]).map((p,i)=>({name:p.protocol.toUpperCase(),value:p.count,fill:['#3769ff','#97c4ff','#32b997','#9270e8','#f4b44e'][i%5]}));
    const assetTotal=distribution.reduce((sum,p)=>sum+p.value,0);
    const stats=[{label:assetAdmin?'资产总数':'我的资产',value:assets.data?.total,icon:Server,url:assetAdmin?'/asset':'/x-asset'},
        {label:all?'在线会话':'我的在线会话',value:online.data?.total,icon:SquareTerminal,url:'/online-session'},
        {label:all?'累计访问':'我的访问记录',value:sessions.data?.total,icon:History,url:'/access-log'},
        {label:'资源分组',value:groups.data?.length,icon:FolderClosed,url:assetAdmin?'/asset':'/x-asset'},
        {label:'授权凭据',value:credentials.data?.length,icon:KeyRound,url:can('credential.read')?'/credential':undefined},
        {label:'系统用户',value:users.data?.total,icon:Users,url:can('user.read')?'/user':undefined}];
    const failed=[assets,sessions,online,groups,credentials,users,aggregate,assetTypes].find(q=>q.isError);
    return <section className="nt-dashboard">
        <h1 className="nt-section-title">数据概览</h1>
        {failed&&<Alert type="error" title="部分数据暂不可用" description={failed.error?.message} action={<Button onClick={()=>void failed.refetch()}>重试</Button>}/>}
        <div className="nt-stat-grid">{stats.map(({label,value,icon:Icon,url})=><div className="nt-stat-card" key={label}><div><span>{label}</span><Icon size={17}/></div>{url?<Link to={url} className="nt-stat-number">{value??'—'}</Link>:<span className="nt-stat-number" title="当前账号没有查看权限">—</span>}</div>)}</div>
        <div className="nt-section-heading"><h2>活跃统计</h2><span>近 14 天 · 完整记录 · {statisticsTimezone}{all?'':' · 仅本人'} · <Link to="/access-log-stats">访问统计</Link></span></div>
        <div className="nt-chart-card">{aggregate.isError?<Empty description="访问统计暂不可用"/>:<ResponsiveContainer width="100%" height={270}><AreaChart data={trend} margin={{top:16,right:12,bottom:0,left:0}}><defs>{['#97c4ff','#689cff','#315bff'].map((color,i)=><linearGradient id={`nt-area-${i}`} key={color} x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor={color} stopOpacity={0.45}/><stop offset="100%" stopColor={color} stopOpacity={0.04}/></linearGradient>)}</defs><CartesianGrid vertical={false} stroke="var(--nt-chart-grid)"/><XAxis dataKey="date" tickFormatter={v=>String(v).slice(5)} axisLine={false} tickLine={false} minTickGap={45} tick={{fontSize:11,fill:'#8b96a8'}}/><YAxis allowDecimals={false} axisLine={false} tickLine={false} width={24} tick={{fontSize:11,fill:'#8b96a8'}}/><Tooltip contentStyle={{borderRadius:8,border:'1px solid #e5e7eb',color:'#182336',background:'#fff'}}/><Legend iconType="square" iconSize={8} wrapperStyle={{fontSize:12,paddingTop:16}}/>{[{key:'sessions',name:'访问次数',color:'#97c4ff'},{key:'users',name:'访问用户',color:'#689cff'},{key:'assets',name:'访问资产',color:'#315bff'}].map((v,i)=><Area key={v.key} type="monotone" dataKey={v.key} name={v.name} stroke={v.color} fill={`url(#nt-area-${i})`} strokeWidth={1.5} isAnimationActive={false}/>)}</AreaChart></ResponsiveContainer>}</div>
        <div className="nt-dashboard-bottom"><section><div className="nt-section-heading"><h2>最新会话</h2><Link to="/access-log">查看全部</Link></div><div className="nt-dashboard-table"><Table<SessionRow> rowKey={r=>r.session.id} dataSource={rows.slice(0,5)} loading={sessions.isPending} pagination={false} scroll={{x:680}} columns={[
            {title:'用户',render:(_,r)=>r.access.username},{title:'协议',render:(_,r)=>r.session.protocol.toUpperCase(),width:75},
            {title:'资产',render:(_,r)=><Link to={`/access-log?sessionId=${r.session.id}`} title={r.access.target}>{r.access.assetName}</Link>},
            {title:'接入时间',render:(_,r)=>new Date(r.session.createdAt).toLocaleString('zh-CN',{hour12:false})},
            {title:'持续时长',render:(_,r)=>duration(r)},
            {title:'状态',render:(_,r)=><Tag variant="filled" color={r.session.state==='connected'?'green':r.session.state==='failed'?'red':'default'}>{states[r.session.state]??r.session.state}</Tag>},
        ]}/></div></section><section><div className="nt-section-heading"><h2>资产类型</h2><span>{assetAdmin?'资产目录':'我的授权资产'}</span></div><div className="nt-chart-card nt-donut-card">{!assetTypes.isError&&assetTotal?<><ResponsiveContainer width="100%" height={240}><PieChart><Pie data={distribution} dataKey="value" nameKey="name" innerRadius={60} outerRadius={85} stroke="none" isAnimationActive={false}/><Tooltip/></PieChart></ResponsiveContainer><div className="nt-donut-center"><strong>{assetTotal}</strong><span>资产</span></div><div className="nt-chart-legend">{distribution.map(p=><span key={p.name}><i style={{background:p.fill}}/> {p.name} {p.value} </span>)}</div></>:<Empty description={assetTypes.isError?'数据暂不可用':assetTypes.isPending?'加载中…':'暂无资产'}/>}</div></section></div>
    </section>;
}
