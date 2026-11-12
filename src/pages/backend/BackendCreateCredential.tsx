import {Alert, Checkbox, Form, Input, Select, Typography} from 'antd';
import {useRef, useState} from 'react';
import {useQueryClient} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';

type CreatedCredential = {id:string;name:string;username:string;kind:string};
type Values = {name:string;username:string;kind:string;generate?:boolean;password?:string;privateKey?:string;passphrase?:string};

/** Mount only while open so cancelling or saving discards all secret form values. */
export default function BackendCreateCredential({passwordOnly=false, onCreated, onCancel}:{
 passwordOnly?:boolean;
 onCreated:(credential:CreatedCredential)=>void;
 onCancel:()=>void;
}) {
 const [form]=Form.useForm<Values>();
 const cache=useQueryClient();
 const [busy,setBusy]=useState(false),[error,setError]=useState('');
 const submitting=useRef(false);
 const kind=Form.useWatch('kind',form),generate=Form.useWatch('generate',form);
 const save=async(values:Values)=>{
  if(submitting.current)return;
  submitting.current=true;setBusy(true);setError('');
  try {
   const generateKey=!passwordOnly&&values.kind==='private-key'&&!!values.generate;
   const metadata={name:values.name.trim(),username:values.username.trim(),kind:passwordOnly?'password':values.kind};
   const result=await requests.post<{id:string}>('/admin/credentials',{
    ...metadata,generate:generateKey,
    ...(!generateKey?{secret:metadata.kind==='password'?{password:values.password}:{privateKey:values.privateKey,passphrase:values.passphrase??''}}:{}),
   },{errorMode:'local'});
   const credential={...metadata,id:result.id};
   // Publish only public metadata, never password/private-key values. Refetch failures
   // must not turn a successful create into a retry that creates a second credential.
   for(const key of ['r03-credentials','r12-database-credentials']){
    cache.setQueryData<CreatedCredential[]>([key],old=>[...(old??[]).filter(c=>c.id!==credential.id),{...credential,version:1,activeVersion:1,publicKey:'',assetCount:0}]);
    void cache.invalidateQueries({queryKey:[key]}).catch(()=>{});
   }
   form.resetFields();
   onCreated(credential);
  }catch(e){setError((e as Error).message)}
  finally{submitting.current=false;setBusy(false)}
 };
 return <Modal open title="新建凭据" width={640} okText="保存并选用" confirmLoading={busy}
  closable={!busy} keyboard={!busy} mask={{closable:!busy}} cancelButtonProps={{disabled:busy}}
  onCancel={()=>{if(!submitting.current){form.resetFields();onCancel()}}} onOk={()=>form.submit()}>
  <Typography.Paragraph type="secondary">保存后自动选用，资产表单中的内容会保留。凭据独立保存，可在“授权凭据”中管理；此操作不会创建远端账号或修改其权限。</Typography.Paragraph>
  {error&&<Alert type="error" showIcon title={error}/>}
  <Form name="inline-credential" form={form} layout="vertical" initialValues={{kind:'password',generate:false}} onFinish={save} disabled={busy}>
   <Form.Item name="name" label="凭据名称" rules={[{required:true,whitespace:true,max:100}]}><Input autoFocus maxLength={100}/></Form.Item>
   <Form.Item name="username" label="远程用户名" rules={[{required:true,whitespace:true,max:128}]}><Input autoComplete="off" maxLength={128}/></Form.Item>
   <Form.Item name="kind" label="凭据类型"><Select disabled={passwordOnly||busy} options={[{value:'password',label:'密码'},...(!passwordOnly?[{value:'private-key',label:'SSH 私钥'}]:[])]} onChange={()=>form.setFieldsValue({generate:false,password:undefined,privateKey:undefined,passphrase:undefined})}/></Form.Item>
   {kind==='private-key'?<>
    <Form.Item name="generate" valuePropName="checked"><Checkbox>由平台生成 Ed25519 密钥</Checkbox></Form.Item>
    {generate?<Alert type="info" showIcon title="保存后请到“授权凭据”复制公钥并安装到目标主机，再连接资产。"/>:<>
     <Form.Item name="privateKey" label="私钥 PEM" rules={[{required:true,max:32768}]}><Input.TextArea rows={5} autoComplete="off"/></Form.Item>
     <Form.Item name="passphrase" label="私钥口令（如有）"><Input.Password autoComplete="new-password" maxLength={4096}/></Form.Item>
    </>}
   </>:<Form.Item name="password" label="远程密码" rules={[{required:true,max:4096}]}><Input.Password autoComplete="new-password" maxLength={4096}/></Form.Item>}
  </Form>
 </Modal>;
}
