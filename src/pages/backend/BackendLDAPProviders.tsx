import {Link} from 'react-router-dom';
import BackendLDAPSync from './BackendLDAPSync';
import {Alert,Button,Form,Input,InputNumber,Select,Switch,Space,Table,Tag} from 'antd';
import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import Modal,{FormTabs} from './BackendFormModal';
type Provider={settingsManaged?:boolean;enabled:boolean;bindingEnabled:boolean;id:string;name:string;url:string;baseDN:string;bindDN:string;searchFilter:string;loginAttribute:string;identityAttribute:string;caPEM:string;hasSecret:boolean;version:number;searchSizeLimit?:number;propertyMapping?:{username:string;nickname?:string;mail?:string}};
export default function BackendLDAPProviders(){
 const cache=useQueryClient();const [form]=Form.useForm();const [open,setOpen]=useState(false),[editing,setEditing]=useState<Provider>(),[testing,setTesting]=useState<Provider>(),[busy,setBusy]=useState(false),[error,setError]=useState(''),[result,setResult]=useState('');
 const query=useQuery({queryKey:['r13-ldap-providers'],queryFn:()=>requests.get<{items:Provider[]}>('/admin/ldap-providers',{errorMode:'local'})});
 const close=()=>{setOpen(false);setEditing(undefined);setTesting(undefined);setError('');form.resetFields()};
 const edit=(p?:Provider)=>{close();setEditing(p);form.setFieldsValue(p??{searchFilter:'(objectClass=inetOrgPerson)',loginAttribute:'uid',identityAttribute:'entryUUID',searchSizeLimit:1000,propertyMapping:{nickname:'cn',mail:'mail'}});setOpen(true)};
 const submit=async()=>{setError('');setResult('');try{const v=await form.validateFields();setBusy(true);if(testing){const r=await requests.post<{message:string;subject:string}>('/admin/ldap-providers/'+testing.id+'/test',v,{errorMode:'local'});setResult(testing.name+'：'+r.message+(r.subject?'；稳定身份：'+r.subject:''))}else{const path='/admin/ldap-providers'+(editing?'/'+editing.id:'');const body={...v,propertyMapping:{username:v.loginAttribute,nickname:v.propertyMapping?.nickname??'',mail:v.propertyMapping?.mail??''},version:editing?.version??0};if(editing)await requests.put(path,body,{errorMode:'local'});else await requests.post(path,body,{errorMode:'local'});await cache.invalidateQueries({queryKey:['r13-ldap-providers']})}close()}catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}};
 return <><Alert type="info" showIcon title="LDAP 目录验证" description="支持 LDAPS 和强制 StartTLS。可检测服务账号及用户密码；用户在个人中心绑定后可使用 LDAP 登录；不会按邮箱自动合并账号。"/>
 {result&&<Alert type="success" showIcon title={result} closable onClose={()=>setResult('')}/>}{query.error&&<Alert type="error" title={query.error.message}/>}
 <Button type="primary" style={{margin:'12px 0'}} onClick={()=>edit()}>新建目录</Button>
 <Table<Provider> rowKey="id" size="small" loading={query.isLoading} dataSource={query.data?.items??[]} pagination={{pageSize:10}} scroll={{x:900}} columns={[{title:'名称',dataIndex:'name'},{title:'目录地址',dataIndex:'url'},{title:'Base DN',dataIndex:'baseDN'},{title:'身份属性',dataIndex:'identityAttribute'},{title:'服务凭据',render:(_,p)=>p.hasSecret?'已加密保存':'未配置'},{title:'状态',render:(_,p)=><Space><Tag>{p.enabled?'登录已开放':'登录关闭'}</Tag><Tag>{p.bindingEnabled?'绑定已开放':'绑定关闭'}</Tag></Space>},{title:'操作',render:(_,p)=><Space><>{p.settingsManaged?<Link to="/system-status?section=ldap">平台配置</Link>:<Button type="link" onClick={()=>edit(p)}>编辑</Button>}</><Button type="link" onClick={()=>{close();setTesting(p);setOpen(true)}}>检测目录</Button><BackendLDAPSync provider={p}/></Space>}]}/>
 <Modal open={open} title={testing?'检测 '+testing.name:editing?'编辑 LDAP 目录':'新建 LDAP 目录'} onCancel={close} onOk={()=>void submit()} confirmLoading={busy} okText={testing?'开始检测':'保存'}>
 {error&&<Alert type="error" title={error}/>}
 <Form form={form} layout="vertical" preserve={false}>
 {testing?<><Alert type="info" title="服务连接与用户验证" description="两项均留空时检测服务账号和 Base DN；同时填写时验证目录用户密码及稳定身份。测试密码不会保存。"/>
 <Form.Item name="username" label="测试目录用户名"><Input maxLength={256} autoComplete="off"/></Form.Item><Form.Item name="password" label="测试目录密码"><Input.Password maxLength={1024} autoComplete="off"/></Form.Item></>:<FormTabs sections={[
 {key:'connection',label:'连接与证书',description:'目录地址、Base DN 和身份属性创建后不可更改；更换身份域请新建目录。',children:<>
 <Form.Item name="name" label="名称" rules={[{required:true}]}><Input maxLength={100}/></Form.Item>
 <Form.Item name="url" label="目录地址" rules={[{required:true},{pattern:/^ldaps?:\/\//,message:'使用 ldaps:// 或 ldap://（强制 StartTLS）'}]}><Input disabled={!!editing} placeholder="ldaps://ldap.example.com:636" maxLength={2048}/></Form.Item>
 <Form.Item name="baseDN" label="Base DN" rules={[{required:true}]}><Input disabled={!!editing} placeholder="dc=example,dc=com" maxLength={1024}/></Form.Item>
 <Form.Item name="caPEM" label="内部 CA 证书（PEM）" extra="可选；留空使用系统信任链。仅接受 CA 公钥证书，不接受私钥，不跳过证书验证。"><Input.TextArea rows={4} maxLength={65536}/></Form.Item></>},
 {key:'mapping',label:'账号查找',description:'用户输入会进行 LDAP 过滤器转义，仅允许唯一匹配；不会按邮箱匹配平台账号。',children:<>
 <Form.Item name="searchFilter" label="用户范围过滤器" rules={[{required:true}]}><Input maxLength={4096}/></Form.Item>
 <Form.Item name="loginAttribute" label="登录属性" rules={[{required:true},{pattern:/^[a-zA-Z][a-zA-Z0-9-]{0,63}$/}]}><Input placeholder="uid 或 sAMAccountName"/></Form.Item>
 <Form.Item name="identityAttribute" label="稳定身份属性" rules={[{required:true}]}><Select disabled={!!editing} options={[{value:'entryUUID',label:'entryUUID（OpenLDAP）'},{value:'objectGUID',label:'objectGUID（Active Directory）'}]}/></Form.Item>
 <Form.Item name="searchSizeLimit" label="目录同步读取上限" rules={[{required:true}]} extra="超过上限会拒绝整个预览，不导入部分结果。"><InputNumber min={1} max={10000} precision={0}/></Form.Item>
 <Form.Item name={['propertyMapping','nickname']} label="昵称属性" rules={[{pattern:/^[a-zA-Z][a-zA-Z0-9-]{0,63}$/,message:'请输入合法 LDAP 属性名'}]} extra="可选；仅设置新导入用户的昵称，留空使用目录账号。"><Input placeholder="cn 或 displayName" maxLength={64}/></Form.Item>
 <Form.Item name={['propertyMapping','mail']} label="邮箱属性" rules={[{pattern:/^[a-zA-Z][a-zA-Z0-9-]{0,63}$/,message:'请输入合法 LDAP 属性名'}]} extra="仅显示目录预览，不自动绑定、验证邮箱或启用密码找回。"><Input placeholder="mail" maxLength={64}/></Form.Item></>},
 {key:'security',label:'凭据与验证',description:'服务账号密码加密保存；保存配置需要复核当前管理员身份。',children:<>
 <Form.Item name="enabled" label="开放 LDAP 登录" valuePropName="checked"><Switch/></Form.Item><Form.Item name="bindingEnabled" label="允许个人绑定" valuePropName="checked"><Switch/></Form.Item>
 <Form.Item name="bindDN" label="服务账号 DN" rules={[{required:true}]}><Input maxLength={1024} placeholder="cn=readonly,dc=example,dc=com"/></Form.Item>
 <Form.Item name="bindPassword" label="服务账号密码" rules={[{required:!editing}]} extra={editing?'留空保留原密码；填写将替换。':undefined}><Input.Password autoComplete="new-password" maxLength={65536}/></Form.Item>
 <Form.Item name="password" label="当前平台登录密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
 <Form.Item name="totp" label="动态口令（已启用 MFA 时必填）"><Input maxLength={6} autoComplete="one-time-code"/></Form.Item></>}
 ]}/>}
 </Form></Modal></>;
}
