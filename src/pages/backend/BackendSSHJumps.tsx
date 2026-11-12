import Modal from './BackendFormModal';
import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Checkbox,Form,Input,InputNumber,Select,Space,Table,Tag,Typography} from 'antd';
import requests from '@/api/core/requests';
type Jump={parentId:string;id:string;name:string;host:string;port:number;credentialId:string;enabled:boolean;version:number;publicKey:string;fingerprint:string};
type Observed={observed:{publicKey:string;fingerprint:string};version:number};
const base='/admin/ssh-jumps';
export default function BackendSSHJumps(){
 const client=useQueryClient(),[form]=Form.useForm();
 const [editing,setEditing]=useState<Jump|null|undefined>(),[trust,setTrust]=useState<(Observed&{id:string;name:string})>(),[confirmed,setConfirmed]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const jumps=useQuery({queryKey:['ssh-jumps'],queryFn:()=>requests.get<Jump[]>(base,{errorMode:'local'})});
 const credentials=useQuery({queryKey:['jump-credentials'],queryFn:()=>requests.get<{id:string;name:string}[]>('/admin/credentials',{errorMode:'local'})});
 const run=async(fn:()=>Promise<void>)=>{setBusy(true);setError('');try{await fn();await client.invalidateQueries({queryKey:['ssh-jumps']})}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const edit=(v:Jump|null)=>{setEditing(v);form.resetFields();form.setFieldsValue(v??{port:22,enabled:true,parentId:''})};
 return <Space orientation="vertical" size={16} style={{width:'100%'}}>
  <Alert type="info" showIcon title="SSH 跳板 · 多级转发" description="先创建跳板并核验主机密钥，再到资产编辑中选择 SSH 跳板。跳板须允许 TCP 转发并可访问目标服务器；目标域名由跳板解析。停用或修改配置会断开已有连接，连接失败不会回退直连。最多支持 4 级跳板；从最外层开始逐级配置并核验主机密钥。"/>
  {(error||jumps.error||credentials.error)&&<Alert type="error" title={error||jumps.error?.message||credentials.error?.message}/>}
  <Space><Button type="primary" onClick={()=>edit(null)}>新建 SSH 跳板</Button><Button onClick={()=>void jumps.refetch()}>刷新</Button></Space>
  <Table<Jump> rowKey="id" dataSource={jumps.data} loading={jumps.isFetching} columns={[
   {title:'名称',dataIndex:'name'},{title:'上级跳板',render:(_,v)=>jumps.data?.find(j=>j.id===v.parentId)?.name||'后端直连'},{title:'地址',render:(_,v)=>`${v.host}:${v.port}`},{title:'状态',render:(_,v)=><Tag color={v.enabled?'green':'default'}>{v.enabled?'启用':'停用'}</Tag>},
   {title:'主机密钥',render:(_,v)=>v.fingerprint?<Typography.Text copyable style={{fontSize:12}}>{v.fingerprint}</Typography.Text>:<Tag color="orange">待核验</Tag>},
   {title:'操作',render:(_,v)=><Space><Button onClick={()=>edit(v)}>编辑</Button><Button disabled={busy} onClick={()=>void run(async()=>{const result=await requests.post<Observed>(`${base}/${v.id}/discover`,undefined,{errorMode:'local'});setConfirmed(false);setTrust({...result,id:v.id,name:v.name})})}>探测主机密钥</Button></Space>}
  ]}/>
  <Modal title={editing?'编辑 SSH 跳板':'新建 SSH 跳板'} open={editing!==undefined} confirmLoading={busy} onCancel={()=>setEditing(undefined)} onOk={()=>form.submit()} forceRender>
   {error&&<Alert type="error" title={error}/>}
   <Form form={form} layout="vertical" onFinish={v=>void run(async()=>{const body={...v,version:editing?.version??0};if(editing)await requests.put(`${base}/${editing.id}`,body,{errorMode:'local'});else await requests.post(base,body,{errorMode:'local'});setEditing(undefined)})}>
    <Form.Item name="name" label="名称" rules={[{required:true,max:100}]}><Input/></Form.Item>
    <Form.Item name="host" label="主机地址" rules={[{required:true,max:253}]}><Input/></Form.Item>
    <Form.Item name="parentId" label="上级跳板" extra="为空表示后端直连此跳板；更改后需重新核验密钥。循环依赖或超过 4 级将被拒绝。"><Select options={[{value:'',label:'后端直连'},...(jumps.data??[]).filter(j=>j.id!==editing?.id).map(j=>({value:j.id,label:j.name,disabled:!j.enabled||!j.fingerprint}))]}/></Form.Item>
    <Form.Item name="port" label="端口" rules={[{required:true}]}><InputNumber min={1} max={65535}/></Form.Item>
    <Form.Item name="credentialId" label="跳板登录凭据" rules={[{required:true}]}><Select options={(credentials.data??[]).map(c=>({value:c.id,label:c.name}))}/></Form.Item>
    <Form.Item name="enabled" valuePropName="checked"><Checkbox>启用</Checkbox></Form.Item>
   </Form>
  </Modal>
  <Modal title={`核验跳板主机密钥 · ${trust?.name??''}`} open={!!trust} confirmLoading={busy} okText="信任此密钥" okButtonProps={{disabled:!confirmed}} onCancel={()=>setTrust(undefined)} onOk={()=>void run(async()=>{if(!trust)return;await requests.post(`${base}/${trust.id}/trust`,{version:trust.version,publicKey:trust.observed.publicKey},{errorMode:'local'});setTrust(undefined)})}>
   {error&&<Alert type="error" title={error}/>}
   <Typography.Paragraph>请通过服务器控制台或管理员提供的指纹核对，下方仅为网络探测结果。</Typography.Paragraph>
   <Typography.Paragraph copyable>{trust?.observed.fingerprint}</Typography.Paragraph>
   <Checkbox checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}>已通过独立可信渠道核对指纹</Checkbox>
  </Modal>
 </Space>;
}
