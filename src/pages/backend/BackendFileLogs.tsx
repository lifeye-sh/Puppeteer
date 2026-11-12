import {useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {Alert,Button,Checkbox,Table,Tag,Space,Typography} from 'antd';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
type Event={id:string;sessionId:string;actor:string;action:string;path:string;destination:string;state:string;bytes:number;message:string;createdAt:string};
const actions:Record<string,string>={upload:'上传 / 保存',download:'下载',read_text:'读取文本',mkdir:'新建目录',rename:'重命名',delete:'删除'};
const states:Record<string,string>={succeeded:'成功',running:'处理中',interrupted:'中断',failed:'失败',canceled:'取消'};
export default function BackendFileLogs(){const {account}=useBackendRuntime();const canAll=account.actions?.includes('audit.read');const [all,setAll]=useState(!!canAll),[page,setPage]=useState(1);
 const query=useQuery({queryKey:['r05-file-logs',all,page],queryFn:()=>requests.get<{items:Event[];total:number}>(`/filesystem-logs?all=${all&&!!canAll}&current=${page}`,{errorMode:'local'}),refetchInterval:3000});
 return <section className="nt-page"><Typography.Title level={2}>文件审计</Typography.Title><Space>{canAll&&<Checkbox checked={all} onChange={e=>{setAll(e.target.checked);setPage(1)}}>查看全部用户</Checkbox>}<Button onClick={()=>void query.refetch()}>刷新</Button></Space>{query.isError&&<Alert type="error" title={query.error.message}/>}<Table<Event> rowKey="id" loading={query.isPending} dataSource={query.data?.items??[]} scroll={{x:1000}} pagination={{current:page,pageSize:20,total:query.data?.total??0,onChange:setPage,showSizeChanger:false}} columns={[{title:'用户',dataIndex:'actor'},{title:'操作',dataIndex:'action',render:v=>actions[v]??v},{title:'路径',render:(_,v)=><>{v.path}{v.destination&&` → ${v.destination}`}</>},{title:'状态',render:(_,v)=><><Tag color={v.state==='succeeded'?'green':v.state==='running'?'blue':'red'}>{states[v.state]??v.state}</Tag>{v.message}</>},{title:'字节',dataIndex:'bytes'},{title:'时间',dataIndex:'createdAt',render:v=>new Date(v).toLocaleString()},{title:'会话',render:(_,v)=><a href={`/access-log?sessionId=${v.sessionId}`}>访问记录</a>}]}/></section>
}
