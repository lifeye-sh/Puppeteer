import {useState} from 'react';
import {NativeRDPReport} from './BackendNativeRDPCapability';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Card,Descriptions,Select,Space,Table,Tag,Typography} from 'antd';
import {Link} from 'react-router-dom';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import {configurationStates} from './configuration-model';
import {settingsDestination} from './platform-settings-model';
import {displayedDimension,operationStates,operationStages,type CheckTask,type ConfigurationStatus,type Dimension,type FeatureStatus} from './configuration-status-model';

function StateTag({value,stale=false}:{value:Dimension;stale?:boolean}){
 const d=displayedDimension(value,stale);
 return <Tag title={d.reason} color={['ready','applied','running'].includes(d.state)?'green':['failed','blocked','degraded'].includes(d.state)?'red':['checking','applying','pending'].includes(d.state)?'blue':'default'}>{operationStates[d.state]??d.state}</Tag>;
}
export default function BackendConfigurationStatus(){
 const {account}=useBackendRuntime();const cache=useQueryClient();const [category,setCategory]=useState('all'),[busy,setBusy]=useState(''),[error,setError]=useState('');
 const canCheck=account.type==='admin'&&!!account.actions?.includes('identity.manage');
 const status=useQuery({queryKey:['configuration-operations'],queryFn:()=>requests.get<ConfigurationStatus>('/settings/operations',{errorMode:'local'}),refetchInterval:5000,retry:false});
 const history=useQuery({queryKey:['configuration-operation-tasks'],queryFn:()=>requests.get<{items:CheckTask[]}>('/settings/operation-tasks',{errorMode:'local'}),refetchInterval:5000,retry:false});
 const refresh=()=>Promise.all([cache.invalidateQueries({queryKey:['configuration-operations']}),cache.invalidateQueries({queryKey:['configuration-operation-tasks']})]);
 const check=async(feature:string)=>{if(!status.data)return;setBusy(feature);setError('');try{const response=await fetch(`/api/settings/operations/${encodeURIComponent(feature)}/preflight`,{method:'POST',headers:{'Content-Type':'application/json','If-Match':`"settings-${status.data.revision}"`},body:'{}'});const result=await response.json();if(!response.ok)throw Error(result.message??'预检提交失败');await refresh()}catch(e){setError((e as Error).message)}finally{setBusy('')}};
 const cancel=async(id:string)=>{setBusy(id);setError('');try{await requests.post(`/settings/operation-tasks/${encodeURIComponent(id)}/cancel`,{}, {errorMode:'local'});await refresh()}catch(e){setError((e as Error).message)}finally{setBusy('')}};
 const active=history.data?.items.some(t=>t.state==='running');
 const details=(row:FeatureStatus)=><Space orientation="vertical" style={{width:'100%'}}>
  <Typography.Text>配置应用：{row.applied.reason||'查看节点确认结果与功能生效范围'}；依赖：{row.dependencies.reason||operationStates[row.dependencies.state]}；实际服务：{row.service.reason||operationStates[row.service.state]}</Typography.Text>
  {row.deployment&&<Typography.Paragraph>部署任务 {row.deployment.id} · {operationStates[row.deployment.state]??row.deployment.state} · {({preflight:'预检',apply:'部署',verify:'验证',rollback:'回滚',fence:'保持关闭',complete:'结束',interrupted:'中断恢复'} as Record<string,string>)[row.deployment.stage]??row.deployment.stage} · {new Date(row.deployment.updated).toLocaleString()} <Link to="/deployment-settings?section=deployment">查看部署进度与处理</Link></Typography.Paragraph>}
  {!row.checkSupported&&row.category!=='policies'&&<Typography.Text type="secondary">尚未接入统一预检，请进入配置或对应资源页面使用专项检测。</Typography.Text>}
  {row.capability&&<NativeRDPReport value={row.capability} stale={!!status.error}/>}
  {row.lastCheck&&<><Typography.Text type="secondary">检测版本 {row.lastCheck.revision} · 节点 {row.lastCheck.node} · 更新于 {new Date(row.lastCheck.updated).toLocaleString()} · 有效至 {new Date(row.lastCheck.expires).toLocaleString()}</Typography.Text>{row.lastCheck.checks.map((c,i)=><Alert key={`${c.code}-${i}`} type={c.state==='ready'?'info':'warning'} title={c.summary} description={<>{c.remediation} <Typography.Text type="secondary">{c.code} · {c.scope}</Typography.Text></>}/>)}</>}
 </Space>;
 return <div className="nt-settings-stack">
  <Alert type="info" showIcon title="预检只检查依赖，不修改配置或部署服务" description="运行状态限定当前响应节点的监听。预检不代表目标数据库、用户权限或公网可用；不检查防火墙和云安全组。发送邮件、调用模型等业务验证仍由对应页面明确操作。"/>
  {status.error&&<Alert type="error" showIcon title="无法刷新统一状态" description={status.error.message}/>}
  {error&&<Alert type="error" closable onClose={()=>setError('')} title={error}/>}
  <Card title="配置与服务状态" extra={<Button onClick={()=>void refresh()} loading={status.isFetching}>刷新</Button>}>
   {status.data&&<Typography.Paragraph type="secondary">配置版本 {status.data.revision} · 当前节点 {status.data.node} · 观测时间 {new Date(status.data.checkedAt).toLocaleString()}</Typography.Paragraph>}
   <Select aria-label="状态类别" value={category} onChange={setCategory} style={{width:180,marginBottom:16}} options={[{value:'all',label:'全部类别'},{value:'deployment',label:'服务部署'},{value:'dependencies',label:'外部依赖'},{value:'policies',label:'业务策略'}]}/>
   <Table<FeatureStatus> size="small" rowKey="id" loading={status.isPending} dataSource={status.data?.items.filter(item=>category==='all'||item.category===category)??[]} pagination={{pageSize:8,showSizeChanger:false}} scroll={{x:900}} expandable={{expandedRowRender:details}} columns={[
    {title:'功能',dataIndex:'label'},
    ...(['desired','applied','dependencies','service'] as const).map((key,i)=>({title:['用户期望','配置应用','依赖就绪','实际服务'][i],key,render:(_:unknown,row:FeatureStatus)=><StateTag value={row[key]} stale={!!status.error}/> })),
    {title:'操作',render:(_:unknown,row:FeatureStatus)=><Space><Link to={settingsDestination(row.section)}>配置</Link>{row.checkSupported&&canCheck&&<Button size="small" loading={busy===row.id} disabled={!!busy||!!status.error||active} onClick={()=>void check(row.id)}>只读预检</Button>}</Space>},
   ]}/>
  </Card>
  {status.data&&<Card title="配置节点确认"><Table size="small" rowKey="id" pagination={false} scroll={{x:550}} dataSource={status.data.nodes} columns={[{title:'节点',dataIndex:'id'},{title:'在线',dataIndex:'online',render:v=>v?'在线':'离线，尚未确认当前状态'},{title:'应用状态',dataIndex:'state',render:v=>configurationStates[v]??v},{title:'最近应用版本',dataIndex:'lastAppliedRevision'}]}/>{status.data.securityPending&&<Alert type="warning" title="安全策略放宽仍等待节点确认，原有较严格规则继续有效"/>}</Card>}
  <Card title="预检任务与进度">
   {history.error&&<Alert type="error" title="无法刷新任务记录" description={history.error.message}/>}
   <Typography.Paragraph type="secondary">预检记录持久保存，刷新后可继续查看。取消只丢弃检测结果；部署任务的进度、恢复与回滚仍在服务部署页面查看。</Typography.Paragraph>
   <Table<CheckTask> size="small" rowKey="id" loading={history.isPending} dataSource={history.data?.items??[]} pagination={{pageSize:5}} scroll={{x:850}} expandable={{expandedRowRender:t=><Descriptions column={1} items={[{key:'id',label:'任务',children:t.id},{key:'node',label:'节点 / 配置版本',children:`${t.node} / ${t.revision}`},{key:'actor',label:'操作人 ID',children:t.actor},{key:'result',label:'检测结果',children:t.checks.map((c,i)=><p key={i}>{c.summary} {c.remediation}</p>)}]}/>}} columns={[{title:'功能',dataIndex:'feature',render:v=>status.data?.items.find(item=>item.id===v)?.label??v},{title:'阶段',dataIndex:'stage',render:v=>operationStages[v]??v},{title:'结果',render:(_,t)=><StateTag value={{state:t.state,scope:'node'}} stale={!!history.error}/>},{title:'更新时间',dataIndex:'updated',render:v=>new Date(v).toLocaleString()},{title:'操作',render:(_,t)=>canCheck&&t.state==='running'&&<Button disabled={!!busy||!!history.error} onClick={()=>void cancel(t.id)}>取消预检</Button>}]}/>
  </Card>
 </div>;
}
