import {useEffect,useState} from 'react';
import {Alert,Button,Card,Checkbox,Descriptions,Form,Input,InputNumber,Modal,Select,Space,Switch,Table,Tag,Typography} from 'antd';
import {useQuery} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import {useSystemUpgrade} from './useSystemUpgrade';
import type {UpgradeTask} from './useSystemUpgrade';
import BackendUpdateAutomation from './BackendUpdateAutomation';

type Settings={revision:number;channel:string;autoCheck:boolean;intervalHours:number};
type Release={version:string;channel:string;publishedAt:string;platform:string;minSource:string;schemaVersion:number;notesUrl:string};
type History={at:string;success:boolean;reason?:string;sequence?:number};
type View={settings:Settings;state:string;reason?:string;lastAttempt:string;lastSuccess:string;expiresAt?:string;sequence:number;releases:Release[];history:History[]};
type Status={currentVersion:string;schemaVersion:number;capabilitiesRevision:number;updates:View;automationEndpoint:string};
type Component={service:string;configuredImage:string;runningImage?:string;imageId?:string;version?:string;state:string;health?:string;disabled:boolean};
type Inventory={inventory:{executorVersion:string;platform:string;managed:boolean;systemd:boolean;runnerReady:boolean;reason?:string;components:Component[]};busy:boolean};
type Preflight={id:string;version:string;passed:boolean;layoutDigest:string;generation:number;notice:string;checks:{key:string;passed:boolean;reason:string}[]};
const opts={errorMode:'local' as const};
const labels:Record<string,string>={not_checked:'尚未检查',checking:'正在检查',update_available:'发现新版本',up_to_date:'后端版本已匹配渠道最新版本',ahead_of_channel:'当前版本高于渠道版本',no_channel_release:'当前渠道暂无版本',custom_build:'版本需要人工核验',expired:'索引已过期',check_failed:'检查失败'};
const stages:Record<string,string>={version:'版本比较',path:'升级路径',schema:'数据库版本',executor:'部署执行器',platform:'运行平台',managed:'部署接管',systemd:'宿主机服务管理',idle:'部署任务互斥',runner:'独立升级器',full_preflight:'升级准备与备份'};
const time=(v?:string)=>!v||v.startsWith('0001-')?'—':new Date(v).toLocaleString();
const upgradeStages:Record<string,string>={queued:'等待独立进程',preparing:'准备镜像与检查空间',quiescing:'排空会话与停止写入',backing_up:'备份与校验',switching:'切换版本与执行器',verifying:'验证候选版本',committing:'开放业务并核验',rolling_back:'恢复配套数据',rollback_committing:'恢复原版本业务',succeeded:'升级成功',rolled_back:'已恢复原版本',failed:'准备失败，原部署未改变',recovery_required:'需要处理恢复故障，业务保持关闭'};
stages.host_preflight='宿主机与应用预检';
upgradeStages.prepared='镜像准备完成，未停止业务';

export default function BackendSystemUpdates(){
 const {account}=useBackendRuntime();const allowed=account.type==='admin'&&!!account.actions?.includes('identity.manage');
 const [form]=Form.useForm<Settings>();const [busy,setBusy]=useState(''),[error,setError]=useState(''),[preflight,setPreflight]=useState<Preflight>();
 const [proof]=Form.useForm();const [confirming,setConfirming]=useState(false);
 const upgrade=useSystemUpgrade(allowed);const upgrading=!!upgrade.task&&!upgrade.task.terminal;
 const status=useQuery({queryKey:['system-updates'],queryFn:()=>requests.get<Status>('/admin/updates',opts),enabled:allowed,retry:false});
 const inventory=useQuery({queryKey:['system-update-inventory'],queryFn:()=>requests.get<Inventory>('/admin/updates/inventory',opts),enabled:allowed,retry:false,staleTime:30000});
 const settings=status.data?.updates.settings;
 useEffect(()=>{if(settings)form.setFieldsValue(settings)},[settings,form]);
 const run=async(key:string,action:()=>Promise<unknown>)=>{setBusy(key);setError('');try{await action()}catch(e){setError((e as Error).message)}finally{setBusy('')}};
 const submit=async()=>{
  if(!preflight?.passed)return;
  let values;try{values=await proof.validateFields()}catch{return}
  await run('upgrade',async()=>{
   try{
    const response=await requests.post<{task:UpgradeTask;notice?:string}>('/admin/updates/tasks',{id:preflight.id,version:preflight.version,layoutDigest:preflight.layoutDigest,generation:preflight.generation,...values},opts);
    upgrade.accept(response.task);setConfirming(false);setPreflight(undefined);if(response.notice)setError(response.notice);
   }finally{proof.resetFields(['password','totp']);upgrade.refresh()}
  });
 };
 if(!allowed)return <Alert type="warning" title="仅平台管理员可管理系统更新"/>;
 const data=status.data,updates=data?.updates,host=inventory.data?.inventory;
 return <Space orientation="vertical" size="middle" style={{width:'100%'}}>
  <Space wrap><Typography.Title level={3} style={{margin:0}}>系统更新</Typography.Title><Button type="primary" loading={busy==='check'} disabled={!!busy||upgrading} onClick={()=>void run('check',async()=>{setPreflight(undefined);try{await requests.post('/admin/updates/check',{},opts)}finally{await status.refetch()}})}>检查更新</Button><Button disabled={!!busy} onClick={()=>void run('refresh',async()=>{upgrade.refresh();await Promise.all([status.refetch(),inventory.refetch()]);setPreflight(undefined)})}>刷新状态</Button></Space>
  {(error||status.error)&&<Alert type="error" showIcon title={error||status.error?.message}/>}
  {upgrade.connection&&<Alert type="warning" showIcon title={upgrade.connection}/>}
  {upgrade.task&&<Card title={`升级任务：${upgrade.task.source} → ${upgrade.task.target}`}>
   <Alert showIcon type={upgrade.task.stage==='succeeded'?'success':['failed','recovery_required'].includes(upgrade.task.stage)?'error':upgrade.task.stage==='rolled_back'?'warning':'info'} title={upgradeStages[upgrade.task.stage]??upgrade.task.stage} description={upgrade.task.reason||'升级由宿主机独立执行，关闭此页面不会取消任务。'}/>
   <Table size="small" style={{marginTop:12}} rowKey={(_,i)=>String(i)} pagination={false} dataSource={upgrade.task.events} columns={[{title:'阶段',dataIndex:'stage',render:(s:string)=>upgradeStages[s]??s},{title:'时间',dataIndex:'at',render:time}]}/>
   {upgrade.task.terminal&&<Button style={{marginTop:12}} onClick={()=>window.location.reload()}>重新加载管理入口</Button>}
  </Card>}
  {data&&<Card title="当前版本"><Descriptions column={2} items={[
   {key:'version',label:'后端版本',children:data.currentVersion},{key:'schema',label:'数据库版本',children:data.schemaVersion},
   {key:'executor',label:'执行器版本',children:host?.executorVersion??'未获取'},{key:'platform',label:'执行器平台',children:host?.platform??'未获取'},
   {key:'status',label:'检查状态',children:<Tag color={updates?.state==='update_available'?'blue':updates?.state==='up_to_date'?'green':'default'}>{labels[updates?.state??'']??updates?.state}</Tag>},
   {key:'last',label:'上次成功检查',children:time(updates?.lastSuccess)},
   {key:'attempt',label:'最近检查',children:time(updates?.lastAttempt)},{key:'expiry',label:'索引有效期',children:time(updates?.expiresAt)},
  ]}/><Typography.Paragraph type="secondary" style={{marginTop:12,marginBottom:0}}>版本比较基于当前后端标识；请同时核对下方组件镜像，混用版本或自定义镜像需要进一步验证。</Typography.Paragraph>{updates?.reason&&<Alert style={{marginTop:12}} type="warning" title={updates.reason} showIcon/>}</Card>}
  <Card title="实际运行组件">
   {inventory.error&&<Alert type="warning" showIcon title={inventory.error.message}/>}
   <Table<Component> size="small" rowKey="service" pagination={false} dataSource={host?.components??[]} scroll={{x:800}} columns={[
    {title:'组件',dataIndex:'service'},{title:'版本',render:(_,c)=>c.version||'未标注'},
    {title:'状态',render:(_,c)=><Tag color={c.disabled?'default':c.health==='healthy'?'green':'orange'}>{c.disabled?'配置为停用':`${c.state} / ${c.health||'无健康记录'}`}</Tag>},
    {title:'运行镜像',render:(_,c)=><Typography.Text style={{wordBreak:'break-all'}}>{c.runningImage||'—'}</Typography.Text>},
    {title:'配置一致性',render:(_,c)=>!c.runningImage?'未验证':c.runningImage===c.configuredImage?'一致':'不一致'},
   ]}/>
  </Card>
  <Card title="更新设置"><Form form={form} layout="inline" onFinish={values=>void run('settings',async()=>{await requests.put('/admin/updates/settings',{...values,revision:settings?.revision},opts);setPreflight(undefined);await status.refetch()})}>
   <Form.Item name="channel" label="更新渠道"><Select style={{width:130}} options={[{value:'beta',label:'Beta 测试版'},{value:'stable',label:'稳定版'}]}/></Form.Item>
   <Form.Item name="autoCheck" label="自动检查" valuePropName="checked"><Switch/></Form.Item>
   <Form.Item name="intervalHours" label="间隔（小时）" rules={[{required:true}]}><InputNumber min={1} max={168}/></Form.Item>
   <Button htmlType="submit" loading={busy==='settings'} disabled={!!busy||!settings||upgrading}>保存</Button>
  </Form><Typography.Paragraph type="secondary" style={{marginTop:12,marginBottom:0}}>自动检查只获取发布信息，不下载镜像或安装更新。连接使用服务器已配置的 HTTPS 出站代理。</Typography.Paragraph></Card>
  <Card title="可用版本">
   <Alert type="info" showIcon title="预检后由管理员确认升级" description="支持已接入独立升级器的标准 Linux 受管部署。提交后先准备镜像和检查空间，通过后停止会话、制作备份，再切换版本。升级期间管理入口会短暂重启。"/>
   <Table<Release> style={{marginTop:12}} rowKey="version" dataSource={updates?.releases??[]} pagination={{pageSize:5}} scroll={{x:700}} columns={[
    {title:'版本',render:(_,r)=><a href={r.notesUrl} target="_blank" rel="noopener noreferrer">{r.version}</a>},
    {title:'发布时间',dataIndex:'publishedAt',render:time},{title:'最低来源版本',dataIndex:'minSource'},{title:'数据库版本',dataIndex:'schemaVersion'},
    {title:'操作',render:(_,r)=><Button disabled={!!busy||upgrading||['expired','check_failed','checking'].includes(updates?.state??'')} loading={busy===r.version} onClick={()=>void run(r.version,async()=>{setPreflight(undefined);setPreflight(await requests.post<Preflight>('/admin/updates/preflight',{version:r.version},opts))})}>升级预检</Button>},
   ]}/>
   {preflight&&<Card size="small" title={`${preflight.version} · ${preflight.passed?'预检通过':'尚不满足自动升级条件'}`}><Table rowKey="key" size="small" pagination={false} dataSource={preflight.checks} columns={[
    {title:'检查项',render:(_,c)=>stages[c.key]??c.key.replace('service_','组件 ')},{title:'结果',render:(_,c)=><Tag color={c.passed?'green':'orange'}>{c.passed?'通过':'未通过'}</Tag>},{title:'说明',dataIndex:'reason'},
   ]}/><Typography.Paragraph style={{marginTop:12}}>{preflight.notice}</Typography.Paragraph><Button type="primary" disabled={!preflight.passed||!!busy||upgrading} onClick={()=>{proof.resetFields();setError('');setConfirming(true)}}>确认升级到 {preflight.version}</Button></Card>}
  </Card>
  <BackendUpdateAutomation disabled={upgrading||!!busy} mfa={account.mfaEnabled} releases={updates?.releases??[]}/>
  <Card title="检查历史"><Table<History> size="small" rowKey={(v,i)=>`${v.at}-${i}`} dataSource={updates?.history??[]} pagination={{pageSize:5}} columns={[
   {title:'时间',dataIndex:'at',render:time},{title:'结果',render:(_,v)=><Tag color={v.success?'green':'red'}>{v.success?'签名验证通过':'检查失败'}</Tag>},{title:'发布序号',dataIndex:'sequence'},{title:'原因',dataIndex:'reason'},
  ]}/></Card>
  <Modal open={confirming} title={`升级到 ${preflight?.version??''}`} okText="开始升级" confirmLoading={busy==='upgrade'} onCancel={()=>{if(busy!=='upgrade'){setConfirming(false);proof.resetFields()}}} onOk={()=>void submit()}>
   <Alert type="warning" showIcon title="升级会停止当前会话和业务写入" description="切换前会制作并校验配套备份。切换或验证失败时恢复旧版本及配套数据；开放业务后不会自动覆盖新写入。"/>
   {error&&<Alert style={{marginTop:12}} type="error" title={error}/>}
   <Form form={proof} layout="vertical" style={{marginTop:16}}>
    <Form.Item name="confirmedStop" valuePropName="checked" rules={[{validator:(_,v)=>v===true?Promise.resolve():Promise.reject(new Error('请确认停机影响'))}]}><Checkbox>已确认当前可以停止会话并进入升级维护</Checkbox></Form.Item>
    <Form.Item name="password" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item>
    <Form.Item name="totp" label="新的动态验证码" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
   </Form>
  </Modal>
 </Space>;
}
