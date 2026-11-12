import BackendNativeDevices from './BackendNativeDevices';
import BackendNativeSSHGateway from './BackendNativeSSHGateway';
import BackendContacts from './BackendContacts';
import BackendOAuthConsents from './BackendOAuthConsents';
import BackendLDAPBindings from './BackendLDAPBindings';
import BackendOIDCBindings from './BackendOIDCBindings';
import BackendPasskeys from './BackendPasskeys';
import {Alert,Button,Checkbox,Divider,Form,Input,InputNumber,Table,Tabs,Tag,Typography} from 'antd';
import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';
import BackendPersonalCertificates from './BackendPersonalCertificates';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
type Credential={id:string;name:string;prefix?:string;scopes?:string[];fingerprint?:string;algorithm?:string;expiresAt:string;revokedAt?:string;lastUsedAt?:string};
export default function BackendPersonalCredentials(){const {capabilities}=useBackendRuntime();const status=useQuery({queryKey:['r13-passkey-status'],queryFn:()=>requests.get<{webauthnEnabled:boolean}>('/login-status',{errorMode:'local'})});return <><Divider/><Typography.Title level={4}>个人访问凭据</Typography.Title><Tabs items={[...(capabilities.features['native.devices']?.state==='available'?[{key:'native-devices',label:'登录设备',children:<BackendNativeDevices/>}]:[]),...(capabilities.features['identity.oidc-server']?.state==='available'?[{key:'oauth-consents',label:'已授权应用',children:<BackendOAuthConsents/>}]:[]),{key:'oidc-bindings',label:'OIDC / 企业微信',children:<BackendOIDCBindings/>},...(capabilities.features['identity.ldap-login']?.state==='available'?[{key:'ldap',label:'LDAP 账号',children:<BackendLDAPBindings/>}]:[]),...(status.data?.webauthnEnabled?[{key:'passkeys',label:'Passkey',children:<BackendPasskeys/>}]:[]),...(capabilities.features['identity.contacts']?.state==='available'?[{key:'contacts',label:'邮箱 / 手机',children:<BackendContacts/>}]:[]),{key:'api',label:'API Key',children:<Credentials kind="api"/>},{key:'ssh',label:'SSH 公钥',children:<Credentials kind="ssh"/>},...(capabilities.features['identity.client-certificates']?.state==='available'?[{key:'certificates',label:'客户端证书',children:<BackendPersonalCertificates/>}]:[])]}/></>}
function Credentials({kind}:{kind:'api'|'ssh'}){
 const cache=useQueryClient();const [open,setOpen]=useState(false),[revoking,setRevoking]=useState<Credential>(),[secret,setSecret]=useState(''),[error,setError]=useState(''),[busy,setBusy]=useState(false);const [form]=Form.useForm();
 const path=`/account/${kind}-keys`;
 const query=useQuery({queryKey:['r13-credentials',kind],queryFn:()=>requests.get<{items:Credential[]}>(path,{errorMode:'local'})});
 const close=()=>{setOpen(false);setRevoking(undefined);setSecret('');setError('');form.resetFields()};
 const submit=async()=>{setError('');setBusy(true);try{const v=await form.validateFields();const response=await fetch('/api'+path+(revoking?`/${revoking.id}`:''),{method:revoking?'DELETE':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(v)});if(!response.ok){const b=await response.json();throw Error(b.message??'操作失败')};await cache.invalidateQueries({queryKey:['r13-credentials',kind]});form.resetFields(['password','totp']);if(!revoking&&kind==='api')setSecret((await response.json()).secret);else close()}catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}};
 return <>
 {kind==='ssh'&&<BackendNativeSSHGateway/>}
 <Alert type="info" showIcon title={kind==='api'?'API Key 按范围访问指定的只读接口；不支持网页登录或创建会话。':'保存用于本人身份认证的 SSH 公钥；连接仍受当前资产授权和访问策略限制。'} description={kind==='api'?'profile.read：GET /api/account/info；database.read：GET /api/portal/database-assets。使用 Authorization: Bearer <API Key>，资源权限仍由当前账户授权决定。':'支持 Ed25519、ECDSA 和至少 3072 位 RSA。请粘贴单个 OpenSSH 公钥，不要填写私钥或 authorized_keys 选项。'}/>
 {query.error&&<Alert type="error" title={query.error.message}/>}
 <Button style={{margin:'12px 0'}} type="primary" onClick={()=>{form.resetFields();setOpen(true)}}>新增{kind==='api'?' API Key':' SSH 公钥'}</Button>
 <Table<Credential> rowKey="id" dataSource={query.data?.items??[]} loading={query.isLoading} size="small" pagination={{pageSize:5}} scroll={{x:680}} columns={[
 {title:'名称',dataIndex:'name'},{title:kind==='api'?'前缀 / 范围':'算法 / 指纹',render:(_,v)=><><code>{v.prefix??v.fingerprint}</code><div>{kind==='api'?v.scopes?.join('、'):v.algorithm}</div></>},
 {title:'有效期至',dataIndex:'expiresAt',render:v=>new Date(v).toLocaleString()},{title:'状态',render:(_,v)=><Tag color={v.revokedAt?'default':Date.parse(v.expiresAt)<=Date.now()?'orange':'green'}>{v.revokedAt?'已撤销':Date.parse(v.expiresAt)<=Date.now()?'已过期':'有效'}</Tag>},
 {title:'操作',render:(_,v)=><Button type="link" danger disabled={!!v.revokedAt} onClick={()=>{form.resetFields();setRevoking(v);setOpen(true)}}>撤销</Button>}
 ]}/>
 <Modal open={open} title={revoking?`撤销 ${revoking.name}`:`新增${kind==='api'?' API Key':' SSH 公钥'}`} onCancel={close} onOk={()=>void submit()} confirmLoading={busy} okText={revoking?'确认撤销':'创建'} footer={secret?<Button onClick={close}>已保存，关闭</Button>:undefined}>
 {error&&<Alert type="error" title={error}/>}
 {secret?<><Alert type="warning" showIcon title="密钥仅显示一次，请保存到安全位置。关闭后无法再次查看。"/><Typography.Paragraph copyable style={{overflowWrap:'anywhere',marginTop:16}}>{secret}</Typography.Paragraph></>:<Form form={form} layout="vertical" initialValues={{days:90,scopes:['profile.read']}} preserve={false}>
 {!revoking&&<><Form.Item name="name" label="名称" rules={[{required:true}]}><Input maxLength={100}/></Form.Item><Form.Item name="days" label="有效期（天）" rules={[{required:true}]}><InputNumber min={1} max={365}/></Form.Item>{kind==='api'?<Form.Item name="scopes" label="访问范围" rules={[{required:true}]}><Checkbox.Group options={[{label:'读取本人资料',value:'profile.read'},{label:'读取已授权数据库',value:'database.read'}]}/></Form.Item>:<Form.Item name="publicKey" label="SSH 公钥" rules={[{required:true}]}><Input.TextArea rows={4} maxLength={16384} placeholder="ssh-ed25519 AAAA..."/></Form.Item>}</>}
 {revoking&&<Alert type="warning" title="撤销立即生效，无法恢复。"/>}
 <Form.Item name="password" label="当前登录密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="totp" label="动态口令（已启用双因素认证时必填）"><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
 </Form>}
 </Modal>
 </>
}
