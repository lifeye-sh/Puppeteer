import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Form,Input,Popconfirm,Space,Table,Tag,Typography} from 'antd';
import Modal from './BackendFormModal';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';

export type Snippet={id:string;name:string;description:string;content:string;version:number;system:boolean;updatedAt:string};
export default function BackendSnippets({system=false,onInsert}:{system?:boolean;onInsert?:(text:string)=>void}){
 const {account}=useBackendRuntime();const client=useQueryClient();const [page,setPage]=useState(1);const [search,setSearch]=useState('');const [edit,setEdit]=useState<Snippet|null>();const [preview,setPreview]=useState<Snippet>();const [error,setError]=useState('');const [busy,setBusy]=useState(false);const [form]=Form.useForm();
 const scope=onInsert?'all':system?'system':'mine';const path=system?'/admin/snippets':'/snippets';const canEdit=!onInsert&&(!system||account.actions?.includes('snippet.manage'));
 const list=useQuery({queryKey:['r10-snippets',scope,page,search],queryFn:()=>requests.get<{items:Snippet[];total:number}>(`/snippets?scope=${scope}&page=${page}&search=${encodeURIComponent(search)}`,{errorMode:'local'})});
 const run=async(fn:()=>Promise<unknown>)=>{setBusy(true);setError('');try{await fn();setEdit(undefined);await client.invalidateQueries({queryKey:['r10-snippets']})}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const open=(v:Snippet|null)=>{setError('');form.resetFields();form.setFieldsValue(v??{name:'',description:'',content:''});setEdit(v)};
 return <section className={onInsert?'':'nt-page'}>
  {!onInsert&&<Typography.Title level={2}>{system?'系统命令片段':'我的命令片段'}</Typography.Title>}
  <Alert type="info" showIcon title={onInsert?'插入只填入命令编辑框，不发送到远端。请检查内容后点击执行。':'片段仅保存命令文本，不会自动执行。请勿保存密码、令牌或私钥。系统片段对所有登录用户可见。'} style={{marginBottom:16}}/>
  {(error||list.error)&&<Alert type="error" showIcon title={error||(list.error as Error).message}/>}
  <Space wrap style={{marginBottom:16}}><Input.Search aria-label="搜索命令片段" placeholder="搜索名称或说明" allowClear onSearch={v=>{setSearch(v);setPage(1)}} style={{maxWidth:'100%',width:280}}/>{canEdit&&<Button type="primary" onClick={()=>open(null)}>新建片段</Button>}<Button onClick={()=>void list.refetch()}>刷新</Button></Space>
  <Table<Snippet> rowKey="id" loading={list.isLoading} dataSource={list.data?.items} pagination={{current:page,pageSize:30,total:list.data?.total,onChange:setPage,showSizeChanger:false}} scroll={{x:600}} columns={[
   {title:'名称',dataIndex:'name',render:(v:string,row)=><Button type="link" onClick={()=>setPreview(row)}>{v}</Button>},
   {title:'说明',dataIndex:'description',ellipsis:true},
   {title:'来源',dataIndex:'system',render:(v:boolean)=><Tag color={v?'blue':'default'}>{v?'系统':'个人'}</Tag>},
   {title:'操作',render:(_,row)=><Space>{onInsert?<Button type="primary" onClick={()=>onInsert(row.content)}>插入命令框</Button>:canEdit&&<><Button onClick={()=>open(row)}>编辑</Button><Popconfirm title="删除此命令片段？" onConfirm={()=>run(()=>requests.delete(`${path}/${row.id}?version=${row.version}`,{errorMode:'local'}))}><Button danger disabled={busy}>删除</Button></Popconfirm></>}</Space>},
  ]}/>
  <Modal title={edit?'编辑命令片段':'新建命令片段'} open={edit!==undefined} confirmLoading={busy} onCancel={()=>setEdit(undefined)} onOk={async()=>{const v=await form.validateFields();await run(()=>requests[edit?'put':'post'](`${path}${edit?'/'+edit.id:''}`,{...v,version:edit?.version},{errorMode:'local'}))}}>
   {error&&<Alert type="error" title={error}/>}
   <Form form={form} layout="vertical"><Form.Item name="name" label="名称" rules={[{required:true,whitespace:true}]}><Input maxLength={120}/></Form.Item><Form.Item name="description" label="说明"><Input maxLength={1000}/></Form.Item><Form.Item name="content" label="命令内容" extra="保留换行。最多 4096 UTF-8 字节；执行前请检查目标资产与命令。" rules={[{required:true,whitespace:true},{validator:(_,v)=>!v||new TextEncoder().encode(v).length<=4096?Promise.resolve():Promise.reject(new Error('命令内容不能超过 4096 字节'))}]}><Input.TextArea rows={8} style={{fontFamily:'Consolas, monospace'}}/></Form.Item></Form>
  </Modal>
  <Modal title={preview?.name} open={!!preview} onCancel={()=>setPreview(undefined)} footer={onInsert?<Button type="primary" onClick={()=>{if(preview)onInsert(preview.content);setPreview(undefined)}}>插入命令框</Button>:null}><Typography.Paragraph>{preview?.description}</Typography.Paragraph><pre style={{whiteSpace:'pre-wrap',overflowWrap:'anywhere'}}>{preview?.content}</pre></Modal>
 </section>
}
