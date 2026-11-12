import BackendDatabasePassword from './BackendDatabasePassword';
import BackendPasswordChange from './BackendPasswordChange';
import BackendPersonalCredentials from './BackendPersonalCredentials';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import {Alert, Button, Card, Form, Input, QRCode, Space, Typography} from 'antd';
import {useQuery} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';

export default function BackendSecurityPanel() {
    const {capabilities}=useBackendRuntime();
    const status=useQuery({queryKey:['r02-mfa'],queryFn:()=>requests.get<{enabled:boolean;required:boolean;phase:string;passwordRequired?:boolean}>('/account/mfa',{errorMode:'local'}),retry:false});
    const [enrollment,setEnrollment]=useState<{secret:string;uri:string}>();
    const [codes,setCodes]=useState<string[]>();
    const [error,setError]=useState(''); const [busy,setBusy]=useState(false);
    const run=async(fn:()=>Promise<void>)=>{setError('');setBusy(true);try{await fn()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
    return <>{status.data?.phase==='authenticated'&&<div style={{marginBottom:16}}><BackendPasswordChange/></div>}<Card title="双因素认证与恢复码">
        {(error||status.isError)&&<Alert type="error" showIcon title={error||status.error?.message} className="mb-4"/>}
        {codes ? <Space orientation="vertical" className="w-full">
            <Alert type="warning" showIcon title="恢复码仅显示这一次，请保存到安全位置" description="每个恢复码只能使用一次。使用恢复码登录后，必须重新绑定验证器才能访问业务页面。"/>
            <Typography.Paragraph copyable={{text:codes.join('\n')}}><pre>{codes.join('\n')}</pre></Typography.Paragraph>
            <Button type="primary" onClick={()=>window.location.assign('/')}>我已保存，继续</Button>
        </Space> : status.data && <>
            <Alert className="mb-4" showIcon type={status.data.required?'warning':'info'} title={status.data.required?'必须重新绑定验证器':status.data.enabled?'已启用 TOTP 双因素认证':'尚未启用 TOTP'}
                description={status.data.enabled&&!status.data.required?'登录时需要密码与动态口令；无法使用验证器时，可在登录页使用恢复码。管理员重置需重新验证身份并记录原因。':'使用支持 TOTP 的验证器扫描二维码，或手动导入密钥。绑定成功后会撤销其他会话。'}/>
            {(!status.data.enabled||status.data.required)&&!enrollment&&<Form layout="vertical" onFinish={v=>void run(async()=>setEnrollment(await requests.post('/account/mfa/enroll',v,{errorMode:'local'})))}>
                {status.data.passwordRequired===false?<Alert className="mb-4" type="info" showIcon title="已验证外部登录身份" description="请在本次五分钟绑定会话内完成验证器绑定，无需输入平台本地密码。超时后请重新通过原身份来源登录。"/>:<Form.Item name="password" label="当前登录密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>}
                <Button htmlType="submit" loading={busy}>开始绑定验证器</Button>
            </Form>}
            {enrollment&&<Space orientation="vertical" className="w-full">
                <QRCode value={enrollment.uri}/><Typography.Paragraph copyable>{enrollment.secret}</Typography.Paragraph>
                <Typography.Text type="secondary">密钥有效期 5 分钟。6 位动态口令每 30 秒更新，同一时间步不可重复使用。</Typography.Text>
                <Form layout="vertical" onFinish={v=>void run(async()=>{const r=await requests.post<{recoveryCodes:string[]}>('/account/mfa/confirm',v,{errorMode:'local'});setCodes(r.recoveryCodes);setEnrollment(undefined)})}>
                    <Form.Item name="totp" label="验证器动态口令" rules={[{required:true,pattern:/^\d{6}$/,message:'请输入6位数字'}]}><Input autoComplete="one-time-code" inputMode="numeric" maxLength={6}/></Form.Item>
                    <Button type="primary" htmlType="submit" loading={busy}>确认绑定并生成恢复码</Button>
                </Form>
            </Space>}
        </>}
        {status.data&&!status.data.required&&<><BackendDatabasePassword/>{capabilities.features['identity.api-keys']?.state==='available'&&<BackendPersonalCredentials/>}</>}
    </Card></>;
}
