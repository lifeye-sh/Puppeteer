import {useState} from 'react';
import {Alert,Button,Card,Checkbox,Form,Input,Space,Switch,Select,Tabs,Typography} from 'antd';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Link} from 'react-router-dom';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal from './BackendFormModal';
import BackendFederationUser from './BackendFederationUser';
import requests from '@/api/core/requests';
import {useConfiguration,useConfigurationSchema} from './useConfiguration';
import {displayConfiguration,type Configuration,type Definition} from './configuration-model';

export default function BackendFederationConfiguration({kind}:{kind:'oidc'|'wecom'}){
 const {account}=useBackendRuntime();const cache=useQueryClient();const [form]=Form.useForm();
 const current=useConfiguration(true),schema=useConfigurationSchema(true);
 const [editing,setEditing]=useState<Configuration>(),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState(''),[tab,setTab]=useState('connection');
 const prefix=kind==='oidc'?'oidc-':'wechat-work-',secretKey=kind==='oidc'?'oidc-client-secret':'wechat-work-secret';
 const departmentKey=kind==='oidc'?'oidc-department':'wechat-work-department';
 const departments=useQuery({queryKey:['federation-default-departments'],queryFn:()=>requests.get<{id:string;name:string}[]>('/admin/departments',{errorMode:'local'})});
 const title=kind==='oidc'?'平台默认 OIDC':'平台默认企业微信';
 const definitions=(schema.data?.properties??[]).filter(p=>p.key.startsWith(prefix)&&!p.key.startsWith('oidc-server-'));
 const clear=Form.useWatch('_clearSecret',form),created=!!editing?.values[kind==='oidc'?'oidc-client-id':'wechat-work-corp-id'];
 const canWrite=account.type==='admin'&&account.actions?.includes('identity.manage');
 const open=()=>{if(!current.data)return;form.resetFields();form.setFieldsValue(Object.fromEntries(definitions.map(p=>[p.key,current.data!.values[p.key]])));setEditing(current.data);setTab('connection');setError('');setNotice('')};
 const close=()=>{if(busy)return;setEditing(undefined);form.resetFields()};
 const save=async()=>{
  if(!editing)return;setBusy(true);setError('');
  try{
   const values=await form.validateFields();const set=Object.fromEntries(definitions.map(p=>[p.key,values[p.key]??(p.key===departmentKey?'':undefined)]));
   if(clear)delete set[secretKey];
   const response=await fetch('/api/settings',{method:'PATCH',headers:{'Content-Type':'application/json','If-Match':`"settings-${editing.id}"`},body:JSON.stringify({set,clearSecrets:clear?[secretKey]:[],password:values._adminPassword,totp:values._adminTOTP})});
   if(!response.ok){const body=await response.json();throw Error(body.message??'保存失败，请刷新配置后重试。')}
   setEditing(undefined);form.resetFields();setNotice('已保存；开启登录或绑定需等待配置节点确认。');
   await Promise.all(['configuration-current','configuration-history','backend-settings','r13-oidc-providers','r13-oidc-bindings'].map(key=>cache.invalidateQueries({queryKey:[key]})));
  }catch(e){if(e instanceof Error)setError(e.message);else if(e&&typeof e==='object'&&'errorFields'in e){const name=String((e as {errorFields:Array<{name:Array<string|number>}>}).errorFields[0]?.name[0]??'');setTab(name.startsWith('_')||name===secretKey?'security':'connection')}}finally{setBusy(false)}
 };
 const field=(p:Definition)=>{
  const immutable=created&&(p.key==='oidc-issuer'||p.key==='wechat-work-api-url'||p.key==='wechat-work-corp-id');
  const hint=p.key===departmentKey?'仅用于管理员明确预建新用户；不修改已有用户、不自动注册或绑定，不赋予管理角色。':p.key===secretKey?'留空或掩码保持；更换客户端 ID 时需重新填写密钥。':p.key==='oidc-scopes'?'空格分隔，必须包含 openid，最多32项。仅申请提供方允许的范围；不会以邮箱合并账号。':p.key.endsWith('redirect-uri')?`当前平台回调：${location.origin}/api/${kind==='oidc'?'oidc':'wechat-work'}/callback` :immutable?'身份域创建后不可修改；更换身份域请新建独立提供方。':undefined;
  return <Form.Item key={p.key} name={p.key} label={p.label} extra={hint} valuePropName={p.type==='boolean'?'checked':'value'} className={p.key===secretKey||p.key.endsWith('redirect-uri')?'nt-settings-field-full':undefined} rules={p.type==='string'&&p.min>0?[{required:true,max:p.max}]:undefined}>
   {p.key===departmentKey?<Select allowClear showSearch optionFilterProp="label" placeholder="不配置默认部门" options={(departments.data??[]).map(d=>({value:d.id,label:d.name}))}/>:p.type==='boolean'?<Switch/>:p.type==='secret'?<Input.Password autoComplete="new-password" maxLength={p.max} disabled={!!clear}/>:<Input maxLength={p.max} disabled={immutable}/>}
  </Form.Item>;
 };
 const connection=definitions.filter(p=>p.type!=='boolean'&&p.type!=='secret'),security=definitions.filter(p=>p.type==='boolean'||p.type==='secret');
 return <div className="nt-settings-stack">
  <Alert showIcon type="info" title={title} description="分别控制账号绑定与登录。只有已绑定的平台账号可以登录，平台权限、账号状态与 MFA 仍生效。已有独立提供方不受此配置影响。"/>
  {departments.error&&<Alert type="error" title={departments.error.message}/>}
  {(current.error||schema.error)&&<Alert type="error" title={current.error?.message??schema.error?.message}/>}
  {notice&&<Alert type="success" showIcon title={notice}/>}
  <Card title={title} loading={current.isPending||schema.isPending} extra={canWrite&&<Button onClick={open} disabled={!current.data||!schema.data}>编辑配置</Button>}>
   <div className="nt-settings-values">{definitions.map(p=><div className="nt-settings-value" key={p.key}><span>{p.label}</span><strong>{p.key===departmentKey?(departments.data?.find(d=>d.id===current.data?.values[p.key])?.name||'未配置'):p.type==='secret'?current.data?.secrets?.[p.key]?.hasValue?'已加密保存':'未配置':displayConfiguration(current.data?.values[p.key],p)}</strong></div>)}</div>
   <Typography.Paragraph type="secondary">每次变更需管理员密码与已绑定 MFA 复核。关闭立即生效；开启等待在线节点确认。修改提供方会使其现有联合登录会话和待完成授权失效，不能移除最后一个管理员登录途径。</Typography.Paragraph>
   {canWrite&&current.data?.values[departmentKey]!==undefined&&<BackendFederationUser kind={kind} departmentId={String(current.data?.values[departmentKey]??'')} departmentName={departments.data?.find(d=>d.id===current.data?.values[departmentKey])?.name??''}/>}
   <Space><Link to="/identity-settings">前往身份提供方检测与验证授权</Link><Link to="/info">前往个人中心绑定账号</Link></Space>
  </Card>
  <Modal className="nt-settings-editor" width={760} open={!!editing} title={`编辑${title}`} onCancel={close} onOk={()=>void save()} confirmLoading={busy} mask={{closable:false}} keyboard={!busy} cancelButtonProps={{disabled:busy}} forceRender>
   {error&&<Alert type="error" showIcon title={error}/>}
   <Typography.Paragraph type="secondary">基于版本 {editing?.id}，仅保存当前身份源配置。</Typography.Paragraph>
   <Form form={form} name={`managed-${kind}`} layout="vertical" preserve={false}>
    <Tabs activeKey={tab} onChange={setTab} items={[
     {key:'connection',label:'连接与范围',forceRender:true,children:<div className="nt-mail-fields">{connection.map(field)}</div>},
     {key:'security',label:'登录与密钥',forceRender:true,children:<div className="nt-mail-fields">{security.map(field)}
      <Form.Item className="nt-settings-field-full" name="_clearSecret" valuePropName="checked"><Checkbox>清除已保存的客户端密钥（须关闭登录与绑定）</Checkbox></Form.Item>
      <Form.Item name="_adminPassword" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item>
      <Form.Item name="_adminTOTP" label="新的动态口令" extra="已绑定 MFA 时需输入未消费的新口令。" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
     </div>},
    ]}/>
   </Form>
  </Modal>
 </div>;
}
