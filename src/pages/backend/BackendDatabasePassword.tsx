import {useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {Alert,Button,Divider,Form,Input,InputNumber,Space,Typography} from 'antd';
import requests from '@/api/core/requests';
export default function BackendDatabasePassword(){
 const proxy=useQuery({queryKey:['r12-proxy-status'],queryFn:()=>requests.get<{ready:boolean;postgres?:{ready:boolean}}>('/database-proxy/status',{errorMode:'local'}),retry:false});
 const query=useQuery({queryKey:['database-password'],queryFn:()=>requests.get<{active:boolean;scramReady:boolean;expiresAt:string|null;version:number}>('/account/database-password',{errorMode:'local'}),retry:false});
 const [form]=Form.useForm();const [secret,setSecret]=useState(''),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 const run=async(revoke=false)=>{setBusy(true);setError('');setSecret('');try{const values=await form.validateFields();const response=await fetch('/api/account/database-password',{method:revoke?'DELETE':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(values)});if(!response.ok){const body=await response.json();throw Error(body.message??'操作失败')}if(!revoke)setSecret((await response.json()).password);form.resetFields(['password','totp']);await query.refetch()}catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}};
 return <><Divider/><Typography.Title level={4}>个人数据库密码</Typography.Title>
 <Alert type="info" title={proxy.data?.ready||proxy.data?.postgres?.ready?'使用专用密码登录数据库代理；用户名为“平台用户名@数据库别名”。连接协议与端口见“我的数据库”。':'数据库代理尚未就绪；专用密码可先创建，连接状态请在“我的数据库”查看。'}/>
 {(error||query.isError)&&<Alert type="error" title={error||query.error?.message}/>}
 <p>{query.data?.active?`已启用，有效期至 ${new Date(query.data.expiresAt!).toLocaleString()}`:'未启用或已失效'}</p>
 {query.data?.active&&<Alert type={query.data.scramReady?'info':'warning'} title={query.data.scramReady?'已具备 MySQL 与 PostgreSQL SCRAM 密码验证材料。PostgreSQL 客户端入口以“我的数据库”的开放状态为准。':'当前为旧版 MySQL 密码；后续使用 PostgreSQL 需重置以生成 SCRAM 验证材料。重置会使原密码失效。'}/>}
 {secret&&<><Alert type="warning" title="新密码仅显示这一次，请保存；重置后旧密码立即失效。"/><Typography.Paragraph copyable>{secret}</Typography.Paragraph><Button onClick={()=>setSecret('')}>已保存，隐藏密码</Button></>}
 <Form form={form} layout="vertical" initialValues={{days:90}} style={{maxWidth:480}}>
 <Form.Item name="password" label="当前登录密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
 <Form.Item name="totp" label="动态口令（已启用双因素认证时必填）"><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
 <Form.Item name="days" label="有效期（天）" rules={[{required:true}]}><InputNumber min={1} max={365}/></Form.Item>
 <Space><Button loading={busy} disabled={query.isError||query.isPending} onClick={()=>void run()}>生成 / 重置密码</Button><Button danger loading={busy} disabled={!query.data?.active} onClick={()=>void run(true)}>吊销密码</Button></Space>
 </Form></>;
}
