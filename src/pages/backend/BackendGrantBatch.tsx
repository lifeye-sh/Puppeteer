import {Alert,Button,Form,Input,Segmented,Select,Table,Tag,Typography} from 'antd';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';
import {grantBatchCount,grantBatchRequest,MAX_BATCH_GRANTS,type GrantBatchForm} from './grant-batch-model';

type Named={id:string;name:string};
type Item={id?:string;userId?:string;departmentId?:string;resourceId:string;status:'created'|'skipped'};
type Result={total:number;created:number;skipped:number;items:Item[]};

export default function BackendGrantBatch(){
 const client=useQueryClient();const [open,setOpen]=useState(false);const [busy,setBusy]=useState(false);const [error,setError]=useState('');
 const [direction,setDirection]=useState('subjects');const [userSearch,setUserSearch]=useState('');const [resourceSearch,setResourceSearch]=useState('');
 const [result,setResult]=useState<Result>();const [resultNames,setResultNames]=useState<Record<string,string>>({});const [form]=Form.useForm<GrantBatchForm>();
 const values=Form.useWatch([],form) as GrantBatchForm|undefined;const count=grantBatchCount(values??{});
 const users=useQuery({queryKey:['r03-batch-users',userSearch],enabled:open,queryFn:()=>requests.get<{items:{id:string;username:string}[]}>('/admin/users/paging?pageSize=100&search='+encodeURIComponent(userSearch),{errorMode:'local'})});
 const departments=useQuery({queryKey:['r03-batch-departments'],enabled:open,queryFn:()=>requests.get<Named[]>('/admin/departments',{errorMode:'local'})});
 const assets=useQuery({queryKey:['r03-batch-assets',resourceSearch],enabled:open,queryFn:()=>requests.get<{items:Named[]}>('/admin/assets/paging?pageSize=100&search='+encodeURIComponent(resourceSearch),{errorMode:'local'})});
 const groups=useQuery({queryKey:['r03-batch-groups'],enabled:open,queryFn:()=>requests.get<Named[]>('/admin/resource-groups',{errorMode:'local'})});
 const websites=useQuery({queryKey:['r03-batch-websites'],enabled:open,queryFn:()=>requests.get<Named[]>('/admin/websites',{errorMode:'local'})});
 const databases=useQuery({queryKey:['r03-batch-databases'],enabled:open,queryFn:()=>requests.get<{items:Named[]}>('/admin/database-assets',{errorMode:'local'})});
 const strategies=useQuery({queryKey:['r03-batch-strategies'],enabled:open,queryFn:()=>requests.get<Named[]>('/admin/policies/strategy',{errorMode:'local'})});
 const filters=useQuery({queryKey:['r03-batch-filters'],enabled:open,queryFn:()=>requests.get<Named[]>('/admin/policies/command-filter',{errorMode:'local'})});
 const match=(v:Named)=>v.name.toLowerCase().includes(resourceSearch.toLowerCase());
 const options=[...(assets.data?.items??[]).map(v=>({value:v.id,label:'资产：'+v.name})),
  ...(groups.data??[]).filter(match).map(v=>({value:v.id,label:'分组：'+v.name})),
  ...(websites.data??[]).filter(match).map(v=>({value:v.id,label:'Web：'+v.name})),
  ...(databases.data?.items??[]).filter(match).map(v=>({value:v.id,label:'数据库：'+v.name}))];
 const loadError=[users,departments,assets,groups,websites,databases,strategies,filters].find(query=>query.error)?.error?.message;
 const submit=async()=>{
  const input=await form.validateFields();setBusy(true);setError('');
  try {
   const body=grantBatchRequest(input);
   const saved=await requests.post<Result>('/admin/grants/batch',body,{errorMode:'local'});
   setResultNames(Object.fromEntries([...(input.users??[]),...(input.departments??[]),...(input.resources??[])].map(v=>[v.value.toLowerCase(),v.label])));
   setResult(saved);
   await client.invalidateQueries({queryKey:['r03-grants']});
  } catch(e){setError((e as Error).message)} finally{setBusy(false)}
 };
 const names=new Map(Object.entries(resultNames));
 const subjects=<div key="subjects" className="nt-form-grid">
  <Form.Item name="users" label="用户（可多选）"><Select mode="multiple" labelInValue showSearch filterOption={false} onSearch={setUserSearch} loading={users.isFetching} maxTagCount="responsive" placeholder="搜索并选择用户" options={users.data?.items.map(v=>({value:v.id,label:v.username}))}/></Form.Item>
  <Form.Item name="departments" label="部门（可多选）"><Select mode="multiple" labelInValue showSearch optionFilterProp="label" maxTagCount="responsive" placeholder="可与用户同时选择" loading={departments.isFetching} options={departments.data?.map(v=>({value:v.id,label:v.name}))}/></Form.Item>
 </div>;
 const resources=<Form.Item key="resources" name="resources" label="资产 / 分组（可多选）" rules={[{required:true,message:'请选择至少一个资产或分组'}]}><Select mode="multiple" labelInValue showSearch filterOption={false} onSearch={setResourceSearch} loading={assets.isFetching} maxTagCount="responsive" options={options} placeholder="搜索资产、分组、网站或数据库"/></Form.Item>;
 return <><Button type="primary" onClick={()=>{form.resetFields();setResult(undefined);setError('');setUserSearch('');setResourceSearch('');setOpen(true)}}>批量授权</Button>
 <Modal width={880} title="批量访问授权" open={open} onCancel={()=>{if(!busy)setOpen(false)}} onOk={result?()=>setOpen(false):submit} confirmLoading={busy} okText={result?'完成':busy?'正在校验并保存':'确认授权'} okButtonProps={{disabled:!result&&(count===0||count>MAX_BATCH_GRANTS||!!loadError)}} cancelButtonProps={{disabled:busy}} maskClosable={!busy} keyboard={!busy} forceRender>
  {(error||loadError)&&<Alert type="error" title={error||loadError} showIcon/>}
  {result?<><Alert type="success" showIcon title={`处理完成：新增 ${result.created} 条，跳过 ${result.skipped} 条`} description="已存在的授权保留原策略和有效期；需要调整时请编辑原授权。"/><Table<Item> size="small" rowKey={v=>`${v.userId||v.departmentId}:${v.resourceId}`} dataSource={result.items} pagination={{pageSize:10}} columns={[
   {title:'主体',render:(_,v)=>`${v.userId?'用户':'部门'}：${names.get(v.userId||v.departmentId||'')||v.userId||v.departmentId}`},
   {title:'资源',render:(_,v)=>names.get(v.resourceId)||v.resourceId},
   {title:'结果',render:(_,v)=><Tag color={v.status==='created'?'success':'default'}>{v.status==='created'?'已授权':'已有授权，已跳过'}</Tag>}
  ]}/></>:<>
   <Segmented value={direction} onChange={setDirection} options={[{value:'subjects',label:'为用户 / 部门选择资源'},{value:'resources',label:'为资产 / 分组选择人员'}]} style={{marginBottom:16}}/>
   <Form form={form} layout="vertical" disabled={busy}>
    {direction==='subjects'?[subjects,resources]:[resources,subjects]}
    <Alert type={count>MAX_BATCH_GRANTS?'error':'info'} showIcon title={`将处理 ${values?.users?.length??0} 个用户、${values?.departments?.length??0} 个部门与 ${values?.resources?.length??0} 个资源，共 ${count} 条授权关系`} description={`每个所选主体将获得每个所选资源的授权，单批最多 ${MAX_BATCH_GRANTS} 条。已有授权（包括已到期但未撤销的授权）会跳过；校验失败则整批不写入。`}/>
    <div className="nt-form-grid" style={{marginTop:16}}>
     <Form.Item name="strategyId" label="动作策略"><Select allowClear placeholder="沿用默认策略" options={strategies.data?.map(v=>({value:v.id,label:v.name}))}/></Form.Item>
     <Form.Item name="commandFilterId" label="SSH 命令过滤（仅显式执行）"><Select allowClear options={filters.data?.map(v=>({value:v.id,label:v.name}))}/></Form.Item>
     <Form.Item name="validFrom" label="开始时间（留空立即）"><Input type="datetime-local"/></Form.Item>
     <Form.Item name="expiresAt" label="到期时间（留空长期）"><Input type="datetime-local"/></Form.Item>
    </div>
    <Typography.Paragraph type="secondary">动作策略和命令过滤仅应用于非数据库资源；数据库保持固定只读策略，写操作仍需原有审批与权限。分组授权动态覆盖子分组，部门授权覆盖直接所属成员。可连续搜索并选择更多对象，每次最多显示 100 个匹配用户或资产。</Typography.Paragraph>
   </Form>
  </>}
 </Modal></>;
}
