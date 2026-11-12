import {Alert,Button,Card,Descriptions,Form,Input,InputNumber,Popconfirm,Select,Switch,Table,Tag,Typography,Tabs} from 'antd';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal from './BackendFormModal';
import BackendBackupDestinations from './BackendBackupDestinations';
import BackendRestoreHistory from './BackendRestoreHistory';
type Config={recipient:string;retentionDays:number;scheduleEnabled:boolean;scheduleTime:string;timezone:string;scope:'control-plane'|'full';revision:number;recipientProofId?:string;recipientResponse?:string};
type BackupFile={id:string;size:number;sha256:string;createdAt:string;manifest:{scope:string;includes:string[];excludes:string[]}};
type Task={id:string;state:string;stage:string;createdAt:string;remark:string;config:Config};
const label:Record<string,string>={queued:'等待执行',preparing:'进入维护',snapshotting:'数据库快照与加密归档',verifying:'校验归档',uploading:'复制与校验远端',partial:'本地可用，远端复制未完成',succeeded:'已完成',failed:'失败',unknown:'执行中断，需检查',interrupted:'恢复后已停止'};
const scopeLabel=(s:string)=>s==='full'?'完整备份':'控制面备份';
export default function BackendBackupPage(){
 const {account}=useBackendRuntime();const client=useQueryClient();const [error,setError]=useState('');const [busy,setBusy]=useState(false);const [editing,setEditing]=useState(false);const [exporting,setExporting]=useState<BackupFile>();const [form]=Form.useForm<Config>();const [proof]=Form.useForm();
 const cfg=useQuery({queryKey:['r11-backup-config'],queryFn:()=>requests.get<Config>('/admin/backup/config',{errorMode:'local'})});
 const status=useQuery({queryKey:['r11-backup-status'],queryFn:()=>requests.get<{available:boolean;maintenance:boolean}>('/admin/backup/status',{errorMode:'local'}),refetchInterval:3000});
 const files=useQuery({queryKey:['r11-backup-files'],queryFn:()=>requests.get<BackupFile[]>('/admin/backup/files',{errorMode:'local'}),refetchInterval:5000});
 const tasks=useQuery({queryKey:['r11-backup-tasks'],queryFn:()=>requests.get<Task[]>('/admin/backup/tasks',{errorMode:'local'}),refetchInterval:3000});
 const run=async(fn:()=>Promise<unknown>)=>{setBusy(true);setError('');try{await fn();await client.invalidateQueries({predicate:q=>String(q.queryKey[0]).startsWith('r11-backup')})}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const active=tasks.data?.some(t=>['queued','running'].includes(t.state));const blocked=busy||status.data?.maintenance;
 const download=async()=>{if(!exporting)return;const input=await proof.validateFields();await run(async()=>{const result=await requests.post<{ticket:string}>(`/admin/backup/files/${exporting.id}/export-ticket`,input,{errorMode:'local'});const a=document.createElement('a');a.href=`/api/admin/backup/files/${exporting.id}/download?ticket=${encodeURIComponent(result.ticket)}`;a.download=`${exporting.id}.ntbackup.age`;a.click();proof.resetFields();setExporting(undefined)})};
 const failure=error||[cfg,status,files,tasks].map(q=>q.error?.message).find(Boolean);
 return <section className="nt-page"><Typography.Title level={2}>备份与恢复</Typography.Title>
 {failure&&<Alert type="error" title={failure} showIcon/>}
 {status.data?.maintenance&&<Alert type="warning" title="系统正在维护" description="现有会话已停止，新登录和业务操作暂不可用。本页会继续更新备份进度。" showIcon/>}
 {status.data&&!status.data.available&&<Alert type="warning" title="备份服务尚未就绪" description="请完成部署配置后再创建备份。"/>}
 <Card title="备份策略" extra={<Button disabled={blocked||!cfg.data||!account.actions?.includes('backup.export')} onClick={()=>{form.resetFields();form.setFieldsValue(cfg.data!);setEditing(true)}}>编辑策略</Button>}>
 <Descriptions size="small" column={{xs:1,sm:2,lg:3}} items={[{key:'scope',label:'包含范围',children:scopeLabel(cfg.data?.scope??'control-plane')},{key:'retention',label:'保留时间',children:`${cfg.data?.retentionDays??31} 天（至少保留最新一份）`},{key:'schedule',label:'自动备份',children:cfg.data?.scheduleEnabled?`每天 ${cfg.data.scheduleTime} · ${cfg.data.timezone}`:'未启用'},{key:'format',label:'格式',children:'.ntbackup.age · age 加密'},{key:'key',label:'恢复公钥',span:2,children:cfg.data?.recipient?<Typography.Text copyable ellipsis>{cfg.data.recipient}</Typography.Text>:'尚未配置'}]}/>
 <p>控制面备份包含配置、授权、密钥、证书和审计元数据；完整备份还包含录屏与共享存储文件。外部资产上的文件不在备份范围内。</p>
 <Popconfirm title="创建备份并进入维护？" description="会关闭现有会话，暂停新登录、业务操作和后台任务，完成后自动恢复服务。" onConfirm={()=>run(()=>requests.post('/admin/backup/tasks',{requestKey:crypto.randomUUID(),drainConfirmed:true},{errorMode:'local'}))}>
 <Button type="primary" disabled={blocked||active||!status.data?.available||!cfg.data?.recipient}>立即备份</Button></Popconfirm>
 </Card>
 <Card title="可下载备份"><Table<BackupFile> size="small" rowKey="id" dataSource={files.data} loading={files.isLoading} pagination={{pageSize:8}} scroll={{x:700}} columns={[
 {title:'创建时间',dataIndex:'createdAt',render:v=>new Date(v).toLocaleString()},
 {title:'范围',render:(_,f)=>scopeLabel(f.manifest.scope)},
 {title:'大小',render:(_,f)=>`${(f.size/2**20).toFixed(2)} MiB`},
 {title:'完整性',render:(_,f)=><Typography.Text copyable={{text:f.sha256}}>SHA-256</Typography.Text>},
 {title:'操作',render:(_,f)=><Button type="link" disabled={blocked||!account.actions?.includes('backup.export')} onClick={()=>{proof.resetFields();setError('');setExporting(f)}}>下载加密备份</Button>}
 ]}/></Card>
 <Card title="最近备份任务"><Table<Task> size="small" rowKey="id" dataSource={tasks.data} pagination={{pageSize:8}} scroll={{x:700}} columns={[{title:'创建时间',dataIndex:'createdAt',render:v=>new Date(v).toLocaleString()},{title:'范围',render:(_,t)=>scopeLabel(t.config.scope)},{title:'状态',render:(_,t)=><Tag color={t.state==='succeeded'?'success':t.state==='failed'?'error':'processing'}>{label[t.state]??t.state}</Tag>},{title:'阶段',render:(_,t)=>label[t.stage]??t.stage},{title:'任务编号',dataIndex:'id',ellipsis:true},{title:'操作',render:(_,t)=>['partial','unknown'].includes(t.state)?<Button type="link" disabled={blocked} onClick={()=>run(async()=>{await requests.post('/admin/backup/tasks/'+t.id+'/retry',{}, {errorMode:'local'})})}>重试远端复制</Button>:null}]}/></Card>
 <Tabs items={[{key:"copies",label:"远端复制",children:<BackendBackupDestinations/>},{key:"restores",label:"恢复记录",children:<BackendRestoreHistory/>}]}/><Modal title="备份策略" open={editing} onCancel={()=>{setEditing(false);form.resetFields()}} confirmLoading={busy} onOk={async()=>{const values=await form.validateFields();await run(async()=>{await requests.put('/admin/backup/config',{...values,revision:cfg.data!.revision},{errorMode:'local'});setEditing(false);form.resetFields()})}}>
 {error&&<Alert type="error" title={error} showIcon/>}
 <Alert type="info" title="妥善保存离线恢复私钥" description="备份服务只保存公钥。恢复时必须提供对应私钥；请先使用运维 CLI 生成并保存在独立位置。"/>
 <Form form={form} layout="vertical" style={{marginTop:16}}><Form.Item name="recipient" label="age 恢复公钥" rules={[{required:true}]}><Input placeholder="age1…"/></Form.Item>
 <Form.Item name="recipientProofId" hidden><Input/></Form.Item>
 <Form.Item noStyle shouldUpdate={(a,b)=>a.recipient!==b.recipient}>{()=>form.getFieldValue('recipient')!==cfg.data?.recipient?<div style={{marginBottom:16}}>
 <Button loading={busy} onClick={()=>run(async()=>{const recipient=form.getFieldValue('recipient');const result=await requests.post<{id:string;challenge:string}>('/admin/backup/recipient-challenge',{recipient},{errorMode:'local'});form.setFieldValue('recipientProofId',result.id);form.setFieldValue('recipientResponse','');const url=URL.createObjectURL(new Blob([result.challenge],{type:'text/plain'}));const link=document.createElement('a');link.href=url;link.download='recipient-challenge.txt';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000)})}>下载私钥验证文件</Button>
 <Typography.Paragraph type="secondary" style={{marginTop:8}}>使用离线私钥解密验证文件，将一次性结果填入下方。私钥始终保留在本地，验证文件 30 分钟有效。</Typography.Paragraph>
 <Typography.Paragraph code copyable>nt backup verify-recipient --identity-file recovery.agekey --challenge-file recipient-challenge.txt</Typography.Paragraph>
 <Form.Item name="recipientResponse" label="离线验证结果" rules={[{required:true,message:'请完成离线私钥验证'}]}><Input autoComplete="off"/></Form.Item>
 </div>:null}</Form.Item>
 <div className="nt-form-grid"><Form.Item name="scope" label="包含范围" rules={[{required:true}]}><Select options={[{value:'control-plane',label:'控制面：配置与审计元数据'},{value:'full',label:'完整：同时包含录屏和共享文件'}]}/></Form.Item><Form.Item name="retentionDays" label="保留天数" rules={[{required:true}]}><InputNumber min={1} max={3650}/></Form.Item><Form.Item name="scheduleEnabled" label="每日自动备份" valuePropName="checked"><Switch/></Form.Item><Form.Item name="scheduleTime" label="备份时间" rules={[{required:true,pattern:/^([01]\d|2[0-3]):[0-5]\d$/,message:'请输入 HH:mm'}]}><Input placeholder="02:00"/></Form.Item><Form.Item name="timezone" label="时区" rules={[{required:true}]}><Select options={[{value:'Asia/Shanghai',label:'北京时间'},{value:'UTC',label:'UTC'}]}/></Form.Item></div>
 <Form.Item name="password" label="当前账号密码（修改备份策略需验证身份）" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="totp" label="动态验证码（已绑定 MFA 时必填）"><Input maxLength={6} autoComplete="one-time-code"/></Form.Item><Alert type="warning" title="自动备份也会进入维护并关闭会话，请选择业务空闲时间。" description="启用或重启服务时，如果当天计划时间已过且尚未执行，会补执行当天备份。"/></Form></Modal>
 <Modal title="验证身份并下载" open={!!exporting} onCancel={()=>{setExporting(undefined);proof.resetFields()}} okText="验证并下载" confirmLoading={busy} onOk={download}>
 {error&&<Alert type="error" title={error}/>}<p>归档包含加密密钥和凭据，请保存到受控位置。</p><Form form={proof} layout="vertical"><Form.Item name="password" label="当前账号密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="totp" label="动态验证码（已绑定 MFA 时必填）"><Input autoComplete="one-time-code" maxLength={6}/></Form.Item></Form></Modal>
 </section>
}
