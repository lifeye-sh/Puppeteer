import {Alert,Button,Form,Input,Select,Switch,Table,Tag} from 'antd';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal from './BackendFormModal';
type Destination={id:string;name:string;kind:'s3'|'sftp'|'webdav';enabled:boolean;revision:number;endpoint:string;prefix:string;bucket:string;region:string;username:string;hostKey:string;ca:string;allowHttp:boolean;hasSecret:boolean};
type Copy={taskId:string;destinationId:string;name:string;remoteKey:string;state:string};
export default function BackendBackupDestinations(){
 const {account}=useBackendRuntime();const canEdit=account.actions?.includes('backup.export');const cache=useQueryClient();const [editing,setEditing]=useState<Destination|null>();const [form]=Form.useForm();const kind=Form.useWatch('kind',form);const [busy,setBusy]=useState(false);const [error,setError]=useState('');const [success,setSuccess]=useState('');
 const destinations=useQuery({queryKey:['r11-backup-destinations'],queryFn:()=>requests.get<Destination[]>('/admin/backup/destinations',{errorMode:'local'})});
 const copies=useQuery({queryKey:['r11-backup-copies'],queryFn:()=>requests.get<Copy[]>('/admin/backup/copies',{errorMode:'local'}),refetchInterval:5000});
 const edit=(d:Destination|null)=>{form.resetFields();form.setFieldsValue(d??{kind:'s3',enabled:false,prefix:'next-terminal',region:'us-east-1',allowHttp:false});setError('');setSuccess('');setEditing(d)};
 const run=async(fn:()=>Promise<unknown>)=>{setBusy(true);setError('');setSuccess('');try{await fn();await cache.invalidateQueries({predicate:q=>String(q.queryKey[0]).startsWith('r11-backup')})}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const save=async()=>{const v=await form.validateFields();await run(async()=>{await requests.post('/admin/backup/destinations',{...v,id:editing?.id??'',revision:editing?.revision??0},{errorMode:'local'});setEditing(undefined)})};
 return <><p>为每个启用的目标复制加密归档，重新读取并核对 SHA-256 后才计为成功。复制失败时保留本地备份。</p>
 {(error||destinations.error||copies.error)&&<Alert type="error" title={error||destinations.error?.message||copies.error?.message}/>}{success&&<Alert type="success" title={success}/>}
 <Button disabled={!canEdit||busy} onClick={()=>edit(null)}>添加远端目标</Button>
 <Table<Destination> rowKey="id" size="small" dataSource={destinations.data} pagination={false} scroll={{x:700}} columns={[{title:'名称',dataIndex:'name'},{title:'协议',dataIndex:'kind',render:v=>String(v).toUpperCase()},{title:'地址',dataIndex:'endpoint',ellipsis:true},{title:'状态',render:(_,d)=><Tag>{d.enabled?'启用':'停用'}</Tag>},{title:'操作',render:(_,d)=><><Button type="link" disabled={!canEdit||busy} onClick={()=>edit(d)}>编辑</Button><Button type="link" disabled={busy} onClick={()=>run(async()=>{await requests.post(`/admin/backup/destinations/${d.id}/test`,{},{errorMode:'local'});setSuccess(`${d.name}：读写、SHA-256 和测试文件清理校验通过`)})}>测试连接</Button></>}]}/>
 <p style={{marginTop:24,fontWeight:600}}>复制记录</p>
 <Table<Copy> rowKey={v=>`${v.taskId}:${v.destinationId}`} size="small" dataSource={copies.data} pagination={{pageSize:5}} scroll={{x:700}} columns={[{title:'备份编号',dataIndex:'taskId',ellipsis:true},{title:'目标',dataIndex:'name'},{title:'状态',render:(_,v)=><Tag color={v.state==='verified'?'success':v.state==='failed'?'error':undefined}>{({pending:'待复制',copying:'复制与校验中',verified:'已校验',failed:'失败，原文件保留',deleting:'保留期清理中',deleted:'已到期清理'} as Record<string,string>)[v.state]??v.state}</Tag>},{title:'远端文件',dataIndex:'remoteKey',ellipsis:true}]}/>
 <Modal title={editing?'编辑远端目标':'添加远端目标'} open={editing!==undefined} onCancel={()=>setEditing(undefined)} confirmLoading={busy} onOk={save}>{error&&<Alert type="error" title={error}/>}<Form form={form} layout="vertical"><div className="nt-form-grid">
 <Form.Item name="name" label="名称" rules={[{required:true}]}><Input maxLength={120}/></Form.Item><Form.Item name="kind" label="协议" rules={[{required:true}]}><Select options={['s3','sftp','webdav'].map(value=>({value,label:value.toUpperCase()}))}/></Form.Item>
 <Form.Item name="endpoint" label={kind==='sftp'?'SFTP 主机与端口':'服务地址'} rules={[{required:true}]}><Input placeholder={kind==='sftp'?'backup.internal:22':'https://backup.internal'}/></Form.Item><Form.Item name="prefix" label={kind==='sftp'?'远端目录':'对象前缀 / WebDAV 集合'}><Input/></Form.Item>
 {kind==='s3'&&<><Form.Item name="bucket" label="存储桶" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="region" label="区域"><Input placeholder="us-east-1"/></Form.Item></>}
 <Form.Item name="username" label={kind==='s3'?'Access Key':'用户名'} rules={[{required:kind!=='webdav'}]}><Input autoComplete="off"/></Form.Item><Form.Item name="secret" label={kind==='s3'?'Secret Key（留空保留）':kind==='sftp'?'密码或 SSH 私钥（留空保留）':'访问密码（留空保留）'} rules={[{required:!editing}]}>{kind==='sftp'?<Input.TextArea rows={3} autoComplete="off"/>:<Input.Password autoComplete="new-password"/>}</Form.Item>
 {kind==='sftp'?<Form.Item name="hostKey" label="SSH 主机指纹" rules={[{required:true}]}><Input placeholder="SHA256:…"/></Form.Item>:<><Form.Item name="ca" label="自签证书的 CA（可选）"><Input.TextArea rows={3}/></Form.Item><Form.Item name="allowHttp" label="允许 HTTP（仅隔离内网测试）" valuePropName="checked"><Switch/></Form.Item></>}
 <Form.Item name="enabled" label="启用自动复制" valuePropName="checked"><Switch/></Form.Item>
 </div><p>SFTP 目标需支持 OpenSSH hardlink 扩展；WebDAV 集合和 S3 存储桶需提前建立。修改地址或用户名时请重新填写访问凭据。</p>
 <Form.Item name="password" label="当前账号密码（验证配置变更）" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="totp" label="动态验证码（已绑定 MFA 时必填）"><Input maxLength={6} autoComplete="one-time-code"/></Form.Item>
 </Form></Modal></>
}
