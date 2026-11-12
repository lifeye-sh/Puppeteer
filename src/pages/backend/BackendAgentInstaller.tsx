import Modal from './BackendFormModal';
import {useEffect,useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Card,Checkbox,Form,Input,Select,Space,Table,Tag} from 'antd';
import requests from '@/api/core/requests';

type Asset={id:string;name:string;host:string;port:number;enabled:boolean;protocol:string;credentialId?:string};
type Job={id:string;assetId:string;assetName:string;agentId:string;state:string;step:string;message:string;createdAt:string};
type Values={assetId:string;name:string;endpoint:string;cidrs:string;ports:string;allowInsecureDevelopment:boolean};
const steps:Record<string,string>={queued:'排队',connecting:'连接 SSH',preflight:'环境检查',registering:'登记设备',installing:'安装服务',waiting_online:'等待上线',completed:'已完成'};
export default function BackendAgentInstaller(){
 const [open,setOpen]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[search,setSearch]=useState('');
 const [form]=Form.useForm<Values>();const client=useQueryClient();
 const defaults=useQuery({queryKey:['agent-install-defaults'],enabled:open,queryFn:()=>requests.get<{endpoint:string;revision:number}>('/admin/agent-installations/config',{errorMode:'local'})});
 useEffect(()=>{if(open&&defaults.data?.endpoint&&!form.isFieldTouched('endpoint')&&!form.getFieldValue('endpoint'))form.setFieldsValue({endpoint:defaults.data.endpoint})},[open,defaults.data,form]);
 const jobs=useQuery({queryKey:['agent-installations'],queryFn:()=>requests.get<Job[]>('/admin/agent-installations',{errorMode:'local'}),refetchInterval:3000});
 const assets=useQuery({queryKey:['agent-install-assets',search],enabled:open,queryFn:()=>requests.get<{items:Asset[]}>('/admin/assets/paging?pageSize=100&search='+encodeURIComponent(search),{errorMode:'local'})});
 const submit=async(v:Values)=>{setBusy(true);setError('');try{
  const ports=v.ports.split(/[\s,，]+/).filter(Boolean);if(!ports.length||ports.some(p=>!/^\d+$/.test(p)||Number(p)<1||Number(p)>65535))throw new Error('端口必须为 1–65535');
  await requests.post('/admin/agent-installations',{assetId:v.assetId,name:v.name,endpoint:v.endpoint.trim(),allowInsecureDevelopment:!!v.allowInsecureDevelopment,networkScope:{cidrs:v.cidrs.split(/[\s,，]+/).filter(Boolean),ports:ports.map(Number)}},{errorMode:'local'});
  setOpen(false);form.resetFields();await client.invalidateQueries({queryKey:['agent-installations']});
 }catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <Card title="从资产安装 Linux amd64 Agent" extra={<Button type="primary" onClick={()=>{setError('');setOpen(true)}}>选择资产安装</Button>}>
  <Alert type="info" showIcon title="选择已配置 SSH 凭据并核验主机密钥的 Linux 资产" description="通过资产现有连接路径安装 systemd 服务并确认上线。需要 root 或免密 sudo；不会覆盖已有安装，也不会改变资产的连接路径。自动更新需另行配置可信 TUF 仓库。" style={{marginBottom:12}}/>
  {jobs.error&&<Alert type="error" title={jobs.error.message}/>}
  <Table<Job> rowKey="id" size="small" dataSource={jobs.data} pagination={{pageSize:5}} columns={[
   {title:'安装时间',render:(_,j)=>new Date(j.createdAt).toLocaleString()},
   {title:'资产',render:(_,j)=><span title={j.assetId}>{j.assetName}</span>},
   {title:'状态',render:(_,j)=><Tag color={j.state==='succeeded'?'green':j.state==='failed'?'red':'blue'}>{j.state==='succeeded'?'成功':j.state==='failed'?'失败':'进行中'}</Tag>},
   {title:'阶段',render:(_,j)=>steps[j.step]??j.step},{title:'结果',dataIndex:'message'},
   {title:'设备',render:(_,j)=>j.agentId?<a href="/monitoring">查看监控</a>:'—'}
  ]}/>
  <Modal title="选择资产并安装 Agent" open={open} width={680} confirmLoading={busy} okText="开始安装" onOk={()=>form.submit()} onCancel={()=>!busy&&setOpen(false)} maskClosable={!busy}>
   <Space orientation="vertical" style={{width:'100%'}}>
    <Alert type="info" title="安装到 /opt/next-terminal-agent 和 /var/lib/next-terminal-agent，创建 nt-agent 服务账户并启用开机启动。安装进度保留在列表中，关闭弹窗不会中止已提交任务。"/>
    {(error||assets.error||defaults.error)&&<Alert type="error" title={error||assets.error?.message||defaults.error?.message}/>}
    <Form form={form} layout="vertical" initialValues={{ports:'22',allowInsecureDevelopment:false}} onFinish={v=>void submit(v)}>
     <Form.Item name="assetId" label="安装目标资产" rules={[{required:true}]} extra="输入名称或地址搜索资产。系统与权限将在连接后检查；未核验的主机不会自动信任。">
      <Select showSearch={{filterOption:false,onSearch:setSearch}} loading={assets.isFetching} options={(assets.data?.items??[]).filter(a=>a.enabled&&a.protocol==='ssh'&&a.credentialId).map(a=>({value:a.id,label:`${a.name} · ${a.host}:${a.port}`}))} onChange={id=>{const a=assets.data?.items.find(a=>a.id===id);if(a)form.setFieldsValue({name:a.name+' Agent'})}}/>
     </Form.Item>
     <Form.Item name="name" label="Agent 名称" rules={[{required:true}]}><Input maxLength={200}/></Form.Item>
     <Form.Item name="endpoint" label="目标机器可访问的后端地址" rules={[{required:true}]} extra="使用后端 HTTPS 地址，不是前端 Vite 地址。backend.localhost/localhost 指向目标机器自身；请填写实际可达的服务器域名或 IP。"><Input placeholder="https://backend.example.com" maxLength={2048}/></Form.Item>
     <Form.Item name="allowInsecureDevelopment" valuePropName="checked"><Checkbox>隔离开发环境允许 HTTP（生产请使用 HTTPS）</Checkbox></Form.Item>
     <Form.Item name="cidrs" label="允许 Agent 拨号的网段（CIDR）" rules={[{required:true}]}><Input.TextArea placeholder="10.0.0.0/24"/></Form.Item>
     <Form.Item name="ports" label="允许拨号的端口" rules={[{required:true}]}><Input placeholder="22,3389"/></Form.Item>
    </Form>
   </Space>
  </Modal>
 </Card>;
}
