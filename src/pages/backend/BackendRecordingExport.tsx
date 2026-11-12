import {useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {Button, Space, Tooltip, Typography} from 'antd';
import requests from '@/api/core/requests';

type Status={available:boolean;export:{state:string;progress:number;reason:string;size:number}};
const reasons:Record<string,string>={conversion_failed:'转换失败，请重试',converter_unavailable:'转换服务暂不可用',recording_integrity_failed:'原始录屏完整性校验失败',recording_limit:'录屏超出转换限制',output_limit:'视频超过 256 MiB',cache_expired:'缓存已到期，可重新生成',cache_missing:'缓存已失效，可重新生成',cache_corrupt:'缓存校验失败，可重新生成',worker_lease_lost:'转换进程中断，请重试'};
export default function BackendRecordingExport({sessionId}:{sessionId:string}){
 const [busy,setBusy]=useState(false),[error,setError]=useState('');
 const path=`/sessions/${sessionId}/recording/mp4`;
 const query=useQuery({queryKey:['recording-mp4',sessionId],queryFn:()=>requests.get<Status>(path,{errorMode:'local'}),retry:false,refetchInterval:q=>['queued','running'].includes(q.state.data?.export.state??'')?2000:15000});
 const change=async(cancel=false)=>{setBusy(true);setError('');try{if(cancel)await requests.delete(path,{errorMode:'local'});else await requests.post(path,undefined,{errorMode:'local'});await query.refetch()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const value=query.data?.export,active=['queued','running'].includes(value?.state??'');
 return <Space size={4} wrap>
  {value?.state==='succeeded'?<Button href={`/api${path}/download`} download>下载 MP4（{value.size<1024?`${value.size} B`:value.size<1024*1024?`${(value.size/1024).toFixed(1)} KiB`:`${(value.size/1024/1024).toFixed(1)} MiB`}）</Button>:active?<><Typography.Text type="secondary">{value?.state==='queued'?'等待转换':`后台转换 ${value?.progress??0}%`}</Typography.Text><Button size="small" loading={busy} onClick={()=>void change(true)}>取消</Button></>:<Tooltip title={query.isError?query.error.message:!query.data?.available?'转换服务未配置':reasons[value?.reason??'']??'服务器后台生成，关闭页面也会继续'}><Button disabled={!query.data?.available} loading={busy||query.isPending} onClick={()=>void change()}>{value&&value.state!=='none'?'重新生成 MP4':'生成 MP4'}</Button></Tooltip>}
  {error&&<Typography.Text type="danger">{error}</Typography.Text>}
 </Space>;
}
