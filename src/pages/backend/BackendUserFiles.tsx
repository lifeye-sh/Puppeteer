import {executionEntitlement} from './execution-entitlement';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal from './BackendFormModal';
import {useEffect,useRef,useState} from 'react';
import {Alert,Button,Input,Progress,Popconfirm,Space,Table} from 'antd';
import requests from '@/api/core/requests';
type UploadTask={id:string;name:string;size:number;received:number;parentId:number;state:string;expiresAt:string};
type Node={id:number;parentId:number;name:string;directory:boolean;size:number;unavailable?:boolean};
type Data={spaceId:string;writable:boolean;download:boolean;rootId:number;folder:Node;items:Node[];usage:{quotaBytes:number;usedBytes:number};rdpMounted:boolean};
const bytes=(n:number)=>`${(n/1024/1024/1024).toFixed(2)} GiB`;
export default function BackendUserFiles({spaceId,sessionId}:{spaceId?:string;sessionId?:string}){
 const {capabilities}=useBackendRuntime();const execution=executionEntitlement(capabilities.features,'files.write','文件写入');
 const scope=`${spaceId?`&space=${encodeURIComponent(spaceId)}`:""}${sessionId?`&session=${encodeURIComponent(sessionId)}`:""}`;
 const uploadRequest=useRef<AbortController|null>(null),stopped=useRef(false),cancelRequested=useRef(false);
 const [tasks,setTasks]=useState<UploadTask[]>([]),[resumeTask,setResumeTask]=useState<UploadTask>();
 const [uploadFile,setUploadFile]=useState<File>(),[progress,setProgress]=useState(0),[uploadState,setUploadState]=useState('');
 const [rename,setRename]=useState<Node>(),[renameName,setRenameName]=useState(''),[editor,setEditor]=useState<{node:Node;text:string;modifiedAt:string}>();
 useEffect(()=>()=>{stopped.current=true;uploadRequest.current?.abort()},[]);
 const [parent,setParent]=useState<number>(),[data,setData]=useState<Data>(),[error,setError]=useState(''),[busy,setBusy]=useState(false),[name,setName]=useState('');
 const [creating,setCreating]=useState(false),[nameError,setNameError]=useState('');
 const [filenameFilter,setFilenameFilter]=useState(''),[page,setPage]=useState(1);
 const filteredItems=(data?.items??[]).filter(item=>item.name.toLocaleLowerCase().includes(filenameFilter.trim().toLocaleLowerCase()));
 useEffect(()=>{setFilenameFilter('');setPage(1);setCreating(false);setName('');setNameError('')},[parent]);
 useEffect(()=>{setPage(p=>Math.min(p,Math.max(1,Math.ceil(filteredItems.length/8))))},[filteredItems.length]);
 const load=async()=>{try{setData(await requests.get<Data>(`/storage?${parent?`parent=${parent}`:''}${scope}`,{errorMode:'local'}));setTasks(await requests.get<UploadTask[]>(`/storage/uploads?${scope}`,{errorMode:'local'}))}catch(e){setError((e as Error).message)}};
 useEffect(()=>{void load()},[parent]);
 const run=async(fn:()=>Promise<unknown>)=>{setError('');setBusy(true);try{await fn();await load()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const createFolder=()=>{
  if(busy||!data)return;
  const folderName=name.trim();
  if(!folderName){setNameError('请输入目录名称');return}
  if(folderName==='.'||folderName==='..'||/[\\/\x00:\r\n]/.test(folderName)||new TextEncoder().encode(folderName).length>255){setNameError('目录名称无效：不能包含路径分隔符，且不能超过 255 字节');return}
  setNameError('');
  void run(async()=>{await requests.post(`/storage/folders?${scope}`,{parentId:data.folder.id,name:folderName},{errorMode:'local'});setName('');setCreating(false);setFilenameFilter('');setPage(1)});
 };
 const upload=(file:File,existing?:UploadTask)=>run(async()=>{
  if(existing&&(file.name!==existing.name||file.size!==existing.size))throw Error('请选择原始同名、同大小文件；已有分块会逐一核验');
  if(file.size>1073741824)throw Error('单文件最大 1 GiB');
  stopped.current=false;cancelRequested.current=false;setUploadFile(file);setProgress(0);setUploadState(existing?'校验并继续上传':'上传中');
  let task=existing;
  try{
   if(!task)task=await requests.post<UploadTask>(`/storage/uploads?${scope}`,{parentId:data!.folder.id,name:file.name,size:file.size},{errorMode:'local'});
   setResumeTask(task);
   const status=await requests.get<UploadTask>(`/storage/uploads/${task.id}?${scope}`,{errorMode:'local'});
   if(status.state!=='complete'){
    for(let offset=0;offset<file.size;offset+=1048576){
     if(stopped.current)throw Error('上传已暂停');
     const controller=new AbortController();uploadRequest.current=controller;
     const r=await fetch(`/api/storage/uploads/${task.id}/parts?offset=${offset}${scope}`,{method:'PUT',body:file.slice(offset,offset+1048576),signal:controller.signal,headers:{'Content-Type':'application/octet-stream'}});
     if(!r.ok){const body=await r.json().catch(()=>({}));throw Error(body.message||`分块上传失败 (${r.status})`)}
     setProgress(Math.round(100*Math.min(file.size,offset+1048576)/file.size));
    }
    if(stopped.current)throw Error('上传已暂停');
    await requests.post(`/storage/uploads/${task.id}/complete?${scope}`,undefined,{errorMode:'local'});
   }
   setProgress(100);setUploadState('完成');setUploadFile(undefined);setResumeTask(undefined);
  }catch(e){
   if(cancelRequested.current&&task){await requests.delete(`/storage/uploads/${task.id}`,{errorMode:'local'});setUploadState('已取消，预留容量已释放');setUploadFile(undefined);setResumeTask(undefined)}
   else{setUploadState('未完成，可继续上传');throw Error(stopped.current?'上传已暂停，任务保留 24 小时，可选择原文件继续':(e as Error).message)}
  }finally{uploadRequest.current=null;await load()}
 });
 const openEditor=(node:Node)=>run(async()=>{const v=await requests.get<{text:string;modifiedAt:string}>(`/storage/files/${node.id}/text?${scope}`,{errorMode:'local'});setEditor({node,...v})});
 return <Space orientation="vertical" style={{width:'100%'}}>{!execution.allowed&&<Alert type="warning" title={execution.message+' 可继续浏览和下载已有文件。'}/>}
  <Alert type="info" title="我的文件" description={data?.rdpMounted?'RDP 连接后，在 Windows“此电脑”中打开 MyZone 共享盘。网页与远端使用本会话绑定的存储空间；修改后点击刷新。':'用户独立存储，可在网页上传和下载。RDP 共享盘尚未启用。'}/>
  {data&&<span>已用 {bytes(data.usage.usedBytes)} / {bytes(data.usage.quotaBytes)} · 单文件最大 1 GiB</span>}
  {uploadState&&<div aria-live="polite">{uploadState} · {uploadFile?.name}<Progress percent={progress} status={uploadState.startsWith('未完成')?'exception':uploadState==='完成'?'success':'active'}/>{busy&&['上传中','校验并继续上传'].includes(uploadState)&&<Space><Button onClick={()=>{stopped.current=true;uploadRequest.current?.abort()}}>暂停上传</Button><Button onClick={()=>{cancelRequested.current=true;stopped.current=true;uploadRequest.current?.abort()}}>取消上传</Button></Space>}{!busy&&uploadFile&&<Button onClick={()=>void upload(uploadFile,resumeTask)}>继续上传</Button>}</div>}
  {tasks.length>0&&<div><strong>未完成上传（容量已预留，24 小时到期自动回收）</strong>{tasks.map(t=><div key={t.id} style={{marginBlock:8}}>{t.name} · {t.received}/{t.size} 字节 <input aria-label={`继续上传 ${t.name}`} type="file" disabled={busy||(!data?.writable||!execution.allowed)} onChange={e=>{const f=e.target.files?.[0];e.target.value='';if(f)void upload(f,t)}}/><Button disabled={busy} onClick={()=>void run(()=>requests.delete(`/storage/uploads/${t.id}`,{errorMode:'local'}))}>取消任务</Button></div>)}<small>重新选择原文件后逐块验证已有内容；完整提交前不会出现在文件目录。</small></div>}
  {error&&<Alert type="error" title={error}/>}
  <Space wrap><Button disabled={busy||!data?.folder.parentId} onClick={()=>setParent(data!.folder.parentId)}>上一级</Button><Button disabled={busy} onClick={()=>void load()}>刷新</Button><input aria-label="上传文件" type="file" disabled={busy||(!data?.writable||!execution.allowed)} onChange={e=>{const f=e.target.files?.[0];e.target.value='';if(f)void upload(f)}}/></Space>
  <Button disabled={busy||(!data?.writable||!execution.allowed)} onClick={()=>{setCreating(true);setNameError('')}}>新建目录</Button>
  {creating&&<div><label htmlFor="rdp-new-folder-name">目录名称</label><Space.Compact style={{width:'100%',marginTop:4}}><Input id="rdp-new-folder-name" aria-label="新文件夹名称" placeholder="请输入目录名称" autoFocus value={name} disabled={busy} status={nameError?'error':undefined} onChange={e=>{setName(e.target.value);setNameError('')}} onPressEnter={e=>{if(!e.nativeEvent.isComposing)createFolder()}} maxLength={255}/><Button type="primary" loading={busy} onClick={createFolder}>创建</Button><Button disabled={busy} onClick={()=>{setCreating(false);setName('');setNameError('')}}>取消</Button></Space.Compact>{nameError&&<div role="alert" style={{color:'#ff4d4f',marginTop:4}}>{nameError}</div>}</div>}
  <Input aria-label="RDP 文件名过滤" placeholder="按文件名过滤当前目录" allowClear value={filenameFilter} onChange={e=>{setFilenameFilter(e.target.value);setPage(1)}}/>
  <span style={{color:'#64748b'}}>{filteredItems.length} / {data?.items.length??0} 项 · 不区分大小写</span>
  <Table<Node> size="small" rowKey="id" loading={busy} dataSource={filteredItems} locale={{emptyText:filenameFilter.trim()?'没有匹配的文件或文件夹':'此目录为空'}} pagination={{current:page,onChange:setPage,pageSize:8,showSizeChanger:false}} columns={[
   {title:'名称',dataIndex:'name',render:(v:string,n:Node)=>n.directory?<Button type="link" onClick={()=>setParent(n.id)}>📁 {v}</Button>:<span>{v}{n.unavailable&&<small style={{color:"#b45309"}}> · 备份未包含内容</small>}</span>},
   {title:'大小',render:(_,n)=>n.directory?'—':`${(n.size/1024).toFixed(1)} KiB`},
   {title:'操作',render:(_,n)=><Space>{!n.directory&&!n.unavailable&&data?.download&&<a href={`/api/storage/files/${n.id}?${scope}`} download>下载</a>}<Button size="small" disabled={busy||(!data?.writable||!execution.allowed)} onClick={()=>{setRename(n);setRenameName(n.name)}}>重命名</Button>{!n.directory&&!n.unavailable&&n.size<=1048576&&<Button size="small" disabled={busy||!data?.download} onClick={()=>void openEditor(n)}>文本编辑</Button>}<Popconfirm title="删除此文件或空目录？" getPopupContainer={trigger=>trigger.parentElement!} onConfirm={()=>run(()=>requests.delete(`/storage/files/${n.id}?${scope}`,{errorMode:'local'}))}><Button size="small" danger disabled={busy||(!data?.writable||!execution.allowed)}>删除</Button></Popconfirm></Space>}
  ]}/>
 <Modal title="重命名" open={!!rename} confirmLoading={busy} getContainer={false} onCancel={()=>setRename(undefined)} onOk={()=>void run(async()=>{await requests.put(`/storage/files/${rename!.id}/name?${scope}`,{name:renameName},{errorMode:'local'});setRename(undefined)})}>{error&&<Alert type="error" title={error}/>}<Input aria-label="新文件名" value={renameName} onChange={e=>setRenameName(e.target.value)}/></Modal>
 <Modal title={`文本编辑 · ${editor?.node.name??''}`} open={!!editor} confirmLoading={busy} getContainer={false} width={760} okButtonProps={{disabled:(!data?.writable||!execution.allowed)}} onCancel={()=>setEditor(undefined)} onOk={()=>void run(async()=>{await requests.put(`/storage/files/${editor!.node.id}/text?${scope}`,{text:editor!.text,modifiedAt:editor!.modifiedAt},{errorMode:'local'});setEditor(undefined)})}><p>仅支持 1 MiB 以内 UTF-8 文本；远端同时修改时会拒绝覆盖。</p><Input.TextArea aria-label="文件文本" rows={16} value={editor?.text} readOnly={(!data?.writable||!execution.allowed)} onChange={e=>setEditor(v=>v?{...v,text:e.target.value}:v)}/>{error&&<Alert type="error" title={error}/>}</Modal>
 </Space>;
}
