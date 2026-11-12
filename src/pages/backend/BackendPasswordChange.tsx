import {Alert,Button,Card,Form,Input,Typography} from 'antd';
import {useQuery} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';

type Status={localPassword:boolean;expired:boolean;mfaRequired:boolean;expirationDays:number;policy:{minLength:number;minCharacterType:number;mustNotContainUsername:boolean;mustNotBePalindrome:boolean;mustNotWeak:boolean}};
export default function BackendPasswordChange(){
 const [form]=Form.useForm(),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 const status=useQuery({queryKey:['account-password-policy'],queryFn:()=>requests.get<Status>('/account/password',{errorMode:'local'}),retry:false});
 const save=async(values:Record<string,string>)=>{setBusy(true);setError('');try{await requests.post('/account/password',values,{errorMode:'local'});form.resetFields();window.location.replace('/login')}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const p=status.data?.policy;
 return <Card title={status.data?.expired?'密码已到期，请设置新密码':'修改平台密码'} loading={status.isPending}>
  {(error||status.error)&&<Alert showIcon type="error" title={error||status.error?.message}/>}
  {status.data?.localPassword===false?<Alert showIcon type="info" title="当前账号未设置本地密码，请在所属身份提供方管理密码。"/>:p&&<>
   <Typography.Paragraph type="secondary">至少 {p.minLength} 个字符，至少 {p.minCharacterType} 类字符（字母／数字／其他）。{p.mustNotContainUsername?'不能包含账号。':''}{p.mustNotBePalindrome?'不能使用回文。':''}{p.mustNotWeak?'检查常见弱密码及重复字符。':''}</Typography.Paragraph>
   <Alert type="info" showIcon title="修改后需要重新登录并重新配置个人访问凭据" description="旧登录会话、原生授权、Passkey、API Key、个人 SSH 公钥及个人数据库密码会被撤销；资产共享凭据不受影响。" style={{marginBottom:16}}/>
   <Form form={form} layout="vertical" onFinish={save}>
    <Form.Item name="password" label="当前密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item>
    <Form.Item name="newPassword" label="新密码" rules={[{required:true},{validator:(_,v)=>!v||Array.from(v).length>=p.minLength?Promise.resolve():Promise.reject(new Error(`至少 ${p.minLength} 个字符`))}]}><Input.Password autoComplete="new-password"/></Form.Item>
    <Form.Item name="confirm" label="确认新密码" dependencies={['newPassword']} rules={[{required:true},({getFieldValue})=>({validator:(_,v)=>!v||v===getFieldValue('newPassword')?Promise.resolve():Promise.reject(new Error('两次输入不一致'))})]}><Input.Password autoComplete="new-password"/></Form.Item>
    {status.data?.mfaRequired&&<Form.Item name="totp" label="新的动态口令" extra="如刚刚使用过当前口令，请等待验证器生成下一组口令。" rules={[{required:true,pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>}
    <Button htmlType="submit" type="primary" loading={busy}>保存新密码并重新登录</Button>
   </Form>
  </>}
 </Card>
}
