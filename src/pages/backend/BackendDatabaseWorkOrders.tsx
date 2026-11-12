import {Alert,Button,Card,Checkbox,Descriptions,Form,Input,InputNumber,Popconfirm,Select,Space,Table,Tag,Typography} from 'antd';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal,{FormTabs} from './BackendFormModal';
type Work={id:string;assetId:string;requesterId:string;title:string;sql:string;hash:string;mode:string;state:string;version:number;runId:string;reason:string;cancelRequested:boolean;createdAt:string};
type Result={index:number;state:string;affectedRows:number;durationMs:number;data:{columns?:string[];rows?:unknown[][];total?:number;retainedRows?:number;truncated?:boolean}};
const statuses:Record<string,string>={pending:'待审批',approved:'待执行',running:'执行中',succeeded:'已完成',failed:'失败',partial:'部分完成',cancelled:'已取消',rejected:'已拒绝',unknown:'结果不确定'};
export default function BackendDatabaseWorkOrders({portal=false}:{portal?:boolean}){
 const {account}=useBackendRuntime();const cache=useQueryClient();const [page,setPage]=useState(1),[state,setState]=useState(''),[creating,setCreating]=useState(false),[selected,setSelected]=useState<Work>(),[error,setError]=useState(''),[busy,setBusy]=useState(false),[reason,setReason]=useState('');const [form]=Form.useForm();
 const list=useQuery({queryKey:['r12-work',portal,page,state],queryFn:()=>requests.get<{items:Work[];total:number}>(`${portal?'/portal':'/admin'}/database-work-orders?current=${page}&pageSize=20&state=${state}`,{errorMode:'local'}),refetchInterval:3000});
 const assets=useQuery({queryKey:['r12-databases',true],queryFn:()=>requests.get<{items:{id:string;name:string;alias:string;engine:string}[]}>('/portal/database-assets',{errorMode:'local'})});
 const run=async(fn:()=>Promise<void>)=>{setError('');setBusy(true);try{await fn();await cache.invalidateQueries({queryKey:['r12-work']})}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const review=(approve:boolean)=>run(async()=>{await requests.post(`/database-work-orders/${fresh!.id}/review`,{version:fresh!.version,approve,reason},{errorMode:'local'});setSelected(undefined)});
 const fresh=selected?(list.data?.items.find(v=>v.id===selected.id)??selected):undefined;
 return <section className="nt-page"><Typography.Title level={2}>{portal?'我的 SQL 工单':'SQL 工单审批'}</Typography.Title>
 {(error||list.error)&&<Alert type="error" title={error||list.error?.message}/>}
 <Card extra={<Space><Select aria-label="工单状态" value={state} style={{width:150}} onChange={v=>{setState(v);setPage(1)}} options={[{value:'',label:'全部状态'},...Object.entries(statuses).map(([value,label])=>({value,label}))]}/><Button onClick={()=>void list.refetch()}>刷新</Button><Button type="primary" onClick={()=>{form.resetFields();setCreating(true)}}>提交 SQL 工单</Button></Space>}>
 <Table<Work> rowKey="id" dataSource={list.data?.items??[]} loading={list.isLoading} pagination={{current:page,pageSize:20,total:list.data?.total,onChange:setPage}} scroll={{x:1000}} columns={[
 {title:'工单',dataIndex:'title',render:(value,v)=><Button type="link" onClick={()=>{setSelected(v);setReason('')}}>{value}</Button>},
 {title:'数据库',render:(_,v)=>assets.data?.items.find(a=>a.id===v.assetId)?.name??v.assetId.slice(0,8)},
 {title:'执行方式',render:(_,v)=><Tag color={v.mode==='ddl'?'orange':'blue'}>{v.mode==='ddl'?'DDL 逐条执行':'事务执行'}</Tag>},
 {title:'状态',render:(_,v)=><Tag color={v.state==='succeeded'?'green':['failed','unknown','partial'].includes(v.state)?'red':'blue'}>{statuses[v.state]??v.state}{v.cancelRequested&&v.state==='running'?' · 取消中':''}</Tag>},
 {title:'提交时间',dataIndex:'createdAt',render:value=>new Date(value).toLocaleString()},
 {title:'执行说明',dataIndex:'reason',ellipsis:true},
 {title:'操作',render:(_,v)=><Space><Button type="link" onClick={()=>{setSelected(v);setReason('')}}>{!portal&&v.state==='pending'&&v.requesterId!==account.id?'审批':'详情 / 结果'}</Button>{['pending','approved','running'].includes(v.state)&&(v.requesterId===account.id||!portal)&&<Popconfirm title="取消此工单？" description="执行中的语句可能已生效，最终状态以数据库确认结果为准。" onConfirm={()=>run(async()=>{await requests.post(`/database-work-orders/${v.id}/cancel`,{version:v.version},{errorMode:'local'})})}><Button type="link" danger disabled={busy||v.cancelRequested}>取消</Button></Popconfirm>}</Space>}
 ]}/></Card>
 <Modal open={creating} title="提交 SQL 工单" okText="提交审批" confirmLoading={busy} onCancel={()=>setCreating(false)} onOk={()=>form.submit()} forceRender>
 {error&&<Alert type="error" title={error}/>}<Form form={form} layout="vertical" onFinish={v=>void run(async()=>{await requests.post('/database-work-orders',{...v,acknowledgeDdl:!!v.acknowledgeDdl},{errorMode:'local'});setCreating(false)})}>
 <FormTabs sections={[
 {key:'target',label:'目标与说明',description:'提交后 SQL、目标和凭据版本会冻结，审批通过后自动执行。申请人不能审批自己的工单。当前工单支持 MySQL。',children:<><Form.Item name="title" label="变更说明" rules={[{required:true}]}><Input maxLength={200}/></Form.Item><Form.Item name="assetId" label="数据库" rules={[{required:true}]}><Select showSearch optionFilterProp="label" options={assets.data?.items.filter(a=>a.engine!=='postgresql').map(a=>({value:a.id,label:`${a.name} · ${a.alias}`}))}/></Form.Item></>},
 {key:'sql',label:'SQL 内容',description:'支持最多 100 条语句，总计不超过 1 MiB。DML 与 DDL 请分别提交。事务写入仅支持 InnoDB 基表。',children:<><Form.Item className="nt-form-span" name="sql" label="SQL" rules={[{required:true}]}><Input.TextArea rows={12} spellCheck={false} style={{fontFamily:'monospace'}}/></Form.Item><Form.Item className="nt-form-span" name="acknowledgeDdl" valuePropName="checked"><Checkbox>我确认 DDL 会逐条提交，失败时可能仅部分完成，不能整体回滚。</Checkbox></Form.Item></>}
 ]}/></Form></Modal>
 <Modal open={!!fresh} title={fresh?.title??'SQL 工单'} width={1050} onCancel={()=>setSelected(undefined)} footer={fresh&&!portal&&fresh.state==='pending'&&fresh.requesterId!==account.id?<Space><Button onClick={()=>setSelected(undefined)}>关闭</Button><Button danger loading={busy} onClick={()=>void review(false)}>拒绝</Button><Button type="primary" loading={busy} onClick={()=>void review(true)}>批准并执行</Button></Space>:<Button onClick={()=>setSelected(undefined)}>关闭</Button>}>
 {fresh&&<>{error&&<Alert type="error" title={error}/>}<Descriptions size="small" column={2} items={[{key:'state',label:'状态',children:statuses[fresh.state]},{key:'mode',label:'执行方式',children:fresh.mode==='ddl'?'DDL 逐条提交':'事务'},{key:'id',label:'工单 ID',children:fresh.id},{key:'run',label:'执行 ID',children:fresh.runId||'尚未执行'}]}/>
 {['partial','unknown'].includes(fresh.state)&&<Alert type="warning" title="请核对目标数据库的实际状态；系统不会自动重试。"/>}
 <Typography.Paragraph>{fresh.reason}</Typography.Paragraph><Typography.Text type="secondary">SQL SHA-256：{fresh.hash}</Typography.Text><Input.TextArea readOnly value={fresh.sql} rows={8} style={{fontFamily:'monospace',margin:'12px 0'}}/>
 {fresh.state==='pending'&&!portal&&fresh.requesterId!==account.id&&<Input.TextArea value={reason} onChange={e=>setReason(e.target.value)} maxLength={2000} rows={2} placeholder="审批说明"/>}
 {fresh.state!=='pending'&&<WorkResults key={fresh.id} work={fresh}/>}</>}
 </Modal></section>
}
function WorkResults({work}:{work:Work}){
 const [statement,setStatement]=useState(1),[page,setPage]=useState(1);
 const query=useQuery({queryKey:['r12-work-result',work.id,work.state,statement,page],queryFn:()=>requests.get<{items:Result[]}>(`/database-work-orders/${work.id}/results?statement=${statement-1}&current=${page}&pageSize=20`,{errorMode:'local'}),refetchInterval:work.state==='running'?3000:false});
 const result=query.data?.items[0];return <div style={{marginTop:18}}><Space><strong>执行结果</strong><span>语句序号</span><InputNumber min={1} max={100} value={statement} onChange={v=>{setStatement(v??1);setPage(1)}}/></Space>
 {query.error&&<Alert type="error" title={query.error.message}/>}
 {result?<><p>状态：{{completed:'已完成',pending_commit:'等待事务提交',rolled_back:'已回滚',failed:'失败',unknown:'结果不确定'}[result.state]??result.state} · {result.data.columns?'返回行数':'影响行数'}：{result.data.columns?result.data.total:result.affectedRows} · 耗时：{result.durationMs} ms</p>{result.data.truncated&&<Alert type="info" title={`结果已截断保存，目标库共返回 ${result.data.total} 行，保存 ${result.data.retainedRows} 行。`}/>}
 <Table rowKey="index" size="small" scroll={{x:true}} dataSource={(result.data.rows??[]).map((values,index)=>({index,values}))} columns={(result.data.columns??[]).map((name,index)=>({title:name,key:index,render:(_:unknown,v:{values:unknown[]})=>v.values[index]===null?<Tag>NULL</Tag>:<span style={{whiteSpace:'pre-wrap',overflowWrap:'anywhere'}}>{typeof v.values[index]==='object'?JSON.stringify(v.values[index]):String(v.values[index]??'')}</span>}))} pagination={{current:page,pageSize:20,total:result.data.retainedRows??0,onChange:setPage}}/>
 </>:<Typography.Paragraph type="secondary">此语句尚无执行结果。</Typography.Paragraph>}</div>
}
