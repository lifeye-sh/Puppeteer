import {useQuery} from '@tanstack/react-query';
import {Alert,Button,Card,Descriptions,Space,Statistic,Typography} from 'antd';
import requests from '@/api/core/requests';

type Metrics={sampledAt:string;node:string;pool:{total:number;acquired:number;idle:number;max:number;acquireCount:number;cancelled:number;emptyWaitCount:number;acquireMilliseconds:number};heapBytes:number;runtimeBytes:number;goroutines:number;host:{cpu:number|null;memoryTotal:number|null;memoryUsed:number|null};business:null|{sessions:number;reserved:number;connected:number;storageSpaces:number;storageUsed:number;storageQuota:number;recordingBytes:number;databaseBytes:number};errors:string[]};
const bytes=(n:number|undefined|null)=>n==null?'—':`${(n/1024**2).toFixed(1)} MiB`;
export default function BackendPlatformMetrics(){
 const query=useQuery({queryKey:['platform-metrics'],queryFn:()=>requests.get<Metrics>('/admin/monitoring/overview',{errorMode:'local'}),refetchInterval:10000});
 const d=query.data;
 return <Space orientation="vertical" size={16} style={{width:'100%'}}>
  <Alert type="info" showIcon title="平台与业务指标" description="进程、数据库连接池和系统指标属于当前处理请求的后端节点；系统指标来自其操作系统命名空间，不代表容器配额。业务统计来自平台数据库。每 10 秒刷新，采集失败显示未知。"/>
  {query.error&&<Alert type="error" title={query.error.message}/>}
  {!!d?.errors.length&&<Alert type="warning" title="部分指标未能采集" description={d.errors.map(v=>({host_cpu_unavailable:'CPU 未知',host_memory_unavailable:'系统内存未知',business_metrics_unavailable:'业务统计未知'}[v]??v)).join('；')}/>}
  <Space wrap><Button loading={query.isFetching} onClick={()=>void query.refetch()}>刷新</Button><Typography.Text type="secondary">节点 {d?.node??'—'} · {d?new Date(d.sampledAt).toLocaleString():'等待采集'}</Typography.Text></Space>
  <div style={{display:'grid',gridTemplateColumns:'repeat(auto-fit,minmax(190px,1fr))',gap:16}}>
   {[['有效会话',d?.business?.sessions],['已连接',d?.business?.connected],['待连接预约',d?.business?.reserved],['存储空间',d?.business?.storageSpaces]].map(([title,value])=><Card key={title}><Statistic title={title} value={value??'—'}/></Card>)}
  </div>
  <Card title="后端进程与系统"><Descriptions column={{xs:1,sm:2,lg:3}} items={[
   {key:'cpu',label:'系统 CPU（100ms 采样）',children:d?.host.cpu==null?'—':`${d.host.cpu.toFixed(1)}%`},
   {key:'mem',label:'系统内存',children:`${bytes(d?.host.memoryUsed)} / ${bytes(d?.host.memoryTotal)}`},
   {key:'heap',label:'Go 堆内存',children:bytes(d?.heapBytes)},
   {key:'runtime',label:'Go 运行时管理内存',children:bytes(d?.runtimeBytes)},
   {key:'goroutines',label:'Goroutine',children:d?.goroutines??'—'}]}/></Card>
  <Card title="数据库连接池"><Descriptions column={{xs:1,sm:2,lg:3}} items={[
   {key:'total',label:'当前 / 上限',children:d?`${d.pool.total} / ${d.pool.max}`:'—'},
   {key:'active',label:'占用 / 空闲',children:d?`${d.pool.acquired} / ${d.pool.idle}`:'—'},
   {key:'count',label:'累计获取次数',children:d?.pool.acquireCount??'—'},
   {key:'wait',label:'累计空池等待次数',children:d?.pool.emptyWaitCount??'—'},
   {key:'cancel',label:'累计取消次数',children:d?.pool.cancelled??'—'},
   {key:'duration',label:'累计获取耗时',children:d?`${d.pool.acquireMilliseconds} ms`:'—'}]}/><Typography.Text type="secondary">累计计数从本节点进程启动时开始，重启后重置。</Typography.Text></Card>
  <Card title="存储账本"><Descriptions column={{xs:1,sm:2}} items={[
   {key:'used',label:'共享文件逻辑大小',children:bytes(d?.business?.storageUsed)},
   {key:'quota',label:'各空间配额合计',children:bytes(d?.business?.storageQuota)},
   {key:'recording',label:'原始录屏逻辑大小',children:bytes(d?.business?.recordingBytes)},
   {key:'db',label:'平台数据库物理大小',children:bytes(d?.business?.databaseBytes)}]}/><Typography.Text type="secondary">空间配额合计不代表磁盘剩余容量。原始录屏不包含 MP4 导出和外部备份，数据库物理大小含索引。</Typography.Text></Card>
 </Space>;
}
