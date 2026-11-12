import Modal from './BackendFormModal';
import {useState} from 'react';
import {Alert, Button, Checkbox, Typography} from 'antd';
import requests from '@/api/core/requests';
type Trust={assetId:string;host:string;port:number;publicKey:string;fingerprint:string;version:number};
type Observation={observed:Trust;trusted:Trust;assetVersion:number};
export default function BackendHostKey({assetId}:{assetId:string}) {
    const [open,setOpen]=useState(false),[data,setData]=useState<Observation>(),[checked,setChecked]=useState(false),[error,setError]=useState(''),[busy,setBusy]=useState(false);
    const discover=async()=>{setOpen(true);setData(undefined);setChecked(false);setError('');setBusy(true);try{setData(await requests.post<Observation>(`/admin/assets/${assetId}/host-key/discover`,undefined,{errorMode:'local'}))}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
    const trust=async()=>{if(!data||!checked)return;setBusy(true);setError('');try{await requests.put(`/admin/assets/${assetId}/host-key`,{...data.observed,assetVersion:data.assetVersion},{errorMode:'local'});setOpen(false)}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
    return <><Button size="small" onClick={()=>void discover()}>SSH 主机密钥</Button><Modal title="核对 SSH 主机身份" open={open} onCancel={()=>setOpen(false)} confirmLoading={busy} onOk={()=>void trust()} okText="信任此主机密钥" cancelText="取消" okButtonProps={{disabled:!checked||!data}}>
        <Alert type="warning" title="请通过主机控制台或管理员提供的可信渠道核对 SHA256 指纹。网络探测结果本身不代表可信。更换密钥会终止旧会话。"/>
        {error&&<Alert type="error" title={error}/>} {busy&&!data&&<p>正在读取主机公钥…</p>}
        {data&&<><p>目标：{data.observed.host}:{data.observed.port}</p><p>当前信任：{data.trusted.fingerprint||'无'}</p><Typography.Paragraph copyable>{data.observed.fingerprint}</Typography.Paragraph><Typography.Paragraph copyable style={{wordBreak:'break-all'}}>{data.observed.publicKey}</Typography.Paragraph>
        <Checkbox checked={checked} onChange={e=>setChecked(e.target.checked)}>已通过可信渠道核对上述指纹</Checkbox></>}
    </Modal></>;
}
