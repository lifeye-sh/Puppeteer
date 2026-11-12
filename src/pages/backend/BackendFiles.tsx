import {executionEntitlement} from './execution-entitlement';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal from './BackendFormModal';
import {useEffect,useRef,useState} from 'react';
import {Alert,Button,Input,Popconfirm,Progress,Space,Table,Tag,Typography} from 'antd';
import requests from '@/api/core/requests';

type Entry={name:string;size:number;directory:boolean;symlink:boolean;mode:string;modified:number};
type Listing={root:string;path:string;items:Entry[];maxFileBytes:number;readonly:boolean};
type Task={id:string;path:string;size:number;received:number;state:string;message:string;sha256:string;cleanupNeeded:boolean};
const join=(dir:string,name:string)=>dir?`${dir}/${name}`:name;
const states:Record<string,string>={receiving:'接收文件中',transferring:'写入远端并校验',committing:'提交目标中',succeeded:'已完成',failed:'失败',canceled:'已取消',interrupted:'已中断，需核对目标'};
export default function BackendFiles({sessionId,readonly}:{sessionId:string;readonly:boolean}){
 const {capabilities}=useBackendRuntime();const execution=executionEntitlement(capabilities.features,'files.write','文件写入');readonly=readonly||!execution.allowed;
 const [dir,setDir]=useState(''),[listing,setListing]=useState<Listing>(),[error,setError]=useState(''),[loading,setLoading]=useState(false),[busy,setBusy]=useState(false);
 const [address,setAddress]=useState('/');
 const [filenameFilter,setFilenameFilter]=useState(''),[page,setPage]=useState(1);
 const filteredItems=(listing?.items??[]).filter(item=>item.name.toLocaleLowerCase().includes(filenameFilter.trim().toLocaleLowerCase()));
 useEffect(()=>{setFilenameFilter('');setPage(1)},[dir,sessionId]);
 useEffect(()=>{setPage(p=>Math.min(p,Math.max(1,Math.ceil(filteredItems.length/15))))},[filteredItems.length]);
 const [task,setTask]=useState<Task>(),[phase,setPhase]=useState(''),[progress,setProgress]=useState(0);
 const [editor,setEditor]=useState<{path:string;hash:string;content:string}>(),[editBusy,setEditBusy]=useState(false);
 const [action,setAction]=useState<{kind:string;path:string;name:string}>();
 const input=useRef<HTMLInputElement>(null),cancelled=useRef(false),activeTask=useRef(''),mounted=useRef(true);
 const base=`/sessions/${sessionId}`;
 const refresh=async()=>{setLoading(true);setError('');try{const data=await requests.get<Listing>(`${base}/files?path=${encodeURIComponent(dir)}`,{errorMode:'local'});if(mounted.current)setListing(data)}catch(e){if(mounted.current){setListing(undefined);setError((e as Error).message)}}finally{if(mounted.current)setLoading(false)}};
 useEffect(()=>{mounted.current=true;return ()=>{mounted.current=false;cancelled.current=true;if(activeTask.current)void requests.post(`/uploads/${activeTask.current}/cancel`,undefined,{errorMode:'local'}).catch(()=>{})}},[]);
 // Directory changes trigger a real authorized SFTP listing.
 useEffect(()=>{void refresh()},[dir,sessionId]);
 useEffect(()=>{setAddress(`/${dir}`)},[dir]);
 const navigate=(target:string)=>{
  if(busy||loading)return;
  setAddress(`/${target}`);
  if(target===dir){void refresh();return}
  setListing(undefined);setError('');setDir(target);
 };
 const goToAddress=()=>{
  if(busy||loading)return;
  if(!address.startsWith('/')||/[\\\x00\r\n]/.test(address)||address.split('/').includes('..')){setError('请输入以 / 开头的绝对目录地址，不支持 .. 或反斜杠。');return}
  navigate(address.split('/').filter(part=>part&&part!=='.').join('/'));
 };
 const status=async(id:string)=>{const value=await requests.get<Task>(`/uploads/${id}`,{errorMode:'local'});if(mounted.current)setTask(value);return value};
 const cancel=async()=>{cancelled.current=true;try{if(activeTask.current){const result=await requests.post<Task>(`/uploads/${activeTask.current}/cancel`,undefined,{errorMode:'local'});if(mounted.current)setTask(result)}}catch(e){if(mounted.current)setError((e as Error).message)}};
 const transfer=async(file:Blob,path:string,expectedHash='')=>{
  if(readonly||busy)return;setBusy(true);setError('');setTask(undefined);setProgress(0);setPhase('准备传输');cancelled.current=false;
  const id=crypto.randomUUID();activeTask.current=id;
  try{
   if(file.size>16*1024*1024)throw new Error('单文件上限 16 MiB');
   const initial=await requests.post<Task>(`${base}/uploads`,{id,path,size:file.size,expectedHash},{errorMode:'local'});if(mounted.current)setTask(initial);
   for(let offset=0;offset<file.size;offset+=1024*1024){
    if(cancelled.current)throw new Error('已取消');if(mounted.current)setPhase('浏览器上传 / 后端接收');
    const response=await fetch(`/api/uploads/${id}/chunks?offset=${offset}`,{method:'PUT',credentials:'same-origin',headers:{'Content-Type':'application/octet-stream'},body:file.slice(offset,offset+1024*1024)});
    if(!response.ok){const failure=await response.json();throw new Error(failure.message??'分片上传失败')}
    if(mounted.current)setProgress(Math.round(Math.min(offset+1024*1024,file.size)/Math.max(1,file.size)*100));
   }
   if(cancelled.current)throw new Error('已取消');if(mounted.current)setPhase('后端传输至目标 / 校验内容');
   const poll=window.setInterval(()=>void status(id).catch(()=>{}),1000);
   try{const result=await requests.post<Task>(`/uploads/${id}/commit`,undefined,{errorMode:'local'});if(mounted.current){setTask(result);setPhase(result.message);setProgress(100)};if(result.state!=='succeeded')throw new Error(result.message)}finally{clearInterval(poll)}
   if(mounted.current){setEditor(undefined);await refresh()}
  }catch(e){if(mounted.current){setError((e as Error).message);setPhase('未确认完成');try{const current=await status(id);if(current.state==='receiving')await cancel();if(current.state==='canceled'||cancelled.current)setPhase('已取消，暂存空间已释放')}catch{/* task may not have been created */}await refresh().catch(()=>{})}}
  finally{if(cancelled.current)void requests.post(`/uploads/${id}/cancel`,undefined,{errorMode:'local'}).catch(()=>{});activeTask.current='';if(mounted.current)setBusy(false)}
 };
 const download=async(entry:Entry)=>{setError('');try{const response=await fetch(`/api${base}/files/content?path=${encodeURIComponent(join(dir,entry.name))}`,{credentials:'same-origin'});if(!response.ok){const body=await response.json();throw new Error(body.message??'下载失败')};const url=URL.createObjectURL(await response.blob());const a=document.createElement('a');a.href=url;a.download=entry.name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}catch(e){setError((e as Error).message)}};
 const edit=async(entry:Entry)=>{setEditBusy(true);setError('');try{const path=join(dir,entry.name);const data=await requests.get<{content:string;sha256:string}>(`${base}/files/content?text=true&path=${encodeURIComponent(path)}`,{errorMode:'local'});setEditor({path,hash:data.sha256,content:data.content})}catch(e){setError((e as Error).message)}finally{setEditBusy(false)}};
 const mutate=async(kind:string,path:string,destination='')=>{setBusy(true);setError('');try{await requests.post(`${base}/files/actions`,{action:kind,path,destination:kind==='chmod'?'':destination,mode:kind==='chmod'?destination:undefined},{errorMode:'local'});setAction(undefined);await refresh()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const blocked=busy||readonly||loading||!listing;
 return <div>{!execution.allowed&&<Alert type="warning" title={execution.message+' 可继续浏览和下载已有文件。'}/>}
  <Alert type="info" showIcon title={`SFTP · ${listing?.root??'读取系统根目录中'}${readonly?' · 只读':''}`} description="可浏览系统根目录，读写权限由远端登录用户决定；单文件 16 MiB，文本编辑 256 KiB；符号链接不允许访问。新上传不覆盖同名文件；编辑保存会校验原内容。"/>
  {error&&<Alert style={{marginTop:10}} type="error" title={error} closable onClose={()=>setError('')}/>}
  <div style={{display:'flex',gap:8,marginTop:14,alignItems:'center'}}><label htmlFor={`directory-address-${sessionId}`} style={{whiteSpace:'nowrap'}}>目录地址</label><Input id={`directory-address-${sessionId}`} aria-label="目录地址" placeholder="输入绝对路径，例如 /var/log" value={address} disabled={busy||loading} onChange={e=>setAddress(e.target.value)} onPressEnter={e=>{if(!e.nativeEvent.isComposing)goToAddress()}}/><Button type="primary" disabled={busy||loading} onClick={goToAddress}>跳转</Button></div>
  <Space wrap style={{margin:'14px 0'}}><Button disabled={!dir||busy||loading} onClick={()=>navigate('')}>系统根目录</Button><Button disabled={!dir||busy||loading} onClick={()=>navigate(dir.split('/').slice(0,-1).join('/'))}>上级目录</Button><Typography.Text code>/{dir}</Typography.Text><Button loading={loading} disabled={busy||loading} onClick={()=>void refresh()}>刷新</Button><Button disabled={blocked} onClick={()=>setAction({kind:'mkdir',path:dir,name:''})}>新建目录</Button><Button type="primary" disabled={blocked} onClick={()=>input.current?.click()}>上传文件</Button><a href="/filesystem-log" target="_blank" rel="noopener noreferrer">文件审计</a></Space>
  <input ref={input} type="file" aria-label="选择上传文件" style={{display:'none'}} onChange={e=>{const file=e.target.files?.[0];if(file)void transfer(file,join(dir,file.name));e.target.value=''}}/>
  {(busy||task)&&<div style={{marginBottom:12}}><Space wrap><Tag color={task?.state==='succeeded'?'green':'blue'}>{task?states[task.state]??task.state:phase}</Tag><span>{task?.path}</span>{busy&&<Button danger disabled={task?.state==='committing'} onClick={()=>void cancel()}>取消传输</Button>}{task&&<Button onClick={()=>void status(task.id).catch(e=>setError(e.message))}>核对结果</Button>}</Space><Progress percent={progress} status={task?.state==='succeeded'?'success':task?.state==='failed'||task?.state==='interrupted'?'exception':'normal'}/><Typography.Text type="secondary">{phase}；浏览器上传 100% 后仍需等待远端完成。</Typography.Text>{task?.sha256&&<div style={{overflowWrap:'anywhere'}}>SHA256：{task.sha256}</div>}</div>}
  <Space wrap style={{marginBottom:12}}><Input aria-label="SFTP 文件名过滤" placeholder="按文件名过滤当前目录" allowClear value={filenameFilter} onChange={e=>{setFilenameFilter(e.target.value);setPage(1)}} style={{width:280,maxWidth:'100%'}}/><Typography.Text type="secondary">{filteredItems.length} / {listing?.items.length??0} 项 · 不区分大小写</Typography.Text></Space>
  <Table<Entry> size="small" rowKey="name" loading={loading} dataSource={filteredItems} locale={{emptyText:filenameFilter.trim()?'没有匹配的文件或文件夹':'此目录为空'}} pagination={{current:page,onChange:setPage,pageSize:15,showSizeChanger:false}} scroll={{x:800}} columns={[
   {title:'名称',render:(_,v)=>v.directory&&!v.symlink?<Button type="link" disabled={busy||loading} onClick={()=>navigate(join(dir,v.name))}>📁 {v.name}</Button>:<span>{v.symlink?'↗ ':''}{v.name}</span>},
   {title:'大小',render:(_,v)=>v.directory?'—':`${v.size} B`},{title:'权限',dataIndex:'mode'},{title:'修改时间',render:(_,v)=>new Date(v.modified*1000).toLocaleString()},
   {title:'操作',render:(_,v)=>v.symlink?<Tag>链接已阻止</Tag>:<Space wrap>{!v.directory&&<><Button size="small" disabled={busy||v.size>16*1024*1024} onClick={()=>void download(v)}>下载</Button><Button size="small" disabled={blocked||editBusy||v.size>256*1024} onClick={()=>void edit(v)}>编辑</Button></>}<Button size="small" disabled={blocked} onClick={()=>setAction({kind:'rename',path:join(dir,v.name),name:v.name})}>重命名</Button><Button size="small" disabled={blocked} onClick={()=>setAction({kind:'chmod',path:join(dir,v.name),name:'644'})}>权限</Button><Popconfirm title={v.directory?'删除此空目录？':'删除此文件？'} onConfirm={()=>mutate('delete',join(dir,v.name))}><Button size="small" danger disabled={blocked}>删除</Button></Popconfirm></Space>}
  ]}/>
  <Modal title={action?.kind==='chmod'?'修改权限（3 位八进制，例如 644 / 755）':action?.kind==='mkdir'?'新建目录':'重命名'} open={!!action} confirmLoading={busy} onCancel={()=>setAction(undefined)} onOk={()=>{if(!action)return;if(action.kind==='chmod'){if(!/^[0-7]{3}$/.test(action.name)){setError('权限需为 3 位八进制');return};void mutate('chmod',action.path,action.name);return};if(!action.name.trim()||/[\\/]/.test(action.name)||action.name==='..'){setError('请输入有效名称');return};void mutate(action.kind,action.kind==='mkdir'?join(action.path,action.name):action.path,action.kind==='rename'?join(dir,action.name):'')}}><Input aria-label="文件或目录名称" value={action?.name} onChange={e=>setAction(v=>v?{...v,name:e.target.value}:v)}/></Modal>
  <Modal title={`编辑：${editor?.path??''}`} open={!!editor} width={900} confirmLoading={busy} onCancel={()=>{if(!busy)setEditor(undefined)}} onOk={()=>{if(editor){const blob=new Blob([editor.content],{type:'text/plain;charset=utf-8'});if(blob.size>256*1024){setError('编辑内容超过 256 KiB');return};void transfer(blob,editor.path,editor.hash)}}} okText="保存并校验"><Input.TextArea aria-label="文件内容" rows={18} value={editor?.content} disabled={busy} onChange={e=>setEditor(v=>v?{...v,content:e.target.value}:v)}/></Modal>
 </div>;
}
