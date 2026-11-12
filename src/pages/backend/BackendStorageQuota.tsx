import {useEffect,useState} from 'react';
import {Alert,Button,InputNumber,Space} from 'antd';
import requests from '@/api/core/requests';
export default function BackendStorageQuota({userId}:{userId:string}){
 const [quota,setQuota]=useState<number>(5),[used,setUsed]=useState(0),[error,setError]=useState(''),[busy,setBusy]=useState(false),[loaded,setLoaded]=useState(false);
 useEffect(()=>{setLoaded(false);void requests.get<{quotaBytes:number;usedBytes:number}>(`/admin/users/${userId}/storage`,{errorMode:'local'}).then(v=>{setQuota(v.quotaBytes/2**30);setUsed(v.usedBytes/2**30);setLoaded(true)}).catch(e=>setError(e.message))},[userId]);
 return <Space orientation="vertical">{error&&<Alert type="error" title={error}/>}<span>已使用 {used.toFixed(2)} GiB。默认 5 GiB，设为 0 停止新增；调低配额不会删除已有文件。</span><Space><InputNumber aria-label="用户存储配额 GiB" min={0} max={1024} precision={2} value={quota} onChange={v=>setQuota(v??0)}/>GiB<Button type="primary" disabled={!loaded} loading={busy} onClick={async()=>{setBusy(true);setError('');try{await requests.put(`/admin/users/${userId}/storage`,{quotaBytes:Math.floor(quota*2**30)},{errorMode:'local'})}catch(e){setError((e as Error).message)}finally{setBusy(false)}}}>保存配额</Button></Space></Space>;
}
