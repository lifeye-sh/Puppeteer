import {useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {Alert,Card} from 'antd';

// A progress ticket grants only a task's redacted state. It never restores a
// cached account or authorizes a mutation while the authentication API is down.
export default function DeploymentProgressFallback(){
 const [ticket]=useState<{id:string;token:string}|null>(()=>{
  try{const value=JSON.parse(sessionStorage.getItem('deployment-progress')??'null');return typeof value?.id==='string'&&typeof value?.token==='string'?value:null}catch{return null}
 });
 const progress=useQuery({queryKey:['deployment-reconnect-progress',ticket?.id],enabled:!!ticket,retry:false,refetchInterval:3000,queryFn:async()=>{
  const response=await fetch(`/deployment-progress/${encodeURIComponent(ticket!.id)}?ticket=${encodeURIComponent(ticket!.token)}`,{cache:'no-store',referrerPolicy:'no-referrer'});
  if(!response.ok)throw Error(response.status===401?'进度票据已过期，请等待平台恢复后查看任务历史':'进度通道正在恢复连接');
  return response.json() as Promise<{state:string;stage:string;error?:string}>;
 }});
 if(!ticket)return null;
 const task=progress.data;
 const stages:Record<string,string>={preflight:'预检',apply:'部署',verify:'验证',rollback:'回滚',fence:'保持关闭',complete:'结束',interrupted:'中断恢复',journal:'日志存储异常'};
 return <Card title="部署进度" style={{maxWidth:760,margin:'24px auto'}}><Alert showIcon type={task?.state==='succeeded'?'success':task?.state==='failed'?'error':'info'} title={task?task.state==='succeeded'?'部署验证成功':task.state==='failed'?'部署失败':`正在${stages[task.stage]??'执行'}`:'正在恢复任务进度'} description={task?.error??progress.error?.message??`任务 ${ticket.id}；平台恢复后可继续管理。`}/></Card>;
}
