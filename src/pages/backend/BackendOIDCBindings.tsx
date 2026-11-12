import {Alert,Button,Form,Input,Select,Table,Typography} from 'antd';
import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';
type Binding={id:string;providerId:string;name:string;issuer:string;subject:string;createdAt:string};
type Provider={id:string;name:string;issuer:string};
export default function BackendOIDCBindings(){
 const cache=useQueryClient();const [form]=Form.useForm();const [open,setOpen]=useState(false),[removing,setRemoving]=useState<Binding>(),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const query=useQuery({queryKey:['r13-oidc-bindings'],queryFn:()=>requests.get<{items:Binding[];providers:Provider[]}>('/account/oidc-identities',{errorMode:'local'})});
 const available=(query.data?.providers??[]).filter(p=>!query.data?.items.some(b=>b.providerId===p.id));
 const close=()=>{setOpen(false);setRemoving(undefined);setError('');form.resetFields()};
 const submit=async()=>{setError('');try{const v=await form.validateFields();setBusy(true);if(removing){const response=await fetch('/api/account/oidc-identities/'+removing.id,{method:'DELETE',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:v.password,totp:v.totp})});if(!response.ok){const body=await response.json();throw Error(body.message??'解绑失败')};await cache.invalidateQueries({queryKey:['r13-oidc-bindings']});close()}else{const result=await requests.post<{authorizationUrl:string}>('/account/oidc-providers/'+v.providerId+'/bind',{password:v.password,totp:v.totp},{errorMode:'local'});window.location.assign(result.authorizationUrl)}}catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}};
 return <>
 <Alert type="info" showIcon title="绑定外部账号" description="绑定需验证当前平台身份及外部账号。同一外部账号只能绑定一个平台账号；登录是否开放由管理员配置。"/>
 {query.error&&<Alert type="error" title={query.error.message}/>}
 <Button type="primary" style={{margin:'12px 0'}} disabled={!available.length} onClick={()=>{close();setOpen(true)}}>绑定账号</Button>
 {!query.isLoading&&!available.length&&<Typography.Paragraph type="secondary">暂无可绑定的提供方，请联系管理员开放绑定。</Typography.Paragraph>}
 <Table<Binding> rowKey="id" size="small" loading={query.isLoading} dataSource={query.data?.items??[]} pagination={{pageSize:5}} scroll={{x:650}} columns={[{title:'提供方',dataIndex:'name'},{title:'身份域',dataIndex:'issuer'},{title:'外部账号标识',dataIndex:'subject'},{title:'绑定时间',dataIndex:'createdAt',render:v=>new Date(v).toLocaleString()},{title:'操作',render:(_,v)=><Button type="link" danger onClick={()=>{close();setRemoving(v);setOpen(true)}}>解绑</Button>}]}/>
 <Modal open={open} title={removing?'解除 '+removing.name+' 绑定':'绑定外部账号'} onCancel={close} onOk={()=>void submit()} confirmLoading={busy} okText={removing?'确认解绑':'前往授权'}>
 {error&&<Alert type="error" title={error}/>}
 {removing&&<Alert type="warning" showIcon title="解绑立即生效" description="未完成的绑定授权也将失效。再次绑定需要重新验证身份。"/>}
 <Form form={form} layout="vertical" preserve={false}>
 {!removing&&<Form.Item name="providerId" label="身份提供方" rules={[{required:true}]}><Select options={available.map(p=>({value:p.id,label:p.name}))}/></Form.Item>}
 <Form.Item name="password" label="当前平台登录密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
 <Form.Item name="totp" label="动态口令（已启用 MFA 时必填）"><Input maxLength={6} autoComplete="one-time-code"/></Form.Item>
 </Form></Modal></>;
}
