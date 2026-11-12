import {Alert,Table} from 'antd';
import {useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import requests from '@/api/core/requests';
type Stream={id:string;host:string;port:number;address:string;state:string;bytesToTarget:number;bytesFromTarget:number;startedAt:string;endedAt:string|null};
export default function BackendForwardAudit({sessionId}:{sessionId:string}){
 const [page,setPage]=useState(1);const query=useQuery({queryKey:['r14-forward-audit',sessionId,page],queryFn:()=>requests.get<{items:Stream[];total:number}>(`/sessions/${sessionId}/forward-streams?current=${page}`,{errorMode:'local'}),refetchInterval:3000});
 const states:Record<string,string>={connecting:'连接中',connected:'传输中',closed:'已关闭',failed:'失败',interrupted:'已中断'};
 return <><Alert type="info" title="端口转发审计" description="只记录目标、持续时长和流量。流量在通道关闭后汇总；不保存原始 TCP 内容或生成终端录屏。"/>{query.error&&<Alert type="error" title={query.error.message}/>}<Table<Stream> size="small" rowKey="id" dataSource={query.data?.items??[]} scroll={{x:780}} pagination={{current:page,pageSize:20,total:query.data?.total??0,onChange:setPage,showSizeChanger:false}} columns={[
 {title:'目标',render:(_,r)=><>{r.host}:{r.port}<div>{r.address}</div></>},{title:'状态',dataIndex:'state',render:v=>states[v]??v},
 {title:'发往目标',render:(_,r)=>r.endedAt?`${r.bytesToTarget} B`:'结束后统计'},{title:'来自目标',render:(_,r)=>r.endedAt?`${r.bytesFromTarget} B`:'结束后统计'},
 {title:'持续时间',render:(_,r)=>`${Math.max(0,Math.floor(((r.endedAt?Date.parse(r.endedAt):Date.now())-Date.parse(r.startedAt))/1000))} 秒`},
 {title:'开始时间',dataIndex:'startedAt',render:v=>new Date(v).toLocaleString()},
 ]}/></>;
}
