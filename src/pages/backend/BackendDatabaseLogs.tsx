import {Alert,Button,Card,Descriptions,Input,Select,Space,Table,Tag,Typography} from 'antd';
import {useQuery} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';
type Log={id:string;sessionId:string;workOrderId:string;parameters:{type:number;bytes:number;null:boolean;streamed:boolean;format?:number}[];sanitized:boolean;user:string;asset:string;sql:string;hash:string;command:number;state:string;rows:number;durationMs:number;createdAt:string};
const states:Record<string,string>={running:'执行中',completed:'已完成',failed:'失败 / 拒绝',unknown:'结果不确定',rolled_back:'已回滚'};
export default function BackendDatabaseLogs(){
 const [search,setSearch]=useState(''),[state,setState]=useState(''),[page,setPage]=useState(1);
 const query=useQuery({queryKey:['r12-sql-logs',search,state,page],queryFn:()=>requests.get<{items:Log[];total:number}>(`/admin/database-sql-logs?search=${encodeURIComponent(search)}&state=${state}&current=${page}&pageSize=20`,{errorMode:'local'}),refetchInterval:5000});
 return <section className="nt-page"><Typography.Title level={2}>数据库 SQL 审计</Typography.Title>{query.error&&<Alert type="error" title={query.error.message}/>}<Card extra={<Space wrap><Input.Search placeholder="搜索 SQL、用户或数据库" allowClear onSearch={v=>{setSearch(v);setPage(1)}}/><Select value={state} style={{width:130}} onChange={v=>{setState(v);setPage(1)}} options={[{value:'',label:'全部状态'},...Object.entries(states).map(([value,label])=>({value,label}))]}/><Button onClick={()=>void query.refetch()}>刷新</Button></Space>}>
 <Table<Log> rowKey="id" dataSource={query.data?.items??[]} loading={query.isLoading} scroll={{x:1100}} pagination={{current:page,pageSize:20,total:query.data?.total,onChange:setPage}} expandable={{expandedRowRender:v=><><Descriptions size="small" items={[{key:'session',label:v.workOrderId?'工单 ID':'会话 ID',children:v.workOrderId||v.sessionId},{key:'digest',label:'原始 SQL SHA-256',children:v.hash}]}/>{v.sanitized&&<Alert type="info" title="SQL 展示内容已截断或清理非法字符，摘要对应原始字节。"/>}<pre style={{whiteSpace:'pre-wrap',overflowWrap:'anywhere'}}>{v.sql}</pre>{v.parameters?.length>0&&<><Typography.Text type="secondary">预编译参数仅记录类型、长度和 NULL 标记，不保存参数原文。</Typography.Text><Table size="small" pagination={false} rowKey="index" dataSource={v.parameters.map((p,index)=>({...p,index:index+1}))} columns={[{title:'参数',dataIndex:'index'},{title:v.command>=65?'PostgreSQL 类型 OID（0 为未推断）':'MySQL 类型编码',dataIndex:'type'},{title:'字节数',dataIndex:'bytes'},{title:'NULL',dataIndex:'null',render:b=>b?'是':'否'},...(v.command>=65?[{title:'编码',dataIndex:'format',render:(n:number)=>n===1?'二进制':'文本'}]:[{title:'流式参数',dataIndex:'streamed',render:(b:boolean)=>b?'是':'否'}])]}/></>}</>}} columns={[
 {title:'时间',dataIndex:'createdAt',render:v=>new Date(v).toLocaleString()},{title:'用户',dataIndex:'user'},{title:'数据库',dataIndex:'asset'},
 {title:'命令',dataIndex:'command',render:v=>({3:'QUERY',22:'PREPARE',23:'EXECUTE',81:'PG QUERY',80:'PG PARSE',69:'PG EXECUTE'}[v as 3|22|23|81|80|69]??v)},
 {title:'来源',render:(_,v)=>v.workOrderId?'SQL 工单':'原生客户端'},
 {title:'SQL',dataIndex:'sql',ellipsis:true},{title:'状态',dataIndex:'state',render:v=><Tag color={v==='completed'?'green':v==='running'?'blue':'red'}>{states[v]??v}</Tag>},{title:'返回行数',dataIndex:'rows'},{title:'耗时',dataIndex:'durationMs',render:v=>`${v} ms`}
 ]}/></Card></section>
}
