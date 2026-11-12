import {useState} from 'react';
import {Alert,Button,Form,Input,Typography} from 'antd';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';

export default function BackendFederationUser({kind,departmentId,departmentName}:{kind:'oidc'|'wecom';departmentId:string;departmentName:string}){
 const [form]=Form.useForm();const [snapshot,setSnapshot]=useState<{id:string;name:string}>(),[busy,setBusy]=useState(false),[error,setError]=useState(''),[created,setCreated]=useState('');
 const close=()=>{if(busy)return;form.resetFields();setSnapshot(undefined);setError('')};
 const save=async()=>{
  if(!snapshot)return;setBusy(true);setError('');
  try{const v=await form.validateFields();await requests.post('/admin/users',{...v,type:'user',status:'enabled',roleIds:[],defaultDepartmentSource:kind,defaultDepartmentId:snapshot.id},{errorMode:'local'});setCreated(v.username);form.resetFields();setSnapshot(undefined)}catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}
 };
 return <>
  {created&&<Alert showIcon type="success" title={`已创建 ${created}`} description="请通过你们的安全渠道交付初始登录信息。用户登录后在个人中心完成外部身份绑定，绑定必须由用户本人验证。"/>}
  <Button disabled={!departmentId||!departmentName} onClick={()=>{setCreated('');setError('');form.resetFields();setSnapshot({id:departmentId,name:departmentName})}}>按默认部门预建用户</Button>
  <Modal width={600} open={!!snapshot} title="预建待绑定用户" onCancel={close} onOk={()=>void save()} confirmLoading={busy} mask={{closable:false}} keyboard={!busy} cancelButtonProps={{disabled:busy}} forceRender>
   {error&&<Alert type="error" showIcon title={error}/>}
   <Typography.Paragraph>创建普通平台账号，加入部门「{snapshot?.name}」。账号可获得该部门已有的资源授权；不会赋予管理角色或自动绑定外部身份。默认部门变化时需重新打开表单确认。</Typography.Paragraph>
   <Form form={form} layout="vertical" preserve={false}>
    <Form.Item name="username" label="登录账号" rules={[{required:true,max:64}]}><Input autoComplete="off"/></Form.Item>
    <Form.Item name="nickname" label="用户姓名" rules={[{required:true,max:100}]}><Input/></Form.Item>
    <Form.Item name="password" label="初始密码" extra="须满足平台当前密码策略。" rules={[{required:true,max:1024}]}><Input.Password autoComplete="new-password"/></Form.Item>
   </Form>
  </Modal>
 </>;
}
