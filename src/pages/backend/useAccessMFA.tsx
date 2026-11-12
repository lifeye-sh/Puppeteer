import {useEffect,useRef,useState} from 'react';
import {Alert,Input,Typography} from 'antd';
import Modal from './BackendFormModal';
import requests from '@/api/core/requests';
import {isApiError} from '@/api/core/api-error';

type Status={required:boolean;verified:boolean;enrolled:boolean;freshnessSeconds:number};
type Pending={resolve:()=>void;reject:(error:Error)=>void};

export default function useAccessMFA(){
 const pending=useRef<Pending|null>(null);
 const alive=useRef(true);
 const sequence=useRef(0);
 const [status,setStatus]=useState<Status>();
 const [code,setCode]=useState('');
 const [error,setError]=useState('');
 const [busy,setBusy]=useState(false);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;sequence.current++;pending.current?.reject(Error('连接已取消'));pending.current=null}},[]);
 const cancel=()=>{sequence.current++;const old=pending.current;pending.current=null;setStatus(undefined);setCode('');setError('');setBusy(false);old?.reject(Error('已取消二次验证'))};
 const verify=async()=>{
  const attempt=++sequence.current;
  pending.current?.reject(Error('连接已重新申请'));pending.current=null;
  setStatus(undefined);setCode('');setError('');setBusy(false);
  let state:Status;
  try{state=await requests.get<Status>('/account/access-mfa',{errorMode:'local'})}
  catch(e){
   // During a rolling upgrade an older server explicitly reports 501. Its
   // reservation handler remains authoritative; never ignore auth/network errors.
   if(isApiError(e)&&e.status===501)return;
   throw e;
  }
  if(!alive.current||attempt!==sequence.current)throw Error('连接已取消');
  if(!state.required||state.verified)return;
  if(!state.enrolled)throw Error('连接远程资产需要二次验证，请先在“我的账号”中绑定动态口令。');
  await new Promise<void>((resolve,reject)=>{pending.current={resolve,reject};setStatus(state)});
 };
 const submit=async()=>{
  const current=pending.current;if(!current||busy)return;
  setBusy(true);setError('');
  try{
   await requests.post('/account/access-mfa',{totp:code},{errorMode:'local'});
   if(!alive.current||pending.current!==current)return;
   pending.current=null;setStatus(undefined);setCode('');current.resolve();
  }catch(e){if(alive.current&&pending.current===current){setCode('');setError((e as Error).message)}}
  finally{if(alive.current)setBusy(false)}
 };
 const dialog=<Modal title="验证后连接远程资产" open={!!status} width={420} onCancel={cancel} onOk={()=>void submit()} confirmLoading={busy} okText="验证并连接" okButtonProps={{disabled:code.length!==6}} mask={{closable:false}} destroyOnHidden>
  <Typography.Paragraph type="secondary">请输入身份验证器中的六位动态口令。{status?.freshnessSeconds===0?'本次验证仅用于一次新连接。':`验证可在当前登录会话中复用 ${Math.floor((status?.freshnessSeconds??0)/60)} 分钟。`}</Typography.Paragraph>
  {error&&<Alert type="error" showIcon title={error} style={{marginBottom:12}}/>}
  <Input aria-label="接入动态口令" autoComplete="one-time-code" inputMode="numeric" maxLength={6} value={code} onChange={e=>setCode(e.target.value.replace(/\D/g,''))} onPressEnter={()=>void submit()} autoFocus/>
 </Modal>;
 return {verify,dialog};
}
