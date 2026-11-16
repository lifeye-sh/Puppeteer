import {useState} from 'react';
import {Alert, Button, Card, Checkbox, Descriptions, Form, Input, InputNumber, Modal, Select, Switch, Table, Tag} from 'antd';
import {useQuery} from '@tanstack/react-query';
import requests from '@/api/core/requests';

type Policy = {revision:number;mode:string;channel:string;maxVersion:string;timezone:string;weekdays:number[];start:string;durationMinutes:number;expiresAt:string;allowSchemaChange:boolean;retainDays:number};
type State = {policy:Policy;reason?:string;suspended:boolean;lastCheck?:string;lastTask?:string;lastStage?:string;cleanupReason?:string};
const opts={errorMode:'local' as const};
const modes:Record<string,string>={off:'关闭',prepare:'仅自动准备镜像',window:'维护窗口内自动升级'};
const localInput=(date:Date)=>new Date(date.getTime()-date.getTimezoneOffset()*60000).toISOString().slice(0,16);

export default function BackendUpdateAutomation({disabled,mfa,releases}:{disabled:boolean;mfa?:boolean;releases:{version:string;channel:string}[]}){
 const query=useQuery({queryKey:['update-automation'],queryFn:()=>requests.get<State>('/admin/updates/automation',opts),retry:false,refetchInterval:15000});
 const history=useQuery({queryKey:['upgrade-history'],queryFn:()=>requests.get<{items:{id:string;target:string;stage:string;updated:string;reason?:string}[]}>('/admin/updates/history',opts),retry:false,refetchInterval:15000});
 const [form]=Form.useForm();const [open,setOpen]=useState(false),[saving,setSaving]=useState(false),[error,setError]=useState('');
 const [editingRevision,setEditingRevision]=useState(0);
 const mode=Form.useWatch('mode',form);const channel=Form.useWatch('channel',form);
 const configure=()=>{const policy=query.data?.policy;if(!policy)return;setEditingRevision(policy.revision);form.resetFields();form.setFieldsValue({...policy,expiresAt:localInput(policy.expiresAt&&!policy.expiresAt.startsWith('0001-')?new Date(policy.expiresAt):new Date(Date.now()+7*86400000))});setError('');setOpen(true)};
 const save=async()=>{
  let values;try{values=await form.validateFields()}catch{return}
  setSaving(true);setError('');try{
   const {password,totp,confirmedStop,...policy}=values;
   const expiresAt=policy.mode==='off'?(query.data?.policy.expiresAt||'0001-01-01T00:00:00Z'):new Date(policy.expiresAt).toISOString();
   await requests.put('/admin/updates/automation',{policy:{...policy,revision:editingRevision,expiresAt},password,totp,confirmedStop:!!confirmedStop},opts);
   setOpen(false);await query.refetch();
  }catch(e){setError((e as Error).message)}finally{setSaving(false);form.resetFields(['password','totp'])}
 };
 const state=query.data,p=state?.policy;
 return <Card title="自动准备与维护窗口" extra={<Button disabled={disabled||!p} onClick={configure}>配置授权</Button>}>
  {query.error&&<Alert showIcon type="warning" title={query.error.message}/>}
  {p&&<Descriptions column={2} items={[
   {key:'mode',label:'执行策略',children:<Tag color={p.mode==='off'?'default':state?.suspended?'orange':'blue'}>{modes[p.mode]}{state?.suspended?' · 已暂停':''}</Tag>},
   {key:'bound',label:'授权上限',children:p.maxVersion||'—'},
   {key:'window',label:'维护窗口',children:p.mode==='window'?`${p.timezone} ${p.start}，${p.durationMinutes} 分钟`:'—'},
   {key:'expires',label:'授权到期',children:p.mode==='off'?'—':new Date(p.expiresAt).toLocaleString()},
  ]}/>}
  {state?.reason&&<Alert style={{marginTop:12}} type={state.suspended?'warning':'info'} title={state.reason} showIcon/>}
  {state?.cleanupReason&&<p>{state.cleanupReason}</p>}
  <p>自动升级由宿主机执行，默认关闭。授权最长 30 天；失败后暂停，需要管理员重新授权。活动任务期间不能修改策略，关闭页面不会中断升级或恢复。</p>
  <p>通知可在“通知规则”中选择系统更新事件，接收人使用“指定人员”，仅向仍有管理权限的平台管理员投递。业务冻结期间无法投递，恢复后补入通知队列。</p>
  {history.error&&<Alert type="warning" title={history.error.message}/>}
  <Table size="small" rowKey="id" dataSource={history.data?.items??[]} pagination={{pageSize:5}} columns={[{title:'目标版本',dataIndex:'target'},{title:'结果',dataIndex:'stage',render:(s:string)=>({prepared:'准备完成',succeeded:'升级成功',rolled_back:'已恢复',failed:'失败',recovery_required:'恢复待处理'}[s]??s)},{title:'更新时间',dataIndex:'updated',render:(v:string)=>new Date(v).toLocaleString()},{title:'原因',dataIndex:'reason'}]}/>
  <Modal open={open} title="配置自动升级授权" okText="验证身份并保存" confirmLoading={saving} onOk={()=>void save()} onCancel={()=>{if(!saving){setOpen(false);form.resetFields()}}}>
   {error&&<Alert type="error" showIcon title={error}/>}
   <Form form={form} layout="vertical" style={{marginTop:12}}>
    <Form.Item name="mode" label="执行策略" rules={[{required:true}]}><Select options={Object.entries(modes).map(([value,label])=>({value,label}))}/></Form.Item>
    <Form.Item name="channel" label="授权渠道"><Select disabled={mode==='off'} options={[{value:'beta',label:'Beta'},{value:'stable',label:'稳定版'}]}/></Form.Item>
    <Form.Item name="maxVersion" label="最高允许版本" rules={[{required:mode!=='off'}]} extra="只允许已验证发布目录中的版本；更高版本需要重新授权。"><Select disabled={mode==='off'} options={releases.filter(r=>r.channel===channel).map(r=>({value:r.version,label:r.version}))}/></Form.Item>
    <Form.Item name="timezone" label="维护时区" rules={[{required:mode!=='off'}]} extra="仅维护窗口升级使用；自动准备可在授权有效期内随时进行。"><Input disabled={mode!=='window'} placeholder="Asia/Shanghai"/></Form.Item>
    <Form.Item name="weekdays" label="允许开始的星期" rules={[{required:mode!=='off'}]}><Select disabled={mode!=='window'} mode="multiple" options={['日','一','二','三','四','五','六'].map((d,value)=>({value,label:'星期'+d}))}/></Form.Item>
    <Form.Item name="start" label="窗口开始时间" rules={[{required:mode!=='off',pattern:/^([01]\d|2[0-3]):[0-5]\d$/}]}><Input type="time" disabled={mode!=='window'}/></Form.Item>
    <Form.Item name="durationMinutes" label="窗口长度（分钟）"><InputNumber disabled={mode!=='window'} min={15} max={240}/></Form.Item>
    <Form.Item name="expiresAt" label="授权到期（当前浏览器时区，最长 30 天）" rules={[{required:mode!=='off'}]}><Input type="datetime-local" disabled={mode==='off'}/></Form.Item>
    <Form.Item name="allowSchemaChange" label="允许数据库结构版本升级" valuePropName="checked"><Switch disabled={mode==='off'}/></Form.Item>
    <Form.Item name="retainDays" label="成功升级产物保留天数" extra="0 表示不自动清理；此设置独立于执行策略。启用后最少 30 天，且保留最近 3 次成功升级、当前任务及所有失败/恢复任务。历史记录和镜像不会删除。"><InputNumber min={0} max={365}/></Form.Item>
    {mode==='window'&&<><Alert type="warning" showIcon title="窗口内可能自动停止所有会话" description="窗口限制开始停机的时间。已开始的备份、升级或恢复会继续完成，不会在窗口结束时强行终止。"/><Form.Item name="confirmedStop" valuePropName="checked" rules={[{validator:(_,v)=>v?Promise.resolve():Promise.reject(new Error('请确认停机会话授权'))}]}><Checkbox>允许窗口内自动停止会话并完成配套备份、升级和必要恢复</Checkbox></Form.Item></>}
    <Form.Item name="password" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item>
    <Form.Item name="totp" label="新的动态验证码" rules={[{required:mfa},{pattern:/^\d{6}$/}]}><Input maxLength={6} autoComplete="one-time-code"/></Form.Item>
   </Form>
  </Modal>
 </Card>;
}
