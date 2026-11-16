import {Alert, Button, Collapse, Form, Input, Space, Table, Tag, Typography} from 'antd';
import {useQuery, useQueryClient} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';

type RecoveryKey = {id:string;recipient:string;createdAt:string;active:boolean};
type Action = {kind:'generate';revision:number} | {kind:'download';key:RecoveryKey};

export default function BackendBackupRecoveryKeys({revision, recipient, disabled, canExport}: {
 revision?:number;recipient?:string;disabled:boolean;canExport:boolean;
}) {
 const client = useQueryClient();
 const keys = useQuery({queryKey:['r11-backup-recovery-keys'],queryFn:()=>requests.get<RecoveryKey[]>('/admin/backup/recovery-keys',{errorMode:'local'})});
 const [action,setAction] = useState<Action>();
 const [busy,setBusy] = useState(false);
 const [error,setError] = useState('');
 const [notice,setNotice] = useState('');
 const [form] = Form.useForm();
 const current = keys.data?.find(key=>key.active && key.recipient===recipient);
 const open = (next:Action) => {form.resetFields();setError('');setAction(next)};
 const submit = async () => {
  if(!action)return;
  const proof = await form.validateFields();
  setBusy(true);setError('');
  try {
   if(action.kind==='generate') {
    await requests.post('/admin/backup/recovery-keys',{...proof,revision:action.revision},{errorMode:'local'});
    setNotice('新的恢复密钥已生成并生效，请下载私钥到独立位置保存。历史密钥仍可下载。');
   } else {
    const result = await requests.post<{privateKey:string;filename:string}>(`/admin/backup/recovery-keys/${action.key.id}/download`,proof,{errorMode:'local'});
    const url = URL.createObjectURL(new Blob([result.privateKey],{type:'text/plain;charset=utf-8'}));
    const anchor = document.createElement('a');anchor.href=url;anchor.download=result.filename;
    document.body.appendChild(anchor);anchor.click();anchor.remove();
    setTimeout(()=>URL.revokeObjectURL(url),1000);
    setNotice('已发起私钥下载，请确认文件保存成功，并与备份服务器分开保管。');
   }
   setAction(undefined);form.resetFields();
   await client.invalidateQueries({predicate:q=>String(q.queryKey[0]).startsWith('r11-backup')});
  } catch(e) {setError((e as Error).message)} finally {setBusy(false)}
 };
 return <div style={{marginTop:16}}>
  {keys.error&&<Alert type="error" title={keys.error.message} showIcon/>}
  {notice&&<Alert type="success" title={notice} closable onClose={()=>setNotice('')} showIcon/>}
  <Typography.Paragraph type="secondary">首次部署自动生成恢复密钥，私钥在平台加密保存。请下载到独立位置；服务器或数据丢失时，恢复备份需要该文件。</Typography.Paragraph>
  {keys.isSuccess&&recipient&&!current&&<Alert type="info" title="当前使用自有恢复公钥" description="平台没有对应私钥，请保留原私钥以恢复旧备份。重新生成后，新备份将使用平台生成的密钥。" showIcon/>}
  <Space wrap>
   <Button disabled={disabled||busy||!canExport||!current} onClick={()=>current&&open({kind:'download',key:current})}>下载恢复私钥</Button>
   <Button disabled={disabled||busy||!canExport||!revision||keys.isPending} onClick={()=>revision&&open({kind:'generate',revision})}>重新生成恢复密钥</Button>
  </Space>
  {!!keys.data?.length&&<Collapse style={{marginTop:12}} items={[{key:'history',label:'恢复密钥记录（含历史密钥）',children:<Table<RecoveryKey> size="small" rowKey="id" dataSource={keys.data} pagination={{pageSize:5}} scroll={{x:500}} columns={[
   {title:'生成时间',dataIndex:'createdAt',render:v=>new Date(v).toLocaleString()},
   {title:'状态',render:(_,key)=><Tag color={key.active?'green':'default'}>{key.active?'当前使用':'历史密钥'}</Tag>},
   {title:'公钥',dataIndex:'recipient',render:v=><Typography.Text copyable={{text:v}}>{v.slice(0,15)}…</Typography.Text>},
   {title:'操作',render:(_,key)=><Button type="link" disabled={disabled||busy||!canExport} onClick={()=>open({kind:'download',key})}>下载私钥</Button>}
  ]}/>}]}/>}
  <Modal title={action?.kind==='generate'?'重新生成恢复密钥':'下载恢复私钥'} open={!!action} confirmLoading={busy} okText={action?.kind==='generate'?'验证并重新生成':'验证并下载'} onCancel={()=>{if(!busy){setAction(undefined);form.resetFields()}}} onOk={submit}>
   {error&&<Alert type="error" title={error} showIcon/>}
   <Alert type="info" showIcon title={action?.kind==='generate'?'新密钥用于后续备份':'请将私钥与服务器分开保管'} description={action?.kind==='generate'?'重新生成立即生效。已有备份和已排队任务仍使用原密钥；平台会保留历史密钥。':'私钥可解密对应的备份，请保存到您控制的独立位置。'}/>
   <Form form={form} layout="vertical" style={{marginTop:16}}>
    <Form.Item name="password" label="当前账号密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
    <Form.Item name="totp" label="动态验证码（已绑定 MFA 时必填）"><Input maxLength={6} autoComplete="one-time-code"/></Form.Item>
   </Form>
  </Modal>
 </div>;
}
