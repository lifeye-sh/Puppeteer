import {useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {Alert,Button,Card,Form,Input,InputNumber,Popconfirm,Select,Space,Switch,Table,Tag,Typography} from 'antd';
import {Link} from 'react-router-dom';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';

type Rule={id:string;name:string;metric:string;agentId:string;threshold:number;recovery:number;holdSeconds:number;enabled:boolean;version:number;firing:boolean;value:number|null;state:string;checkedAt:string|null};
type Event={id:string;ruleId:string;name:string;metric:string;kind:string;value:number|null;createdAt:string;acknowledgedAt:string|null};
type Metric={key:string;label:string;unit:string;scope:'agent'|'platform';min:number;max:number;description:string};
const states:Record<string,string>={unknown:'数据未知',normal:'正常',pending:'等待持续时间',firing:'告警中'};
export default function BackendMonitorRules(){
 const [form]=Form.useForm();const metric=Form.useWatch('metric',form);
 const catalog=useQuery({queryKey:['monitor-metrics'],queryFn:()=>requests.get<Metric[]>('/admin/monitor-metrics',{errorMode:'local'})});
 const metrics=catalog.data??[];const selected=metrics.find(m=>m.key===metric);
 const [editing,setEditing]=useState<Rule|null|undefined>(),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const [cursor,setCursor]=useState<{before:string;beforeId:string}>();
 const rules=useQuery({queryKey:['monitor-rules'],queryFn:()=>requests.get<Rule[]>('/admin/monitor-rules',{errorMode:'local'}),refetchInterval:10000});
 const agents=useQuery({queryKey:['agent-devices'],queryFn:()=>requests.get<{id:string;name:string}[]>('/admin/agent-gateways',{errorMode:'local'})});
 const events=useQuery({queryKey:['system-events',cursor],queryFn:()=>requests.get<{items:Event[];hasMore:boolean}>(`/admin/system-events?${new URLSearchParams(cursor)}`,{errorMode:'local'}),refetchInterval:cursor?false:10000});
 const edit=(r:Rule|null)=>{form.resetFields();form.setFieldsValue(r??{name:'',metric:'agent.cpu',agentId:'',threshold:90,recovery:75,holdSeconds:30,enabled:false});setEditing(r);setError('')};
 const save=async()=>{setBusy(true);setError('');try{const values=await form.validateFields();const body={...values,agentId:metrics.find(m=>m.key===values.metric)?.scope==='platform'?'':values.agentId,version:editing?.version??0};if(editing)await requests.put(`/admin/monitor-rules/${editing.id}`,body,{errorMode:'local'});else await requests.post('/admin/monitor-rules',body,{errorMode:'local'});setEditing(undefined);await rules.refetch();await events.refetch()}catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}};
 return <Space orientation="vertical" size={16} style={{width:'100%'}}>
  <Alert type="info" title="告警评估每 10 秒运行一次" description={<>平台规则从共享数据库采样，Agent 规则使用服务端收到的指标；CPU、内存和磁盘数据失效时保持未知，已有告警不会误报恢复。恢复值必须低于触发阈值。修改规则会取消旧条件并重新评估。通知需在 <Link to="/setting">通知配置</Link> 中选择“运维告警触发 / 恢复”和有管理权限的指定接收人。</>}/>
  {(error||rules.error||events.error||agents.error||catalog.error)&&<Alert type="error" title={error||rules.error?.message||events.error?.message||agents.error?.message||catalog.error?.message}/>}
  <Card title="告警规则" extra={<Space><Button onClick={()=>{void rules.refetch();void events.refetch()}}>刷新</Button><Button type="primary" disabled={!catalog.data} onClick={()=>edit(null)}>新建规则</Button></Space>}>
   <Table<Rule> rowKey="id" dataSource={rules.data} loading={rules.isPending} scroll={{x:950}} columns={[
    {title:'名称',dataIndex:'name'},{title:'指标',dataIndex:'metric',render:v=>metrics.find(m=>m.key===v)?.label??v},{title:'触发 / 恢复',render:(_,r)=>`${r.threshold} / ${r.recovery}`},
    {title:'持续时间',dataIndex:'holdSeconds',render:v=>`${v} 秒`},{title:'当前值',dataIndex:'value',render:v=>v==null?'—':Number(v).toFixed(1)},
    {title:'状态',render:(_,r)=><Space><Tag color={r.firing?'red':r.state==='normal'?'green':'default'}>{!r.enabled?'已禁用':states[r.state]??r.state}</Tag>{r.enabled&&r.firing&&r.state==='unknown'&&<Tag color="red">原告警未恢复</Tag>}</Space>},
    {title:'操作',render:(_,r)=><Space><Button onClick={()=>edit(r)}>编辑</Button><Popconfirm title="删除此规则？历史事件会保留。" onConfirm={()=>{setError('');return requests.delete(`/admin/monitor-rules/${r.id}?version=${r.version}`,{errorMode:'local'}).then(()=>{void rules.refetch();void events.refetch()}).catch(e=>setError(e.message))}}><Button danger>删除</Button></Popconfirm></Space>},
   ]}/>
  </Card>
  <Card title="系统事件" extra={<Button onClick={()=>{setCursor(undefined);void events.refetch()}}>最新事件</Button>}>
   <Table<Event> rowKey="id" dataSource={events.data?.items} loading={events.isFetching} pagination={false} scroll={{x:850}} columns={[
    {title:'时间',dataIndex:'createdAt',render:v=>new Date(v).toLocaleString()},{title:'规则',dataIndex:'name'},{title:'事件',dataIndex:'kind',render:v=>({firing:'告警触发',recovered:'告警恢复',cancelled:'规则修改，旧条件取消'}[v as string]??v)},
    {title:'观测值',dataIndex:'value',render:v=>v==null?'—':Number(v).toFixed(1)},{title:'确认',render:(_,r)=>r.acknowledgedAt?'已确认':<Button onClick={()=>{setError('');void requests.post(`/admin/system-events/${r.id}/acknowledge`,undefined,{errorMode:'local'}).then(()=>events.refetch()).catch(e=>setError(e.message))}}>确认已查看</Button>},
   ]}/>
   <Space style={{marginTop:16}}><Typography.Text type="secondary">确认只标记已查看，不解除告警。</Typography.Text><Button disabled={!events.data?.hasMore} onClick={()=>{const items=events.data?.items;const last=items?.[items.length-1];if(last)setCursor({before:last.createdAt,beforeId:last.id})}}>更早事件</Button></Space>
  </Card>
  <Modal open={editing!==undefined} title={editing?'编辑告警规则':'新建告警规则'} onCancel={()=>setEditing(undefined)} onOk={()=>void save()} confirmLoading={busy}>
   {error&&<Alert type="error" title={error}/>}
   <Form form={form} layout="vertical">
    <Form.Item name="name" label="规则名称" rules={[{required:true,max:100}]}><Input maxLength={100}/></Form.Item>
    <Form.Item name="metric" label="指标" extra={selected?.description} rules={[{required:true}]}><Select loading={catalog.isPending} options={metrics.map(m=>({value:m.key,label:`${m.label}（${m.unit}）`}))} onChange={key=>{const m=metrics.find(x=>x.key===key);if(m){form.setFieldsValue({agentId:m.scope==='platform'?'':undefined,threshold:m.unit==='%'?90:m.unit==='秒'?60:1,recovery:m.unit==='%'?75:0})}}}/></Form.Item>
    {selected?.scope==='agent'&&<Form.Item name="agentId" label="Agent" rules={[{required:true}]}><Select showSearch optionFilterProp="label" options={agents.data?.map(a=>({value:a.id,label:a.name}))}/></Form.Item>}
    <div style={{display:'grid',gridTemplateColumns:'1fr 1fr',gap:16}}>
     <Form.Item name="threshold" label="触发阈值（大于等于）" rules={[{required:true}]}><InputNumber min={selected?.min??0.1} max={selected?.max??100} style={{width:'100%'}}/></Form.Item>
     <Form.Item name="recovery" label="恢复阈值（小于等于）" dependencies={['threshold']} rules={[{required:true},({getFieldValue})=>({validator:(_,v)=>v<getFieldValue('threshold')?Promise.resolve():Promise.reject(new Error('恢复阈值必须小于触发阈值'))})]}><InputNumber min={0} style={{width:'100%'}}/></Form.Item>
    </div>
    <Form.Item name="holdSeconds" label="持续超限秒数" extra="0 表示首次检测到超限即触发；瞬时波动可通过持续时间过滤。" rules={[{required:true},{type:'integer',min:0,max:3600}]}><InputNumber min={0} max={3600}/></Form.Item>
    <Form.Item name="enabled" label="启用规则" valuePropName="checked"><Switch/></Form.Item>
   </Form>
  </Modal>
 </Space>;
}
