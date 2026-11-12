import {useEffect, useRef, useState} from 'react';
import {Alert, Button, Card, Checkbox, Input, Select, Space, Switch, Typography} from 'antd';
import {useQuery} from '@tanstack/react-query';
import requests from '@/api/core/requests';

const opts={errorMode:'local' as const};
export type AIScope={contextType:string;contextId:string};
export function AIAssetSelect({value,onChange}:{value:string;onChange:(value:string)=>void}){
 const [search,setSearch]=useState('');
 const assets=useQuery({queryKey:['ai-select-assets',search],queryFn:()=>requests.get<{items:{id:string;name:string;host:string}[]}>('/portal/assets?pageSize=100&search='+encodeURIComponent(search),opts)});
 return <><Select aria-label="选择 AI 资产" showSearch={{filterOption:false,onSearch:setSearch}} value={value||undefined} onChange={onChange} style={{minWidth:200,maxWidth:'100%'}} placeholder="搜索并选择资产" loading={assets.isFetching} options={assets.data?.items.map(a=>({value:a.id,label:a.name+' · '+a.host}))}/>{assets.error&&<Typography.Text type="danger">{assets.error.message}</Typography.Text>}</>;
}
export function AIAssetSettings(){
 const [asset,setAsset]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const access=useQuery({queryKey:['ai-asset-access',asset],enabled:!!asset,queryFn:()=>requests.get<{enabled:boolean;version:number}>('/admin/ai/assets/'+asset,opts)});
 const save=async(enabled:boolean)=>{if(!access.data)return;setBusy(true);setError('');try{await requests.put('/admin/ai/assets/'+asset,{...access.data,enabled},opts);await access.refetch()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <Card size="small" title="逐个资产开放 AI 分析与工具"><Space wrap><AIAssetSelect value={asset} onChange={setAsset}/>{asset&&<><Switch aria-label="允许该资产 AI 分析" checked={access.data?.enabled??false} disabled={!access.data||busy} onChange={v=>void save(v)}/><span>允许该资产 AI 分析</span></>}</Space><p>新资产默认关闭。还需启用对应系统开关并具备资产授权；发送元数据须逐次确认，工具操作须另行审批。</p>{(error||access.error)&&<Alert type="error" title={error||access.error?.message}/>}</Card>;
}
export function AIContextChooser({scope,onChange,locked,disabled,digest,onConfirm}:{scope:AIScope;onChange:(v:AIScope)=>void;locked:boolean;disabled:boolean;digest:string;onConfirm:(v:string)=>void}){
 const {contextType,contextId}=scope;
 const details=useRef<HTMLDetailsElement>(null);
 const valid=/^[0-9a-f-]{36}$/i.test(contextId);
 const preview=useQuery({queryKey:['ai-context-preview',contextType,contextId],enabled:!!contextType&&valid,queryFn:()=>requests.get<{snapshot:string;digest:string}>('/ai/context?type='+contextType+'&targetId='+encodeURIComponent(contextId),opts),retry:false});
 useEffect(()=>{onConfirm('')},[contextType,contextId,preview.data?.digest,onConfirm]);
 useEffect(()=>{if(disabled&&details.current)details.current.open=false},[disabled]);
 const snapshot=preview.data?JSON.parse(preview.data.snapshot) as {asset?:Record<string,string|number>;session?:Record<string,string|null>}:{};
 const labels:Record<string,string>={name:'资产名称',host:'主机地址',port:'端口',protocol:'协议',state:'会话状态',createdAt:'创建时间',connectedAt:'连接时间',closedAt:'结束时间'};
 const fields=Object.entries({...snapshot.asset,...snapshot.session});
 return <div className="nt-ai-context"><Space wrap>
  <Select aria-label="分析范围" disabled={locked||disabled} style={{width:150}} value={contextType} onChange={contextType=>onChange({contextType,contextId:''})} options={[{value:'',label:'仅文本问答'},{value:'asset',label:'资产只读分析'},{value:'session',label:'会话摘要分析'}]}/>
  {contextType==='asset'&&!locked&&<AIAssetSelect value={contextId} onChange={contextId=>onChange({...scope,contextId})}/>}
  {contextType==='session'&&!locked&&<Input aria-label="会话 ID" placeholder="输入访问记录中的会话 ID" value={contextId} maxLength={36} onChange={e=>onChange({...scope,contextId:e.target.value.trim()})} style={{width:290,maxWidth:'100%'}}/>}
  {!!contextType&&<Button size="small" disabled={disabled||!valid} onClick={()=>{onConfirm('');void preview.refetch()}}>刷新预览</Button>}
 </Space>
 {contextType&&<>
  {preview.error&&<Alert type="warning" title={preview.error.message}/>}
  {preview.data&&!preview.error&&<><details ref={details}><summary>查看将发送给模型的字段</summary><dl>{fields.map(([key,value])=><div key={key}><dt>{labels[key]||key}</dt><dd>{value==null?'无记录':key.endsWith('At')?new Date(String(value)).toLocaleString():String(value)}</dd></div>)}</dl></details><Checkbox disabled={disabled||preview.isFetching} checked={digest===preview.data.digest} onChange={e=>onConfirm(e.target.checked?preview.data.digest:'')}>确认将上述字段发送给本对话的模型服务</Checkbox></>}
  <Typography.Paragraph type="secondary" style={{margin:'6px 0'}}>只读分析不执行命令，不读取密码、文件和命令原文。会话分析仅依据状态与时间；切换目标请新建对话。</Typography.Paragraph>
 </>}
 </div>;
}
