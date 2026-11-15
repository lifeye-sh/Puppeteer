import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Card,Form,Input,Space,Switch,Tag,Typography} from 'antd';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import requests from '@/api/core/requests';

type Kind='agent-repository'|'wal'|'web-access';
type Task={id:string;state:string;stage:string;error?:string;progressToken?:string;request?:{protocol:string}};
type State={supported:boolean;desired:string;applied:string;dependencies:string;service:string;reason:string;checkedAt?:string;schedule?:{full:string;backup:string;check:string;error?:string}};
type Status={generation:number;task?:Task;operations?:Record<string,State>};
const labels:Record<Kind,string>={'agent-repository':'Agent 更新仓库',wal:'WAL 归档与数据库恢复','web-access':'网页入口访问限制'};
const states:Record<string,string>={enabled:'启用',disabled:'停用',applied:'已应用',applying:'应用中',checking:'检测中',unknown:'未验证',ready:'已通过',blocked:'未通过',stale:'已过期',verified:'本机验证通过',recoverable:'数据库恢复链路已验证',failed:'失败',stopped:'已停止'};
const stages:Record<string,string>={preflight:'检查部署条件',apply:'准备部署',apply_nginx:'应用 Nginx 配置',verify:'验证',verify_operations:'验证入口与发布结果',wal_prepare:'准备独立加密密钥',wal_image:'构建固定 PG18 归档镜像',wal_restart:'更新 PostgreSQL 服务',wal_stanza:'初始化并检查归档',wal_backup:'创建完整基础备份',wal_archive:'验证恢复点归档',wal_restore:'隔离恢复验证与清理',rollback:'恢复原配置',fence:'完成停用',complete:'完成',interrupted:'中断恢复'};
async function api<T>(path:string,body?:unknown):Promise<T>{const r=await fetch('/api/admin/deployment/'+path,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json'},...(body===undefined?{}:{body:JSON.stringify(body)})});const b=await r.json();if(!r.ok)throw Error(b.message??b.error??'专项部署失败');return b}

export default function BackendOperations({kind}:{kind:Kind}){
 const {account}=useBackendRuntime(),cache=useQueryClient(),[form]=Form.useForm();
 const [busy,setBusy]=useState(false),[error,setError]=useState(''),[phase,setPhase]=useState('');
 const [ticket,setTicket]=useState<{id:string;token:string;protocol?:Kind}|undefined>(()=>{try{return JSON.parse(sessionStorage.getItem('deployment-progress')??'null')??undefined}catch{return undefined}});
 const status=useQuery({queryKey:['deployment-status'],queryFn:()=>api<Status>('status'),refetchInterval:5000,retry:false});
 const source=useQuery({queryKey:['diagnostic-source'],queryFn:()=>requests.get<{sourceIp:string;trustedProxyApplied:boolean}>('/admin/diagnostics/source',{errorMode:'local'}),enabled:kind==='web-access',retry:false});
 const progress=useQuery({queryKey:['deployment-progress',ticket?.id],enabled:!!ticket,queryFn:async()=>{const r=await fetch(`/deployment-progress/${encodeURIComponent(ticket!.id)}?ticket=${encodeURIComponent(ticket!.token)}`,{cache:'no-store',referrerPolicy:'no-referrer'});if(!r.ok)throw Error('独立进度通道不可用');return r.json() as Promise<Task>},refetchInterval:5000,retry:false});
 const row=status.data?.operations?.[kind],currentTask=status.error?progress.data??status.data?.task:status.data?.task;
 const task=currentTask&&(currentTask.request?.protocol===kind||(ticket?.protocol===kind&&currentTask.id===ticket.id))?currentTask:undefined;
 const enabled=Form.useWatch('enabled',form);
 const submit=async()=>{setBusy(true);setError('');try{
  const v=await form.validateFields();const latest=await api<Status>('status');
  const request={protocol:kind,enabled:v.enabled,bind:'',port:0,generation:latest.generation,...(kind==='web-access'?{allowCidrs:v.enabled?v.allowCidrs:''}:{}),...(kind==='agent-repository'&&v.enabled?{repository:v.repository,trustRoot:v.trustRoot}:{})};
  setPhase('正在预检；失败时不会改变运行配置');await api('preflight',{request});setPhase('预检通过，正在登记执行任务');
  const accepted=await api<Task>('tasks',{request,password:v.password,totp:v.totp});if(accepted.progressToken){const t={id:accepted.id,token:accepted.progressToken,protocol:kind};setTicket(t);sessionStorage.setItem('deployment-progress',JSON.stringify(t))}
  form.setFieldsValue({password:'',totp:''});setPhase('');await cache.invalidateQueries({queryKey:['deployment-status']});
 }catch(e){setError(e instanceof Error?e.message:'请检查输入');setPhase('')}finally{setBusy(false)}};
 return <Card title={labels[kind]}><Space orientation="vertical" size={16} style={{width:'100%'}}>
  <Alert type={kind==='wal'?'warning':'info'} showIcon title={kind==='wal'?'启停将重建 PostgreSQL 容器并短暂中断数据库连接':'操作前预检，自动应用、验证并在失败时恢复'} description={kind==='wal'?'自动配置独立加密密钥、本地仓库和保留两份完整备份的策略；首份备份、WAL 归档与隔离恢复通过后开启每周 full、每天 diff、每 5 分钟归档检查。停用保留仓库和密钥。本地数据库备份不替代异地副本、应用文件及离线密钥备份。':kind==='agent-repository'?'提供 HTTPS 签名仓库与从独立可信渠道取得的 1.root.json。自动验证根轮换链、元数据有效期和目标摘要，原子部署只读导出目录。后续发布不能降低序号或替换初始信任根。入口为 /agent-updates/。':'输入允许访问的 CIDR 网段。必须包含当前管理员来源 IP；平台只生成受限 Nginx 策略，保留容器回环恢复入口，不修改防火墙或安全组。'}/>
  {kind==='web-access'&&<Typography.Text>当前识别 IP：{source.data?.sourceIp??'读取中'}；可信代理提取：{source.data?.trustedProxyApplied?'已确认':'未确认，开启前需配置客户端 IP 策略'}</Typography.Text>}
  {row&&<><Space wrap>{([['期望',row.desired],['配置应用',row.applied],['依赖',row.dependencies],['实际结果',row.service]] as const).map(([label,state])=><Tag key={label}>{label}：{states[status.error?'stale':state]??state}</Tag>)}</Space><Typography.Text type="secondary">{row.reason}{row.checkedAt?` · ${new Date(row.checkedAt).toLocaleString()}`:''}</Typography.Text>{row.schedule&&<Typography.Text>最近备份：{new Date(row.schedule.backup).toLocaleString()}；归档检查：{new Date(row.schedule.check).toLocaleString()}。{row.schedule.error}</Typography.Text>}</>}
  {(status.error||error)&&<Alert type="error" title={error||'部署状态刷新失败，旧结果已过期'} description={status.error?.message}/>}
  {status.data&&!row?.supported&&<Alert type="warning" title="当前安装未接管此专项能力" description="请更新匹配的安装/升级脚本。自定义或外部组件不会被自动覆盖。"/>}
  {phase&&<Alert type="info" title={phase}/>}
  {currentTask?.state==='running'&&!task&&<Alert type="info" title="另一项部署正在执行，请等待完成"/>}
  {task&&<Alert type={task.state==='failed'?'error':task.state==='succeeded'?'success':'info'} title={`${stages[task.stage]??task.stage} · ${task.state==='running'?'执行中':task.state==='succeeded'?'成功':'失败'}`} description={task.error??`任务 ${task.id}；刷新后仍可追踪`}/>}
  <Form form={form} name={'operations-'+kind} layout="vertical" initialValues={{enabled:true}} style={{width:'100%',maxWidth:600}}>
   <Form.Item name="enabled" label={kind==='web-access'?'启用入口限制':'启用服务'} valuePropName="checked"><Switch/></Form.Item>
   {kind==='web-access'&&enabled&&<Form.Item name="allowCidrs" label="允许访问的 CIDR（逗号或换行分隔）" rules={[{required:true,max:4096}]}><Input.TextArea rows={4} placeholder="203.0.113.42/32&#10;10.0.0.0/8"/></Form.Item>}
   {kind==='agent-repository'&&enabled&&<><Form.Item name="repository" label="HTTPS 签名仓库地址" rules={[{required:true,type:'url',max:2048}]}><Input placeholder="https://updates.example.com/agent"/></Form.Item><Form.Item name="trustRoot" label="独立核验的 1.root.json 内容（公开密钥）" rules={[{required:true,max:32768}]}><Input.TextArea rows={6}/></Form.Item></>}
   <Form.Item name="password" label="管理员密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
   <Form.Item name="totp" label="新的动态口令" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input maxLength={6} autoComplete="one-time-code"/></Form.Item>
   <Button type="primary" loading={busy} disabled={!row?.supported||!!status.error||currentTask?.state==='running'} onClick={()=>void submit()}>预检并执行</Button>
  </Form>
 </Space></Card>;
}
