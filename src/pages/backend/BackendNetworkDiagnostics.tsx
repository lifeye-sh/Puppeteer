import {useEffect,useRef,useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {Alert,Button,Card,Descriptions,InputNumber,Select,Space,Table,Tag,Typography} from 'antd';
import requests from '@/api/core/requests';

type Sample={sequence:number;ok:boolean;status:string;durationMs:number;message:string};
type Summary={result:string;sent:number;succeeded:number;failed:number};
const opts={errorMode:'local' as const};
export default function BackendNetworkDiagnostics(){
 const [search,setSearch]=useState(''),[assetId,setAssetId]=useState<string>(),[kind,setKind]=useState('tcp'),[count,setCount]=useState(4);
 const [busy,setBusy]=useState(false),[error,setError]=useState(''),[route,setRoute]=useState(''),[samples,setSamples]=useState<Sample[]>([]),[summary,setSummary]=useState<Summary>();
 const controller=useRef<AbortController|null>(null);
 useEffect(()=>()=>controller.current?.abort(),[]);
 const assets=useQuery({queryKey:['diagnostic-assets',search],queryFn:()=>requests.get<{items:{id:string;name:string;protocol:string}[]}>('/admin/assets/paging?pageSize=100&search='+encodeURIComponent(search),opts)});
 const source=useQuery({queryKey:['diagnostic-source'],queryFn:()=>requests.get<{sourceIp:string;peerIp:string;trustedProxyApplied:boolean;description:string}>('/admin/diagnostics/source',opts)});
 const run=async()=>{
  if(!assetId)return;
  const abort=new AbortController();controller.current=abort;setBusy(true);setError('');setSummary(undefined);setSamples([]);setRoute('');
  try{await requests.postStream('/admin/diagnostics/stream',{assetId,kind,count},(event,data)=>{
   if(event==='start')setRoute((data as {route:string}).route);
   if(event==='sample')setSamples(old=>[...old,data as Sample]);
   if(event==='complete')setSummary(data as Summary);
   if(event==='error')setError((data as {message:string}).message);
  },abort.signal,{completionEvent:'complete',disconnectMessage:'诊断连接提前断开，请重新探测。'})}catch(e){if(!abort.signal.aborted)setError((e as Error).message)}finally{if(controller.current===abort){controller.current=null;setBusy(false)}}
 };
 return <Space orientation="vertical" size={16} style={{width:'100%'}}>
  <Alert type="info" showIcon title="受控网络诊断" description="仅探测已保存且启用的资产。TCP 沿用资产的网关、跳板或代理路径；ICMP Ping 仅支持直连。最多 10 次，每次超时 3 秒，停止或离开页面会取消探测。连通不等于凭据和远端服务正常。"/>
  <Card title="探测目标">
   <Space wrap align="start">
    <Select aria-label="诊断资产" showSearch filterOption={false} onSearch={setSearch} loading={assets.isFetching} style={{width:320}} placeholder="搜索并选择资产" value={assetId} onChange={setAssetId} disabled={busy} options={assets.data?.items.map(v=>({value:v.id,label:`${v.name} · ${v.protocol.toUpperCase()}`}))}/>
    <Select aria-label="诊断方式" style={{width:160}} value={kind} disabled={busy} onChange={setKind} options={[{value:'tcp',label:'TCP 连通'},{value:'ping',label:'ICMP Ping'}]}/>
    <Space><InputNumber aria-label="探测次数" min={1} max={10} precision={0} value={count} disabled={busy} onChange={v=>setCount(v??4)}/><span>次</span></Space>
    <Button type="primary" disabled={!assetId||busy} onClick={()=>void run()}>开始探测</Button>
    <Button disabled={!busy} onClick={()=>{controller.current?.abort();setError('已停止探测')}}>停止</Button>
   </Space>
   {(error||assets.error)&&<Alert style={{marginTop:16}} type="warning" title={error||assets.error?.message}/>}
   {route&&<Typography.Paragraph style={{marginTop:16}}>路径：{({direct:'平台直连',agent:'Agent','ssh-jump':'SSH 跳板','gateway-group':'网关组',proxy:'网络代理'} as Record<string,string>)[route]??route}{busy?' · 正在探测':''}</Typography.Paragraph>}
   <Table<Sample> rowKey="sequence" dataSource={samples} pagination={false} size="small" style={{marginTop:16}} columns={[{title:'序号',dataIndex:'sequence',width:80},{title:'结果',render:(_,s)=><Tag color={s.ok?'green':'orange'}>{s.ok?'可达':s.status==='unsupported'?'环境不支持':'未连通'}</Tag>,width:130},{title:'耗时',render:(_,s)=>`${s.durationMs.toFixed(1)} ms`,width:150},{title:'说明',dataIndex:'message'}]}/>
   {summary&&<Alert style={{marginTop:16}} type={summary.failed?'warning':'success'} title={`已完成 ${summary.sent} 次：成功 ${summary.succeeded}，失败 ${summary.failed}`}/>}
  </Card>
  <Card title="当前请求来源" extra={<Button onClick={()=>void source.refetch()}>刷新</Button>}>
   {source.error?<Alert type="error" title={source.error.message}/>:<><Descriptions column={{xs:1,sm:2,lg:3}} items={[{key:'source',label:'平台识别 IP',children:source.data?.sourceIp??'—'},{key:'peer',label:'直接连接 IP',children:source.data?.peerIp??'—'},{key:'proxy',label:'可信代理提取',children:source.data?.trustedProxyApplied?'已应用':'未应用'}]}/><Typography.Text type="secondary">{source.data?.description}</Typography.Text></>}
  </Card>
 </Space>;
}
