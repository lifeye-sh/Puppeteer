import {useState} from 'react';
import {Alert,Button,Card,Form,Input,Radio,Tabs,Tag,Typography} from 'antd';
import {useQueryClient} from '@tanstack/react-query';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import {useConfiguration} from './useConfiguration';
import type {Configuration} from './configuration-model';
import Modal from './BackendFormModal';

const keys=[
 {key:'oidc-server-signing-key',label:'ID Token 签名私钥',hint:'导入未加密的 RSA PEM 私钥（PKCS#1 或 PKCS#8，2048–4096 位）；平台自动生成使用 3072 位 RSA。',limit:8192},
 {key:'oidc-server-hmac-secret',label:'OAuth HMAC 密钥',hint:'导入 32 字节随机密钥的 Base64URL 编码（43 个字符，不带 =）；不得复用平台加密密钥。建议选择平台自动生成。',limit:43},
];
export default function BackendOAuthKeys(){
 const {account}=useBackendRuntime(),current=useConfiguration(true),cache=useQueryClient();
 const [form]=Form.useForm();const [editing,setEditing]=useState<Configuration>(),[tab,setTab]=useState(keys[0].key),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('');
 const mode=Form.useWatch('mode',form),selected=keys.find(k=>k.key===tab)!;
 const open=()=>{if(!current.data)return;form.resetFields();form.setFieldsValue({mode:'generate'});setEditing(current.data);setError('');setNotice('')};
 const close=()=>{if(busy)return;form.resetFields();setEditing(undefined);setError('')};
 const save=async()=>{
  if(!editing)return;setBusy(true);setError('');
  try{
   const v=await form.validateFields();
   const response=await fetch('/api/settings',{method:'PATCH',headers:{'Content-Type':'application/json','If-Match':`"settings-${editing.id}"`},body:JSON.stringify({set:v.mode==='import'?{[tab]:v.secret}:{},clearSecrets:v.mode==='generate'?[tab]:[],password:v.password,totp:v.totp})});
   if(!response.ok){const body=await response.json();throw Error(body.message??'保存失败，请刷新配置后重试。')}
   form.resetFields();setEditing(undefined);setNotice(`${selected.label}已更新，新签发立即使用新密钥。`);
   await Promise.all(['configuration-current','configuration-history','backend-settings','oauth-server'].map(key=>cache.invalidateQueries({queryKey:[key]})));
  }catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}
 };
 return <div className="nt-settings-stack">
  <Alert showIcon type="info" title="密钥独立加密保存，不提供明文回读" description="轮换保留已签发授权：旧签名公钥保留 61 分钟；旧 HMAC 密钥用于验证尚未过期的授权码及令牌。此操作不会开启 OIDC 或原生客户端入口。"/>
  {current.error&&<Alert type="error" title={current.error.message}/>}
  {notice&&<Alert type="success" showIcon title={notice}/>}
  <Card title="OAuth 密钥" loading={current.isPending}>
   <Tabs activeKey={tab} onChange={setTab} items={keys.map(k=>({key:k.key,label:k.label,children:<>
    <Typography.Paragraph>{k.hint}</Typography.Paragraph>
    <Typography.Paragraph>当前来源：<Tag>{current.data?.secrets?.[k.key]?.hasValue?'已导入自定义密钥':'平台自动管理'}</Tag></Typography.Paragraph>
    {account.type==='admin'&&account.actions?.includes('identity.manage')&&<Button onClick={open} disabled={!current.data}>更换{k.label}</Button>}
   </>}))}/>
  </Card>
  <Modal width={700} open={!!editing} title={`更换${selected.label}`} onCancel={close} onOk={()=>void save()} confirmLoading={busy} mask={{closable:false}} keyboard={!busy} cancelButtonProps={{disabled:busy}} forceRender>
   {error&&<Alert type="error" showIcon title={error}/>}
   <Typography.Paragraph type="secondary">基于配置版本 {editing?.id}，仅更换当前密钥。每次自动生成都会创建新密钥。</Typography.Paragraph>
   <Form form={form} layout="vertical" preserve={false}>
    <Form.Item name="mode" label="密钥来源"><Radio.Group options={[{label:'平台生成新密钥',value:'generate'},{label:'导入自定义密钥',value:'import'}]}/></Form.Item>
    {mode==='import'&&<Form.Item name="secret" label={selected.label} extra={selected.hint} rules={[{required:true,max:selected.limit}]}>{tab===keys[0].key?<Input.TextArea autoComplete="off" spellCheck={false} rows={7} maxLength={selected.limit}/>:<Input.Password autoComplete="new-password" maxLength={selected.limit}/>}</Form.Item>}
    <Form.Item name="password" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item>
    <Form.Item name="totp" label="新的动态口令" extra="已绑定 MFA 时需输入未消费的新口令。" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
   </Form>
  </Modal>
 </div>;
}
