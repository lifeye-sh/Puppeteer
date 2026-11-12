import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Card,Form,Input,InputNumber,Select,Space,Table,Tabs,Typography} from 'antd';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal from './BackendFormModal';
type Category={id:string;key:string;label:string};
type Config={revision:number;days:Record<string,number>;categories:Category[]};
type Preview={revision:number;items:{category:string;key:string;label:string;days:number;eligible:number;held:number}[]};
type Hold={category:string;recordId:string;reason:string;createdAt:string};
type HoldForm={category:string;recordId:string;reason:string;held:boolean};
const labelDays=(days:number,category?:string)=>days===-1?(category==='session'?'跟随访问记录':'永久保留'):`${days} 天`;
export default function BackendAuditRetention(){
 const {account}=useBackendRuntime(),cache=useQueryClient();const canEdit=!!account.actions?.includes('identity.manage');
 const config=useQuery({queryKey:['audit-retention'],queryFn:()=>requests.get<Config>('/admin/audit-retention',{errorMode:'local'})});
 const [draft,setDraft]=useState<Record<string,number>>({}),[preview,setPreview]=useState<Preview>(),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('');
 const [holdPage,setHoldPage]=useState(1),[hold,setHold]=useState<HoldForm>(),[form]=Form.useForm<HoldForm>();
 const holds=useQuery({queryKey:['audit-holds',holdPage],enabled:canEdit,queryFn:()=>requests.get<{items:Hold[];total:number}>(`/admin/audit-holds?page=${holdPage}`,{errorMode:'local'})});
 const refresh=async()=>{await Promise.all([cache.invalidateQueries({queryKey:['audit-retention']}),cache.invalidateQueries({queryKey:['configuration-current']}),cache.invalidateQueries({queryKey:['backend-settings']})])};
 const run=async(fn:()=>Promise<void>)=>{setBusy(true);setError('');setNotice('');try{await fn()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const prepare=()=>run(async()=>{if(!config.data)return;setPreview(await requests.post<Preview>('/admin/audit-retention/preview',{revision:config.data.revision,days:{...config.data.days,...draft}},{errorMode:'local'}))});
 const save=()=>run(async()=>{if(!preview)return;const r=await fetch('/api/settings',{method:'PATCH',headers:{'Content-Type':'application/json','If-Match':`"settings-${preview.revision}"`},body:JSON.stringify({set:Object.fromEntries(preview.items.map(p=>[p.key,p.days]))})});if(!r.ok){const body=await r.json();throw Error(body.message??'保存失败，请刷新配置重新预览')}setPreview(undefined);setDraft({});await refresh();setNotice('保留策略已保存，下一轮清理任务按新版本执行。')});
 const editHold=(value:HoldForm)=>{setError('');form.setFieldsValue(value);setHold(value)};
 const saveHold=()=>run(async()=>{const value=await form.validateFields();await requests.post('/admin/audit-holds',{...value,held:hold!.held},{errorMode:'local'});setHold(undefined);await holds.refetch();setNotice('保全状态已保存。')});
 const policies=<Space orientation="vertical" style={{width:'100%'}}>
  <Typography.Paragraph type="secondary">访问记录默认 31 天。会话内容可设置更短期限，先清理录屏、命令与转发明细，保留访问摘要；最终保留期不超过访问记录期限。会话内容选择“跟随访问记录”表示不提前清理，随访问记录到期删除。其他日志类别升级后保持永久保留。允许永久保留（-1）或 1–3650 天；已清理数据不能通过调大天数恢复。活跃会话、运行中日志和保全记录不清理。</Typography.Paragraph>
  <Table<Category> size="small" rowKey="id" pagination={false} dataSource={config.data?.categories??[]} columns={[
   {title:'日志类别',dataIndex:'label'},
   {title:'当前策略',render:(_,v)=>labelDays(config.data?.days[v.key]??-1,v.id)},
   {title:'调整为',render:(_,v)=>{const days=draft[v.key]??config.data?.days[v.key]??-1;return <Space wrap><Select aria-label={`${v.label}保留方式`} value={days===-1?'forever':'days'} disabled={!canEdit||busy} style={{width:120}} options={[{value:'forever',label:v.id==='session'?'跟随访问记录':'永久保留'},{value:'days',label:'按天数'}]} onChange={value=>setDraft(d=>({...d,[v.key]:value==='forever'?-1:31}))}/>{days!==-1&&<InputNumber aria-label={`${v.label}保留天数`} value={days} min={1} max={3650} precision={0} disabled={!canEdit||busy} onChange={value=>{if(value!==null)setDraft(d=>({...d,[v.key]:value}))}}/>}</Space>}},
  ]}/>
  <Space><Button onClick={()=>void refresh()} disabled={busy}>刷新配置</Button>{canEdit&&<Button type="primary" onClick={()=>void prepare()} loading={busy} disabled={!config.data}>预览清理影响</Button>}</Space>
 </Space>;
 return <Card title="审计日志保留与保全" loading={config.isPending}>
  {(error||config.error)&&<Alert type="error" title={error||config.error?.message}/>} {notice&&<Alert type="success" title={notice}/>}
  <Tabs items={[{key:'policy',label:'保留策略',children:policies},...(canEdit?[{key:'holds',label:'保全记录',children:<Space orientation="vertical" style={{width:'100%'}}>
   <Typography.Paragraph type="secondary">保全记录不受自动清理影响。按日志类别及记录 ID 设置，可从审计详情或接口结果获取 ID。访问记录保全包含其录屏和命令；独立文件、SQL、任务日志需分别保全。解除后，已经超期的记录会在后续清理中删除。</Typography.Paragraph>
   <Button onClick={()=>editHold({category:'access',recordId:'',reason:'',held:true})}>新增保全</Button>
   {holds.error&&<Alert type="error" title={holds.error.message}/>}
   <Table<Hold> size="small" rowKey={v=>v.category+v.recordId} dataSource={holds.data?.items??[]} loading={holds.isFetching} pagination={{current:holdPage,pageSize:50,total:holds.data?.total,onChange:setHoldPage,showSizeChanger:false}} columns={[
    {title:'类别',dataIndex:'category',render:id=>config.data?.categories.find(c=>c.id===id)?.label??id},{title:'记录 ID',dataIndex:'recordId',render:id=><Typography.Text copyable>{id}</Typography.Text>},{title:'原因',dataIndex:'reason'},{title:'设置时间',dataIndex:'createdAt',render:v=>new Date(v).toLocaleString()},{title:'操作',render:(_,v)=><Button type="link" onClick={()=>editHold({...v,held:false,reason:''})}>解除保全</Button>},
   ]}/>
  </Space>}]:[])]}/>
  <Modal open={!!preview} title="确认保留策略与清理影响" onCancel={()=>setPreview(undefined)} onOk={()=>void save()} confirmLoading={busy}>
   {error&&<Alert type="error" title={error}/>}<Alert type="warning" title="此处仅预览，不立即删除。保存后每分钟分批清理，预览有效期十分钟，实际清理数量可能随时间变化。"/>
   <Table rowKey="key" size="small" pagination={false} dataSource={preview?.items??[]} columns={[{title:'类别',dataIndex:'label'},{title:'拟保存',dataIndex:'days',render:(days:number,v)=>labelDays(days,v.category)},{title:'可清理',dataIndex:'eligible'},{title:'超期但保全',dataIndex:'held'}]}/>
  </Modal>
  <Modal open={!!hold} title={hold?.held?'新增日志保全':'解除日志保全'} onCancel={()=>setHold(undefined)} onOk={()=>void saveHold()} confirmLoading={busy}>
   {error&&<Alert type="error" title={error}/>}<Form form={form} layout="vertical"><Form.Item name="category" label="日志类别" rules={[{required:true}]}><Select disabled={!hold?.held} options={config.data?.categories.filter(c=>c.id!=='session').map(c=>({label:c.label,value:c.id}))}/></Form.Item><Form.Item name="recordId" label="记录 ID" rules={[{required:true,pattern:/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,message:'请输入日志记录的 UUID'}]}><Input disabled={!hold?.held}/></Form.Item><Form.Item name="reason" label={hold?.held?'保全原因':'解除原因'} rules={[{required:true,whitespace:true,max:500}]}><Input.TextArea rows={3} maxLength={500} showCount/></Form.Item></Form>
  </Modal>
 </Card>;
}
