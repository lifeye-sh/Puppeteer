import {useEffect,useState} from 'react';
import {Alert,Button,Space,Switch} from 'antd';
import requests from '@/api/core/requests';
type Policy={upload:boolean;download:boolean;clipboardCopy:boolean;clipboardPaste:boolean};
export default function BackendChannelPolicy({userId}:{userId:string}){
 const [policy,setPolicy]=useState<Policy>(),[error,setError]=useState(''),[busy,setBusy]=useState(false),[saved,setSaved]=useState(false);
 useEffect(()=>{void requests.get<Policy>(`/admin/users/${userId}/channels`,{errorMode:'local'}).then(setPolicy).catch(e=>setError(e.message))},[userId]);
 const save=async()=>{setBusy(true);setSaved(false);setError('');try{await requests.put(`/admin/users/${userId}/channels`,policy,{errorMode:'local'});setSaved(true)}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <Space orientation="vertical"><Alert type="info" title="RDP 与平台共享存储通道策略" description="保存后会结束该用户现有 RDP 会话，重新连接使用新策略。SSH/SFTP 不受此设置影响。"/>{error&&<Alert type="error" title={error}/>} {policy&&(['upload','download','clipboardCopy','clipboardPaste'] as const).map((key,i)=><Space key={key}><Switch checked={policy[key]} disabled={busy} onChange={v=>{setPolicy({...policy,[key]:v});setSaved(false)}}/>{['写入共享存储','读取共享文件内容','远端剪贴板 → 本机','本机剪贴板 → 远端'][i]}</Space>)}<Button type="primary" disabled={!policy} loading={busy} onClick={()=>void save()}>保存策略</Button>{saved&&<span>已保存</span>}</Space>;
}
