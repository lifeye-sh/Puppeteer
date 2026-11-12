import {Alert,Button,Form,Input,Table,Tag} from 'antd';
import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {startRegistration} from '@simplewebauthn/browser';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';
type Key={id:string;name:string;createdAt:string;lastUsedAt?:string;revokedAt?:string};
export default function BackendPasskeys(){
 const cache=useQueryClient();const [form]=Form.useForm();const [open,setOpen]=useState(false),[selected,setSelected]=useState<Key>(),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const query=useQuery({queryKey:['r13-passkeys'],queryFn:()=>requests.get<{items:Key[]}>('/account/passkeys',{errorMode:'local'})});
 const mfa=useQuery({queryKey:['r02-mfa'],queryFn:()=>requests.get<{enabled:boolean}>('/account/mfa',{errorMode:'local'})});
 const close=()=>{setOpen(false);setSelected(undefined);setError('');form.resetFields()};
 const submit=async()=>{setError('');let stage='proof';try{const v=await form.validateFields();setBusy(true);
  if(selected){const r=await fetch('/api/account/passkeys/'+selected.id,{method:'DELETE',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:v.password,totp:v.totp})});if(!r.ok)throw new Error((await r.json()).message??'撤销失败')}
  else{if(!window.isSecureContext)throw new Error('Passkey 需要可信 HTTPS 页面');const start=await requests.post<{token:string;options:Parameters<typeof startRegistration>[0]['optionsJSON']}>('/account/passkeys/start',{password:v.password,totp:v.totp},{errorMode:'local'});stage='device';const response=await startRegistration({optionsJSON:start.options});stage='finish';await requests.post('/account/passkeys/finish',{token:start.token,name:v.name,response},{errorMode:'local'})}
  await cache.invalidateQueries({queryKey:['r13-passkeys']});close();
 }catch(e){if(e instanceof Error){if(stage==='proof'&&e.message==='验证信息错误、过期或已使用，请重试'){setError(mfa.data?.enabled?'当前登录密码或动态口令校验失败。请重新输入密码，并使用验证器中新生成的口令。':'当前登录密码校验失败。请填写本平台的登录密码，不是 Windows PIN 或新建的 Passkey 密码。');form.resetFields(['password','totp'])}else setError(e.message)}}finally{setBusy(false)}};
 return <><Alert showIcon type="info" title="使用设备验证器或安全密钥注册 Passkey" description="注册需要复核当前密码及已启用的动态口令。登录仍遵守账户 MFA 策略；撤销立即阻止此凭据继续登录。"/>
 {query.error&&<Alert type="error" title={query.error.message}/>}
 <Button style={{margin:'12px 0'}} type="primary" onClick={()=>{close();setOpen(true)}}>新增 Passkey</Button>
 <Table<Key> rowKey="id" size="small" dataSource={query.data?.items??[]} loading={query.isLoading} pagination={{pageSize:5}} columns={[{title:'名称',dataIndex:'name'},{title:'创建时间',dataIndex:'createdAt',render:v=>new Date(v).toLocaleString()},{title:'最近使用',dataIndex:'lastUsedAt',render:v=>v?new Date(v).toLocaleString():'未使用'},{title:'状态',render:(_,v)=><Tag>{v.revokedAt?'已撤销':'有效'}</Tag>},{title:'操作',render:(_,v)=><Button type="link" danger disabled={!!v.revokedAt} onClick={()=>{close();setSelected(v);setOpen(true)}}>撤销</Button>}]}/>
 <Modal open={open} title={selected?'撤销 Passkey':'注册 Passkey'} onCancel={close} onOk={()=>void submit()} confirmLoading={busy}>
 {error&&<Alert type="error" title={error}/>}
 <Form name="passkey-form" form={form} layout="vertical" preserve={false}>
 {!selected&&<Form.Item name="name" label="名称" rules={[{required:true}]}><Input maxLength={100} placeholder="例如：工作电脑"/></Form.Item>}
 <Form.Item name="password" label="当前平台登录密码" extra="填写登录 Puppeteer 的密码。Windows PIN 或指纹会在后续系统弹窗中确认。" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
 {mfa.data?.enabled&&<Form.Item name="totp" label="验证器动态口令" rules={[{required:true,pattern:/^\d{6}$/,message:'请输入新生成的 6 位动态口令'}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>}
 </Form></Modal></>;
}
