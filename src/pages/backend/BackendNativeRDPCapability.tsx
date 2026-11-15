import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Collapse,Space,Tag,Typography} from 'antd';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import {type ConfigurationStatus,type NativeRDPCapability,operationStages,operationStates} from './configuration-status-model';

export function NativeRDPReport({value,stale=false}:{value:NativeRDPCapability;stale?:boolean}){
 return <Space orientation="vertical" style={{width:'100%'}}>
  <Typography.Text>部署模式：{({loopback:'普通回环监听','windows-local':'显式本机容器 profile',unavailable:'未部署'} as Record<string,string>)[value.profile]??'未知'}；会话容量 {value.capacity.inUse} / {value.capacity.limit}，当前剩余 {value.capacity.available}。容量不作预留。</Typography.Text>
  {value.checks.map(c=><div key={c.code}><Tag color={stale?'default':c.state==='ready'?'green':'red'}>{stale?'上次观测':c.state==='ready'?'通过':'未通过'}</Tag>{c.summary}{c.state!=='ready'&&c.remediation&&<Typography.Paragraph type="secondary">{c.remediation}</Typography.Paragraph>}</div>)}
 </Space>;
}

export default function BackendNativeRDPCapability(){
 const {account}=useBackendRuntime();const cache=useQueryClient();const [busy,setBusy]=useState(false),[error,setError]=useState('');
 const query=useQuery({queryKey:['configuration-operations'],queryFn:()=>requests.get<ConfigurationStatus>('/settings/operations',{errorMode:'local'}),refetchInterval:5000,retry:false});
 const row=query.data?.items.find(i=>i.id==='native-rdp');const task=row?.lastCheck;
 const canCheck=account.type==='admin'&&!!account.actions?.includes('identity.manage');
 const check=async()=>{if(!query.data)return;setBusy(true);setError('');try{
  const response=await fetch('/api/settings/operations/native-rdp/preflight',{method:'POST',headers:{'Content-Type':'application/json','If-Match':`"settings-${query.data.revision}"`},body:'{}'});
  const body=await response.json();if(!response.ok)throw Error(body.message??'能力预检失败');
  await cache.invalidateQueries({queryKey:['configuration-operations']});await cache.invalidateQueries({queryKey:['configuration-operation-tasks']});
 }catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <Space orientation="vertical" style={{width:'100%',marginBottom:16}}>
  <Alert showIcon type="info" title="原生 RDP：开关允许不代表具备部署能力" description="普通部署仅允许本机回环监听；显式 windows-local 容器 profile 仍需宿主回环映射。能力预检只检查已保存配置与当前节点，不安装组件、不开放宿主端口，也不验证完整目标会话。"/>
  {(query.error||error)&&<Alert type="error" title={error||'无法刷新原生 RDP 能力'} description={query.error?.message}/>}
  {row?.capability?<>
   <Space wrap><Tag>{row.capability.supported?'已登记专用部署':'当前部署不支持'}</Tag><Tag color={!query.error&&row.capability.staticReady?'green':'default'}>{query.error?'状态已过期':row.capability.staticReady?'静态条件通过':'静态条件未通过'}</Tag><Tag color={!query.error&&row.dependencies.state==='ready'?'green':!query.error&&row.dependencies.state==='blocked'?'red':'default'}>预检证据：{query.error?'已过期':operationStates[row.dependencies.state]??row.dependencies.state}</Tag><Typography.Text>{query.error?'运行状态未确认':`实际服务：${operationStates[row.service.state]??row.service.state}`}</Typography.Text></Space>
   <Typography.Text type="secondary">{row.dependencies.reason}。{row.service.reason}</Typography.Text>
   <Collapse size="small" items={[{key:'capability',label:'查看 Worker、编码器、证书与容量检查',children:<NativeRDPReport value={row.capability} stale={!!query.error}/>}]}/>
  </>:!query.isPending&&!query.error&&<Alert type="warning" title="当前服务端未提供原生 RDP 能力报告，请更新匹配版本"/>}
  {task&&<Alert type={task.state==='failed'?'warning':'info'} title={`最近能力预检：${operationStates[task.state]??task.state} · ${operationStages[task.stage]??task.stage}`} description={task.checks.map(c=><div key={c.code}>{c.summary}{c.state==='blocked'?`；${c.remediation??''}`:''}</div>)}/>}
  {canCheck&&<Button disabled={!row?.capability||!!query.error||busy||task?.state==='running'} loading={busy} onClick={()=>void check()}>原生 RDP 能力预检</Button>}
 </Space>;
}
