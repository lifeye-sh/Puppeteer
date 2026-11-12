import {Alert,Button,Form,Input,Table,Tag,Space,Switch,Select} from 'antd';
import {useState} from 'react';
import {Link} from 'react-router-dom';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import Modal,{FormTabs} from './BackendFormModal';
type Provider={settingsManaged?:boolean;scopes?:string[];kind:string;agentId:string;authorizationUrl:string;id:string;name:string;issuer:string;clientId:string;redirectUri:string;hasSecret:boolean;enabled:boolean;bindingEnabled:boolean;version:number};
export default function BackendOIDCProviders(){
 const [testing,setTesting]=useState<Provider>();
 const cache=useQueryClient();const [form]=Form.useForm();const [open,setOpen]=useState(false),[editing,setEditing]=useState<Provider>(),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const [checking,setChecking]=useState<string>(),[checkResult,setCheckResult]=useState<{ok:boolean;text:string}>();
 const check=async(v:Provider)=>{setChecking(v.id);setCheckResult(undefined);try{const result=await requests.post<{message:string}>('/admin/oidc-providers/'+v.id+'/test',{},{errorMode:'local'});setCheckResult({ok:true,text:v.name+'：'+result.message})}catch(e){setCheckResult({ok:false,text:v.name+'：'+(e as Error).message})}finally{setChecking(undefined)}};
 const query=useQuery({queryKey:['r13-oidc-providers'],queryFn:()=>requests.get<{items:Provider[]}>('/admin/oidc-providers',{errorMode:'local'})});
 const close=()=>{setTesting(undefined);setOpen(false);setEditing(undefined);setError('');form.resetFields()};
 const edit=(v?:Provider)=>{close();setEditing(v);form.setFieldsValue(v??{kind:'oidc',scopes:['openid'],redirectUri:location.origin+'/api/oidc/callback'});setOpen(true)};
 const save=async()=>{setError('');try{const v=await form.validateFields();setBusy(true);if(testing){const result=await requests.post<{authorizationUrl:string}>('/admin/oidc-providers/'+testing.id+'/authorize-test',v,{errorMode:'local'});window.location.assign(result.authorizationUrl);return}const path='/admin/oidc-providers'+(editing?'/'+editing.id:'');const body={...v,version:editing?.version??0};if(editing)await requests.put(path,body,{errorMode:'local'});else await requests.post(path,body,{errorMode:'local'});await cache.invalidateQueries({queryKey:['r13-oidc-providers']});close()}catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}};
 return <><Alert type="info" showIcon title="外部身份提供方（OIDC / 企业微信）" description="可分别开放账号绑定和联合登录。只有已绑定的平台账号可以使用联合登录，账号权限和 MFA 仍按平台策略执行。"/>
 {checkResult&&<Alert closable onClose={()=>setCheckResult(undefined)} type={checkResult.ok?'success':'error'} title={checkResult.text}/>}
 {query.error&&<Alert type="error" title={query.error.message}/>}
 <Button type="primary" style={{margin:'12px 0'}} onClick={()=>edit()}>新建提供方</Button>
 <Table<Provider> rowKey="id" size="small" loading={query.isLoading} dataSource={query.data?.items??[]} pagination={{pageSize:10}} scroll={{x:800}} columns={[{title:'名称',dataIndex:'name'},{title:'类型',dataIndex:'kind'},{title:'Issuer',dataIndex:'issuer'},{title:'客户端 ID',dataIndex:'clientId'},{title:'客户端密钥',render:(_,v)=>v.hasSecret?'已配置':'未配置'},{title:'状态',render:(_,v)=><Tag>{(v.bindingEnabled?'允许绑定':'禁止新绑定')+' · '+(v.enabled?'登录已启用':'登录未启用')}</Tag>},{title:'操作',render:(_,v)=><Space>{v.settingsManaged?<Link to={`/system-status?section=${v.kind==='wecom'?'wecomProvider':'oidcProvider'}`}>平台设置</Link>:<Button type="link" onClick={()=>edit(v)}>编辑</Button>}<Button type="link" loading={checking===v.id} disabled={!!checking} onClick={()=>void check(v)}>检测配置</Button><Button type="link" onClick={()=>{close();setTesting(v);setOpen(true)}}>验证授权</Button></Space>}]}/>
 <Modal open={open} title={testing?'验证 '+testing.name:editing?'编辑身份提供方':'新建身份提供方'} onCancel={close} onOk={()=>void save()} confirmLoading={busy}>
 {error&&<Alert type="error" title={error}/>}
 <Form name="oidc-provider" form={form} layout="vertical" preserve={false}>
 {testing?<Alert type="info" showIcon title="验证外部授权" description="复核平台身份后跳转到提供方，完成授权码及令牌验证。验证不会绑定外部账号或启用联合登录。OIDC 回调路径为 /api/oidc/callback，企业微信为 /api/wechat-work/callback。"/>:<FormTabs sections={[{key:'connection',label:'连接配置',description:'使用身份提供方的精确 Issuer；创建后不可直接更换身份域。',children:<>
 <Form.Item name="kind" label="协议类型" rules={[{required:true}]}><Select disabled={!!editing} options={[{value:'oidc',label:'OIDC'},{value:'wecom',label:'企业微信自建应用'}]} onChange={kind=>{form.setFieldsValue(kind==='wecom'?{issuer:'https://qyapi.weixin.qq.com',authorizationUrl:'https://login.work.weixin.qq.com/wwlogin/sso/login',redirectUri:location.origin+'/api/wechat-work/callback'}:{issuer:'',authorizationUrl:'',redirectUri:location.origin+'/api/oidc/callback'})}}/></Form.Item>
 <Form.Item name="name" label="名称" rules={[{required:true}]}><Input maxLength={100}/></Form.Item>
 <Form.Item name="clientId" label="客户端 ID / 企业 Corp ID" rules={[{required:true}]}><Input maxLength={512}/></Form.Item>
 <Form.Item name="issuer" label="Issuer / 企业微信 API 地址" rules={[{required:true,type:'url'},{pattern:/^https:\/\//,message:'必须使用 HTTPS'}]}><Input disabled={!!editing} maxLength={2048} placeholder="https://idp.example.com/tenant"/></Form.Item>
 <Form.Item noStyle shouldUpdate={(a,b)=>a.kind!==b.kind}>{({getFieldValue})=>getFieldValue('kind')==='wecom'?<><Form.Item name="agentId" label="应用 Agent ID" rules={[{required:true},{pattern:/^[0-9]+$/}]}><Input maxLength={32}/></Form.Item><Form.Item name="authorizationUrl" label="企业微信授权地址" rules={[{required:true,type:'url'}]}><Input maxLength={2048}/></Form.Item></>:null}</Form.Item>
 <Form.Item noStyle shouldUpdate={(a,b)=>a.kind!==b.kind}>{({getFieldValue})=>getFieldValue('kind')!=='wecom'?<Form.Item name="scopes" label="权限范围" extra="必须包含 openid；只申请提供方允许的范围，最多32项。" rules={[{required:true}]}><Select mode="tags" tokenSeparators={[' ']} options={[{value:'openid'},{value:'profile'},{value:'email'}]}/></Form.Item>:null}</Form.Item>
 <Form.Item name="redirectUri" label="回调地址" rules={[{required:true,type:'url'},{pattern:/^https:\/\//,message:'必须使用 HTTPS'}]}><Input maxLength={2048} placeholder="填写计划登记到提供方的精确回调地址"/></Form.Item>
 </>},{key:'security',label:'密钥与验证',description:'密钥加密保存且不回显。保存需要复核当前管理员身份。',children:<>
 <Form.Item name="enabled" label="启用联合登录" valuePropName="checked" extra="停用或修改配置会使现有联合登录会话失效。"><Switch/></Form.Item>
 <Form.Item name="bindingEnabled" label="允许用户绑定" valuePropName="checked" extra="开放后用户可在个人中心自行验证并绑定外部身份；开启登录后，已绑定用户可以使用此提供方登录。"><Switch/></Form.Item>
 <Form.Item name="clientSecret" label="客户端密钥" extra={editing?'留空保留已保存密钥；输入新值将替换。':undefined} rules={[{required:!editing}]}><Input.Password autoComplete="new-password" maxLength={8192}/></Form.Item>
 <Form.Item name="password" label="当前平台登录密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
 <Form.Item name="totp" label="动态口令（已启用 MFA 时必填）"><Input maxLength={6} autoComplete="one-time-code"/></Form.Item>
 </>}]} />}
 {testing&&<><Form.Item name="password" label="当前平台登录密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="totp" label="动态口令（已启用 MFA 时必填）"><Input maxLength={6} autoComplete="one-time-code"/></Form.Item></>}
 </Form></Modal></>;
}
