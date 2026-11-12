import {useEffect,useRef,useState} from 'react';
import {Alert,Button,Card,Spin} from 'antd';
import requests from '@/api/core/requests';

export default function BackendWebsiteLogin(){
 const started=useRef(false);const [error,setError]=useState('');
 useEffect(()=>{if(started.current)return;started.current=true;const q=new URLSearchParams(location.search);const id=q.get('websiteId')||'';const state=q.get('state')||'';const path=q.get('return')||'/';
  if(!/^[a-f0-9-]{36}$/.test(id)||!/^[a-f0-9]{64}$/.test(state)||!path.startsWith('/')||path.startsWith('//')){setError('网站登录参数无效，请重新从网站入口访问。');return}
  void requests.post<{url:string}>(`/portal/websites/${id}/login-code`,{state,return:path},{errorMode:'local'}).then(v=>{const target=new URL(v.url);if(target.protocol!=='https:'||target.username||target.password)throw new Error('网站回调地址无效');location.replace(target.href)}).catch(e=>setError((e as Error).message));
 },[]);
 return <Card title="正在授权网站访问" style={{maxWidth:560,margin:'15vh auto'}}>{error?<><Alert type="error" title={error}/><Button href="/x-website">返回我的 Web 资产</Button></>:<Spin description="验证授权并返回网站…"/>}</Card>;
}
