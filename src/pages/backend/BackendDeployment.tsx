import {useState,useEffect} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Card,Form,Input,InputNumber,Select,Space,Switch,Table,Tag,Typography} from 'antd';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';

type Task={progressToken?:string;id:string;state:string;stage:string;error?:string;updated:string;request?:{protocol:string;enabled:boolean;siteId?:string}};
type Status={components?:Record<string,{supported:boolean;enabled:boolean;state?:string;reason?:string}>;generation:number;task?:Task;history?:Task[];mappings?:Record<string,{bind:string;published:number}>};
async function api<T>(path:string,body?:unknown):Promise<T>{
 const response=await fetch(`/api/admin/deployment/${path}`,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json'},...(body===undefined?{}:{body:JSON.stringify(body)})});
 const result=await response.json();if(!response.ok)throw Error(result.message??result.error??'部署请求失败');return result;
}
export default function BackendDeployment({site,mode='proxies'}:{site?:{id:string;version:number;enabled:boolean};mode?:'proxies'|'components'|'drive'|'mp4'}={}){
 const componentMode=mode!=='proxies';const componentKey=mode==='mp4'?'mp4':mode==='drive'?'rdp-drive':'graphics';
 const {account}=useBackendRuntime();const [form]=Form.useForm();const queryClient=useQueryClient();
 const [enabledEdited,setEnabledEdited]=useState(false);
 const [busy,setBusy]=useState(false),[error,setError]=useState(''),[stage,setStage]=useState('');
 const status=useQuery({queryKey:['deployment-status'],queryFn:()=>api<Status>('status'),refetchInterval:3000,retry:false});
 const [ticket,setTicket]=useState<{id:string;token:string}|undefined>(()=>{try{return JSON.parse(sessionStorage.getItem('deployment-progress')??'null')??undefined}catch{return undefined}});
 const progress=useQuery({queryKey:['deployment-progress',ticket?.id],enabled:!!ticket,queryFn:async()=>{
  const response=await fetch(`/deployment-progress/${encodeURIComponent(ticket!.id)}?ticket=${encodeURIComponent(ticket!.token)}`,{cache:'no-store',referrerPolicy:'no-referrer'});
  if(!response.ok)throw Error('进度通道暂不可用');return response.json() as Promise<Task>;
 },refetchInterval:3000,retry:false});
 useEffect(()=>{const mapping=status.data?.mappings?.website;if(site&&mapping&&!form.isFieldsTouched(['bind','port']))form.setFieldsValue({bind:mapping.bind,port:mapping.published})},[status.data?.mappings,site,form]);
 useEffect(()=>{if(status.data?.task?.state==='succeeded'&&status.data.task.id===ticket?.id)void queryClient.invalidateQueries({predicate:q=>String(q.queryKey[0]).startsWith('r09-')})},[status.data?.task?.state,status.data?.task?.id,ticket?.id,queryClient]);
 useEffect(()=>{if(componentMode&&status.data?.components?.[componentKey]&&!enabledEdited)form.setFieldValue('enabled',status.data.components[componentKey].enabled)},[mode,componentKey,status.data?.components,enabledEdited,form]);
 const protocol=Form.useWatch('protocol',form);
 const enabled=Form.useWatch('enabled',form);
 const submit=async()=>{
  setBusy(true);setError('');
  try{
   const values=await form.validateFields();
   const fresh=await api<Status>('status');
   const request={protocol:values.protocol,enabled:values.enabled,bind:['graphics','rdp-drive','mp4'].includes(values.protocol)?'':values.bind,port:['graphics','rdp-drive','mp4'].includes(values.protocol)?0:values.port,generation:fresh.generation,...(site?{siteId:site.id,siteVersion:site.version,siteEnabled:values.enabled}:{})};
   setStage('正在预检宿主机与部署依赖');
   await api('preflight',{request});
   setStage('预检通过，正在提交部署任务');
   const accepted=await api<Task>('tasks',{request,password:values.password,totp:values.totp});
   if(accepted.progressToken){const next={id:accepted.id,token:accepted.progressToken};setTicket(next);sessionStorage.setItem('deployment-progress',JSON.stringify(next))}
   form.setFieldsValue({password:'',totp:''});setStage('任务已提交，等待部署结果');
   await queryClient.invalidateQueries({queryKey:['deployment-status']});
  }catch(e){setStage('');setError(e instanceof Error?e.message:'请检查表单或刷新重试')}finally{setBusy(false)}
 };
 const task=status.error?progress.data??status.data?.task:status.data?.task;const running=task?.state==='running';
 const names:Record<string,string>={preflight:'预检',apply:'部署',verify:'验证',rollback:'回滚',fence:'保持关闭',complete:'结束',interrupted:'中断恢复',journal:'日志存储异常'};
 return <Card title={site?"网站启停部署":componentMode?(mode==='mp4'?"MP4 渲染器自动部署":mode==='drive'?"RDP 共享盘自动部署":"图形组件自动部署"):"代理自动部署"}>
  <Typography.Paragraph type="secondary">操作前自动预检，提交后查看执行阶段和失败原因。部署可能重建应用容器，连接恢复后会继续查询任务。完整执行能力以宿主机预检结果为准。</Typography.Paragraph>
  {componentMode&&<Alert showIcon type="warning" title="操作将重建应用容器，现有连接会中断" description={mode==='mp4'?"开启前检查密钥、缓存空间和组件配置，启动后实际转换内置样例，通过后恢复队列。停用会暂停并保留未完成任务；原始录屏和现有 MP4 缓存保留，授权下载仍可使用。渲染器只使用内部网络，不开放宿主端口。":mode==='drive'?"开启前检查 FUSE、存储配置卷和宿主安全能力，自动加载限定挂载策略并验证读写。关闭会断开现有连接、卸载共享盘并移除挂载权限，保留图形服务和网页 MyZone 数据。":"启用时自动拉取已登记镜像、启动 guacd 并验证 RDP/VNC 协议；停用时关闭新图形连接并停止组件。已有共享盘配置和数据卷保留；仅使用内部网络，不开放宿主端口。"}/>}
  {componentMode&&status.data?.components?.[componentKey]?.supported&&<Typography.Paragraph>部署配置：{status.data.components[componentKey].enabled?'已启用':'已停用'}；实际组件：{({running:'运行中',stopped:'已停止',failed:'异常',unknown:'未知'} as Record<string,string>)[status.data.components[componentKey].state??'unknown']}。{status.data.components[componentKey].reason}</Typography.Paragraph>}
  {componentMode&&status.data&&!status.data.components?.[componentKey]?.supported&&<Alert showIcon type="error" title={mode==='mp4'?"当前部署不支持自动管理 MP4 渲染器":mode==='drive'?"当前部署不支持自动管理共享盘":"当前部署不支持自动管理图形组件"} description={status.data.components?.[componentKey]?.reason??'执行器版本尚未提供此组件的部署能力，请更新匹配的安装/升级版本。'}/>}
  {status.error&&<Alert showIcon type={progress.data?"info":"error"} title={progress.data?"应用正在重建，已切换到独立进度通道":"无法连接部署执行器"} description={progress.data?undefined:status.error.message}/>}
  {error&&<Alert showIcon type="error" title={error}/>}
  {busy&&stage&&<Alert showIcon type="info" title={stage}/>}
  {task&&<Alert showIcon type={task.state==='succeeded'?'success':task.state==='failed'?'error':'info'} title={<Space><Tag>{names[task.stage]??task.stage}</Tag>{task.state==='succeeded'?'部署验证成功':task.state==='failed'?'部署失败':'正在执行'}</Space>} description={task.error??`任务 ${task.id}`}/>}
  <Form name={`deployment-${site?.id??mode}`} form={form} onValuesChange={changed=>{if(Object.prototype.hasOwnProperty.call(changed,'enabled'))setEnabledEdited(true)}} layout="vertical" initialValues={{protocol:componentMode?componentKey:site?'website':'mysql',enabled:site?!site.enabled:true,bind:'127.0.0.1',port:site?18443:13306}} style={{maxWidth:520}}>
   <Form.Item name="protocol" label={componentMode?'组件':'代理'} rules={[{required:true}]}><Select disabled={!!site} options={componentMode?[{value:componentKey,label:mode==='mp4'?'MP4 渲染器':mode==='drive'?'RDP 共享盘 / MyZone':'浏览器 RDP / VNC（guacd）'}]:[{value:'mysql',label:'MySQL'},{value:'postgres',label:'PostgreSQL'},{value:'website',label:'网站 HTTPS 网关'},{value:'ssh',label:'SSH'},{value:'management',label:'管理 HTTP（容器内）'}]} onChange={value=>{form.setFieldValue('port',({mysql:13306,postgres:15432,website:18443,ssh:2022,management:8089} as Record<string,number>)[value]);if(value==='management')form.setFieldsValue({bind:'0.0.0.0',enabled:true})}}/></Form.Item>
   <Form.Item name="enabled" label={site?"启用此网站":componentMode?(mode==='mp4'?"启用 MP4 渲染器":mode==='drive'?"启用 RDP 共享盘":"启用图形组件"):"开放服务"} valuePropName="checked"><Switch disabled={protocol==='management'}/></Form.Item>
   {!['graphics','rdp-drive','mp4'].includes(protocol)&&(enabled||site)&&<><Form.Item name="bind" label={protocol==='management'?'容器监听地址':'宿主机监听地址'} extra="127.0.0.1 仅供本机访问；远程入口需使用宿主机可用地址；本功能不配置防火墙或云安全组。" rules={[{required:true}]}><Input disabled={protocol==='management'}/></Form.Item><Form.Item name="port" label={protocol==='management'?'容器管理端口（自动同步 Nginx）':'宿主机端口'} rules={[{required:true}]}><InputNumber min={1} max={65535}/></Form.Item></>}
   <Form.Item name="password" label="管理员密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
   <Form.Item name="totp" label="新的动态口令" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
   <Button type="primary" loading={busy} disabled={!!status.error||!status.data||running||(componentMode&&!status.data?.components?.[componentKey]?.supported)||!!(site&&ticket?.id===task?.id&&task?.state==='succeeded')} onClick={()=>void submit()}>预检并执行</Button>
  </Form>
  {!!status.data?.history?.length&&<Table<Task> style={{marginTop:24}} size="small" rowKey="id" dataSource={status.data.history} pagination={{pageSize:5}} columns={[
   {title:'最近部署',dataIndex:'id',render:(id:string)=><Typography.Text copyable={{text:id}}>{id.slice(0,12)}</Typography.Text>},
   {title:'服务',render:(_:unknown,row:Task)=>row.request?.protocol??'—'},
   {title:'阶段',render:(_:unknown,row:Task)=>names[row.stage]??row.stage},
   {title:'结果',render:(_:unknown,row:Task)=><Tag color={row.state==='succeeded'?'success':row.state==='failed'?'error':'processing'}>{row.state==='succeeded'?'成功':row.state==='failed'?'失败':'执行中'}</Tag>},
   {title:'说明',dataIndex:'error'},
   {title:'时间',dataIndex:'updated',render:(value:string)=>new Date(value).toLocaleString()},
  ]}/>}
 </Card>;
}
