import {useBackendBranding} from '@/hook/useBackendBranding';
import {rememberReturn,consumeReturn} from './login-return';
import {Alert, Button, Card, Form, Input, Radio, Select, Space, Spin, Typography} from 'antd';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {useEffect,useState} from 'react';
import requests from '@/api/core/requests';
import CaptchaChallenge from '@/pages/account/CaptchaChallenge';
import BackendContactLogin from './BackendContactLogin';
import BackendSecurityPanel from './BackendSecurityPanel';
import BackendPasswordChange from './BackendPasswordChange';
import type {CaptchaProof} from '@/api/account-api';
import {startAuthentication} from '@simplewebauthn/browser';

export default function BackendLoginPage(){
    const branding=useBackendBranding();
    useEffect(()=>rememberReturn(location.search,location.origin,sessionStorage),[]);
    const client=useQueryClient();const [form]=Form.useForm();const [proof,setProof]=useState<CaptchaProof>();
    const [error,setError]=useState(new URLSearchParams(location.search).has('oidcError')?'联合登录失败，请确认已绑定账号且提供方已启用，然后重新尝试。':'');const [busy,setBusy]=useState(false);const [method,setMethod]=useState('totp');
    const state=useQuery({queryKey:['r02-login'],queryFn:()=>requests.get<{status:string;passwordEnabled:boolean;captchaEnabled:boolean;webauthnEnabled:boolean;smsEnabled:boolean;recoveryEnabled:boolean}>('/login-status',{errorMode:'local'}),retry:false,refetchOnWindowFocus:false,refetchOnReconnect:false});
    const [loginSource,setLoginSource]=useState('password');
    const ldap=useQuery({queryKey:['r13-ldap-login-providers'],queryFn:()=>requests.get<{items:{id:string;name:string}[]}>('/ldap/login/providers',{errorMode:'local'}),enabled:state.data?.status==='Unlogged',retry:false});
    useEffect(()=>{if(state.data?.passwordEnabled===false&&loginSource==='password'&&ldap.data?.items.length)setLoginSource(ldap.data.items[0].id)},[state.data?.passwordEnabled,ldap.data,loginSource]);
    const oidc=useQuery({queryKey:['r13-oidc-login-providers'],queryFn:()=>requests.get<{items:{id:string;name:string}[]}>('/oidc/login/providers',{errorMode:'local'}),enabled:state.data?.status==='Unlogged',retry:false});
    const oidcLogin=async(id:string)=>{setBusy(true);setError('');try{if(state.data?.captchaEnabled&&(!proof||proof.expiresAt<=Date.now()))throw Error('请先完成滑块验证码');const result=await requests.post<{authorizationUrl:string}>('/oidc/login/'+id+'/start',{key:proof?.key??''},{errorMode:'local'});window.location.assign(result.authorizationUrl)}catch(e){setError((e as Error).message)}finally{setProof(undefined);setBusy(false)}};
    const passkeyLogin=async()=>{setBusy(true);setError('');try{
        if(!window.isSecureContext)throw new Error('Passkey 需要可信 HTTPS 页面');
        if(state.data?.captchaEnabled&&(!proof||proof.expiresAt<=Date.now()))throw new Error('请先完成滑块验证码');
        const start=await requests.post<{token:string;options:Parameters<typeof startAuthentication>[0]['optionsJSON']}>('/passkey/login/start',{key:proof?.key??''},{errorMode:'local'});
        const response=await startAuthentication({optionsJSON:start.options});
        await requests.post('/passkey/login/finish',{token:start.token,response},{errorMode:'local'});
        await state.refetch();
    }catch(e){setError((e as Error).message)}finally{setProof(undefined);setBusy(false)}};
    useEffect(()=>{if(state.data?.status==='Logged In')window.location.replace(safeLoginReturn());if((state.error as {status?:number})?.status===418)window.location.replace('/setup')},[state.data,state.error]);
    const submit=async(values:Record<string,string>)=>{setBusy(true);setError('');try{
        if(state.data?.status==='OTP Required')await requests.post('/validate-totp',{[method]:values.proof},{errorMode:'local'});
        else {if(state.data?.captchaEnabled&&(!proof||proof.expiresAt<=Date.now()))throw new Error('请先完成滑块验证码');await requests.post(loginSource==='password'?'/login':'/ldap/login',{...values,source:loginSource==='password'?'password':'ldap',providerId:loginSource==='password'?undefined:loginSource,key:proof?.key??''},{errorMode:'local'})}
        form.resetFields();client.removeQueries({queryKey:['r02-mfa']});await state.refetch();
    }catch(e){setError((e as Error).message);setProof(undefined)}finally{setBusy(false)}};
    const reset=async()=>{sessionStorage.removeItem('nt-login-return');await requests.post('/logout',undefined,{errorMode:'local'});client.clear();window.location.replace('/login')};
    return <main className="min-h-screen bg-slate-100 p-8 flex justify-center items-center"><div className="w-full max-w-lg space-y-4">
        <Typography.Title level={2}>{branding.data?.logo&&<img src={branding.data.logo} alt="系统 Logo" style={{width:40,height:40,objectFit:"contain",display:"inline-block",marginRight:12,verticalAlign:"middle"}}/>}{branding.data?.name??'Puppeteer'}</Typography.Title>
        {state.isPending?<Spin/>:state.isError?<Alert type="error" title="新后端暂不可用" description={state.error.message}/>:state.data?.status==='MFA Enrollment Required'?<>
            <BackendSecurityPanel/><Button onClick={()=>void reset()}>退出并返回登录</Button>
        </>:state.data?.status==='Password Change Required'?<><BackendPasswordChange/><Button onClick={()=>void reset()}>退出并返回登录</Button></>:<Card title={state.data?.status==='OTP Required'?'完成双因素认证':'登录新后端'}>
            {error&&<Alert className="mb-4" type="error" showIcon title={error}/>}
            <Form form={form} layout="vertical" onFinish={v=>void submit(v)}>
                {state.data?.status==='OTP Required'?<>
                    <Radio.Group className="mb-4" value={method} onChange={e=>{setMethod(e.target.value);form.resetFields()}} options={[{label:'动态口令',value:'totp'},{label:'恢复码',value:'recoveryCode'}]}/>
                    <Form.Item name="proof" label={method==='totp'?'6位动态口令':'一次性恢复码'} rules={[{required:true}]}><Input autoComplete="one-time-code"/></Form.Item>
                </>:<>
                    {!!ldap.data?.items.length&&<Form.Item label="登录方式"><Select value={loginSource} onChange={v=>{setLoginSource(v);form.resetFields();setProof(undefined)}} options={[...(state.data?.passwordEnabled?[{value:'password',label:'平台密码'}]:[]),...ldap.data.items.map(p=>({value:p.id,label:p.name+'（LDAP）'}))]}/></Form.Item>}
                    {(state.data?.passwordEnabled||loginSource!=='password')&&<><Form.Item name="username" label="账号" rules={[{required:true}]}><Input autoComplete="username"/></Form.Item>
                    <Form.Item name="password" label="密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item></>}
                    {state.data?.captchaEnabled&&<div className="mb-4"><CaptchaChallenge value={proof} onChange={setProof}/></div>}
                </>}
                <Space>{(state.data?.passwordEnabled||loginSource!=='password'||state.data?.status==='OTP Required')&&<Button type="primary" htmlType="submit" loading={busy}>验证并继续</Button>}{state.data?.status==='OTP Required'&&<Button onClick={()=>void reset()}>返回登录</Button>}</Space>
            </Form>
            {state.data?.webauthnEnabled&&state.data.status==='Unlogged'&&<Button style={{marginTop:16}} onClick={()=>void passkeyLogin()} disabled={busy}>使用 Passkey 登录</Button>}
            {state.data?.status==='Unlogged'&&<Space wrap style={{marginTop:16}}>{state.data.smsEnabled&&<BackendContactLogin purpose="sms-login" proof={proof} captchaEnabled={state.data.captchaEnabled} onUsed={()=>setProof(undefined)} onSuccess={()=>void state.refetch()}/ >}{state.data.recoveryEnabled&&<BackendContactLogin purpose="recovery" proof={proof} captchaEnabled={state.data.captchaEnabled} onUsed={()=>setProof(undefined)} onSuccess={()=>void state.refetch()}/ >}{oidc.data?.items.map(p=><Button key={p.id} disabled={busy} onClick={()=>void oidcLogin(p.id)}>使用 {p.name} 登录</Button>)}</Space>}
        </Card>}
        <Typography.Text type="secondary">本机开发验收环境 · 未开放功能不会转发至旧后端</Typography.Text>
    </div></main>;
}

function safeLoginReturn(){return consumeReturn(location.search,location.origin,sessionStorage)}
