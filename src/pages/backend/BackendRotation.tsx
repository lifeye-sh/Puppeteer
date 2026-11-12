import {useRef,useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Form,Popconfirm,Select,Space,Table,Tag,Typography} from 'antd';
import Modal from './BackendFormModal';
import {pagedOptions} from './r08-options';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import {executionEntitlement} from './execution-entitlement';

type Target={id:string;assetId:string;kind:string;state:string;reason:string;newCredentialId:string};
type Run={id:string;state:string;reason:string;createdAt:string;cancelRequested:boolean;targets:Target[]};
const labels:Record<string,string>={pending:'待执行',queued:'排队中',running:'执行中',applying:'修改远端',verifying:'验证中',succeeded:'成功',failed:'失败',partial:'部分成功',unknown:'待核对',cancelled:'已取消'};
const reasons:Record<string,string>={authorization_or_version_changed:'权限、资产或凭据已变化',host_key_untrusted:'主机指纹未确认',credential_unavailable:'凭据不可用',ssh_connect_failed:'SSH 连接失败',linux_chpasswd_privilege_required:'需要 Linux root 或免交互 sudo chpasswd 权限',authorized_keys_preflight_failed:'请检查标准 authorized_keys 路径、权限、锁目录和 SFTP 支持',remote_result_requires_review:'远端修改结果不明，请核对',candidate_verification_failed:'候选凭据未通过验证',both_credentials_work:'新旧凭据均可用，请检查远端认证配置',previous_verification_inconclusive:'无法确认旧凭据是否失效',activation_requires_review:'远端已修改，平台绑定需要核对',key_commit_result_unknown:'旧公钥移除结果不明',worker_stopped:'执行进程中断，未自动重试',authentication_reconciled:'已通过独立认证核对',cancelled_before_dispatch:'发送修改前已取消',cancel_before_key_commit:'取消发生在旧密钥移除前，请核对'};
const badge=(value:string)=><Tag color={value==='succeeded'?'green':value==='unknown'?'orange':value==='failed'?'red':'blue'}>{labels[value]||value}</Tag>;
export default function BackendRotation(){
 const {capabilities}=useBackendRuntime();const execution=executionEntitlement(capabilities.features,'automation.execute','凭据轮换');
 const client=useQueryClient();const [page,setPage]=useState(1);const [open,setOpen]=useState(false);const [selected,setSelected]=useState(new URLSearchParams(window.location.search).get('run')||'');const [error,setError]=useState('');const [busy,setBusy]=useState(false);const key=useRef('');const lock=useRef(false);const [form]=Form.useForm();
 const runs=useQuery({queryKey:['r10-rotation',page],queryFn:()=>requests.get<{items:Run[];total:number}>(`/rotation-runs?page=${page}`,{errorMode:'local'}),refetchInterval:3000});
 const assets=useQuery({queryKey:['r10-rotation-assets'],queryFn:()=>pagedOptions<{id:string;name:string;protocol:string}>('/portal/assets')});
 const detail=useQuery({queryKey:['r10-rotation-detail',selected],enabled:!!selected,queryFn:()=>requests.get<Run>(`/rotation-runs/${selected}`,{errorMode:'local'}),refetchInterval:3000});
 const names=new Map(assets.data?.map(a=>[a.id,a.name]));
 const act=async(fn:()=>Promise<void>)=>{if(lock.current)return;lock.current=true;setBusy(true);setError('');try{await fn();await client.invalidateQueries({predicate:q=>String(q.queryKey[0]).startsWith('r10-rotation')})}catch(e){setError((e as Error).message)}finally{lock.current=false;setBusy(false)}};
 return <section className="nt-page"><Typography.Title level={2}>凭据轮换</Typography.Title>{!execution.allowed&&<Alert type="warning" title={execution.message+' 结果不明的轮换仍可核对认证。'}/>}
  <Alert type="info" showIcon title="Linux SSH 密码与密钥轮换" description="自动生成独立凭据，验证成功后切换对应资产，保留原共享凭据。密钥使用标准 ~/.ssh/authorized_keys，保留原有访问限制。结果不明时只做认证核对，不自动重复修改。" style={{marginBottom:16}}/>
  {(error||runs.error||detail.error||assets.error)&&<Alert type="error" title={error||((runs.error||detail.error||assets.error) as Error).message}/>}
  <Space style={{marginBottom:16}}><Button type="primary" disabled={!execution.allowed} onClick={()=>{key.current=crypto.randomUUID();form.resetFields();setOpen(true)}}>新建轮换</Button><Button onClick={()=>void runs.refetch()}>刷新</Button></Space>
  <Table<Run> rowKey="id" dataSource={runs.data?.items} loading={runs.isLoading} pagination={{current:page,pageSize:30,total:runs.data?.total,onChange:setPage,showSizeChanger:false}} columns={[
   {title:'创建时间',dataIndex:'createdAt',render:(v:string)=>new Date(v).toLocaleString()},
   {title:'资产',render:(_,v)=>v.targets.map(t=>names.get(t.assetId)||t.assetId).join('、')},
   {title:'状态',dataIndex:'state',render:badge},
   {title:'操作',render:(_,v)=><Space><Button onClick={()=>setSelected(v.id)}>查看逐目标结果</Button>{['queued','running'].includes(v.state)&&<Popconfirm title="请求取消？已发送到远端的修改不能保证撤销，需要核对最终状态。" onConfirm={()=>act(async()=>{await requests.post(`/rotation-runs/${v.id}/cancel`,undefined,{errorMode:'local'})})}><Button danger disabled={v.cancelRequested||busy}>取消</Button></Popconfirm>}</Space>}
  ]}/>
  <Modal title="新建凭据轮换" okText="开始轮换" open={open} onCancel={()=>setOpen(false)} confirmLoading={busy} onOk={()=>act(async()=>{const values=await form.validateFields();const result=await requests.post<{id:string}>('/rotation-runs',{...values,key:key.current},{errorMode:'local'});setOpen(false);setSelected(result.id)})}>
   {error&&<Alert type="error" title={error}/>}<Alert type="warning" showIcon title="将修改选中资产的远端登录凭据" description="密码轮换需要 root 或免密码 sudo chpasswd。密钥轮换需要 SFTP 和原公钥在标准 authorized_keys 中。使用同一远端账户的其他客户端也需要更新凭据。轮换期间请勿同时手工编辑目标凭据或公钥文件。" style={{marginBottom:16}}/>
   <Form form={form} layout="vertical"><Form.Item name="assetIds" label="目标资产（最多 20 台）" rules={[{required:true,message:'请选择资产'},{type:'array',max:20}]}><Select mode="multiple" showSearch optionFilterProp="label" loading={assets.isLoading} options={assets.data?.filter(a=>a.protocol==='ssh').map(a=>({value:a.id,label:a.name}))}/></Form.Item></Form>
  </Modal>
  <Modal title="轮换详情" open={!!selected} onCancel={()=>setSelected('')} footer={null} width={1000}>
   {error&&<Alert type="error" title={error}/>} {detail.data&&<><Space style={{marginBottom:16}}>{badge(detail.data.state)}<Typography.Text type="secondary">{detail.data.id}</Typography.Text></Space><Table<Target> rowKey="id" pagination={false} dataSource={detail.data.targets} columns={[
    {title:'资产',render:(_,t)=>names.get(t.assetId)||t.assetId},{title:'类型',dataIndex:'kind',render:(v:string)=>v==='password'?'密码':'SSH 密钥'},{title:'状态',dataIndex:'state',render:badge},{title:'说明',dataIndex:'reason',render:(v:string)=>reasons[v]||v||'—'},
    {title:'操作',render:(_,t)=>t.state==='unknown'?<Button loading={busy} disabled={detail.data?.state==='running'} onClick={()=>act(async()=>{const result=await requests.post<{state:string}>(`/rotation-runs/${selected}/targets/${t.id}/reconcile`,undefined,{errorMode:'local'});if(result.state==='unknown')setError('核对仍不确定：可能新旧凭据同时有效、均无效或网络不可达。请先检查远端配置，再核对；平台不会重做修改。')})}>仅核对认证</Button>:t.newCredentialId?'已绑定新凭据':'—'}
   ]}/></>}
  </Modal>
 </section>
}
