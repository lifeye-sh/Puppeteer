import Modal from './BackendFormModal';
import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Card,Form,Input,Popconfirm,Space,Table,Tag,Typography} from 'antd';
import requests from '@/api/core/requests';
import BackendAgentInstaller from './BackendAgentInstaller';
type Scope={cidrs:string[];ports:number[]};
type Registration={id:string;name:string;networkScope:Scope;expiresAt:string;consumedAt?:string;revokedAt?:string;token?:string};
type Device={id:string;name:string;os:string;arch:string;version:string;online:boolean;dialReady:boolean;lastHeartbeatAt?:string;revokedAt?:string};
const registrations='/admin/agent-registration-tokens',devices='/admin/agent-gateways';
export default function BackendAgentGateways(){
 const [install,setInstall]=useState(false);
 const defaults=useQuery({queryKey:['agent-install-defaults'],enabled:install,queryFn:()=>requests.get<{endpoint:string;revision:number}>('/admin/agent-installations/config',{errorMode:'local'})});
 const client=useQueryClient();const [form]=Form.useForm();const [open,setOpen]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState('');const [issued,setIssued]=useState<Registration>();
 const tokens=useQuery({queryKey:['agent-registrations'],queryFn:()=>requests.get<Registration[]>(registrations,{errorMode:'local'})});
 const agents=useQuery({queryKey:['agent-devices'],queryFn:()=>requests.get<Device[]>(devices,{errorMode:'local'}),refetchInterval:3000});
 const refresh=()=>Promise.all([client.invalidateQueries({queryKey:['agent-registrations']}),client.invalidateQueries({queryKey:['agent-devices']})]);
 const create=async(v:{name:string;cidrs:string;ports:string})=>{setBusy(true);setError('');try{const cidrs=v.cidrs.split(/[\s,，]+/).filter(Boolean),parts=v.ports.split(/[\s,，]+/).filter(Boolean);if(!parts.length||parts.some(p=>!/^\d+$/.test(p)||Number(p)<1||Number(p)>65535))throw new Error('请输入 1–65535 范围内的端口');const result=await requests.post<Registration>(registrations,{name:v.name,networkScope:{cidrs,ports:parts.map(Number)}},{errorMode:'local'});setIssued(result);setOpen(false);form.resetFields();await refresh()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const revoke=async(base:string,id:string)=>{setBusy(true);setError('');try{await requests.post(`${base}/${id}/revoke`,undefined,{errorMode:'local'});await refresh()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <Space orientation="vertical" size={16} style={{width:'100%'}}>
  <BackendAgentInstaller/>
  <Alert type="info" showIcon title="Agent 注册与设备管理" description="支持 Linux amd64 Agent 注册、上线、心跳、重连和撤销。新版 Agent 可在允许网段和端口内转发 SSH；资产中选择 Agent 后使用原终端、录屏及文件管理。监控页提供 CPU、内存、磁盘和网卡指标、设备密钥轮换以及受管更新操作。自有 Agent 与官方旧 Agent 不兼容。"/>
  {(error||tokens.error||agents.error)&&<Alert type="error" title={error||tokens.error?.message||agents.error?.message}/>}
  <Space><Button type="primary" onClick={()=>setOpen(true)}>创建注册令牌</Button><Button onClick={()=>void refresh()}>刷新</Button><Button onClick={()=>setInstall(true)}>安装说明</Button><Button href="/api/admin/agent-binary/linux-amd64" download>下载 Linux amd64 Agent</Button></Space>
  <Card title="注册令牌（最近 100 条）"><Table<Registration> rowKey="id" loading={tokens.isFetching} dataSource={tokens.data} columns={[{title:'名称',dataIndex:'name'},{title:'允许范围',render:(_,v)=><>{v.networkScope.cidrs.join(', ')} · 端口 {v.networkScope.ports.join(', ')}</>},{title:'到期时间',render:(_,v)=>new Date(v.expiresAt).toLocaleString()},{title:'状态',render:(_,v)=><Tag>{v.revokedAt?'已撤销':v.consumedAt?'已使用':Date.parse(v.expiresAt)<=Date.now()?'已过期':'待注册'}</Tag>},{title:'操作',render:(_,v)=><Popconfirm title="撤销此注册令牌？" onConfirm={()=>revoke(registrations,v.id)}><Button danger disabled={busy||!!v.revokedAt||!!v.consumedAt}>撤销</Button></Popconfirm>}]}/></Card>
  <Card title="已登记设备（最近 100 条）"><Table<Device> rowKey="id" loading={agents.isFetching} dataSource={agents.data} columns={[{title:'名称',dataIndex:'name'},{title:'系统',render:(_,v)=>`${v.os} / ${v.arch}`},{title:'版本',dataIndex:'version'},{title:'状态',render:(_,v)=><Tag color={v.revokedAt?'red':v.online?'green':'default'}>{v.revokedAt?'已撤销':v.dialReady?'在线 · 可拨号':v.online?'在线 · 暂不可拨号':'离线'}</Tag>},{title:'最后心跳',render:(_,v)=>v.lastHeartbeatAt?new Date(v.lastHeartbeatAt).toLocaleString():'—'},{title:'操作',render:(_,v)=><Popconfirm title="撤销此设备凭据？" onConfirm={()=>revoke(devices,v.id)}><Button danger disabled={busy||!!v.revokedAt}>撤销</Button></Popconfirm>}]}/></Card>
  <Modal title="Linux amd64 Agent 安装与启动" open={install} onCancel={()=>setInstall(false)} footer={<Button onClick={()=>setInstall(false)}>关闭</Button>} width={800}>
   <Typography.Paragraph>下载程序并复制到 Linux 测试机器。使用专用普通用户和私有目录运行，将新建令牌写入 token.txt 并设为 0600；请勿把令牌放进命令行参数。将下方 endpoint 改为 Agent 实际可访问的后端 HTTPS 地址。</Typography.Paragraph>
   <pre style={{whiteSpace:'pre-wrap'}}>{`chmod 700 nt-agent-linux-amd64
chmod 600 token.txt
./nt-agent-linux-amd64 --endpoint '${defaults.data?.endpoint||'https://your-backend.example'}' --name test-agent --token-file token.txt --config agent.json

# 后续启动只使用保存的设备配置
./nt-agent-linux-amd64 --config agent.json`}</pre><Typography.Paragraph>受管更新需由管理员通过可信渠道配置 TUF root、仓库与私有 stateDir，然后以 --supervise 启动。默认每 10 分钟检查、只通知；在监控页可对单台设备检查或排空连接后升级。新进程认证及稳定性检查失败会恢复旧程序。离线发布工具为 nt-agent-release；配置模板和服务文件见 backend/deploy，操作步骤见 backend/deploy/README.md。</Typography.Paragraph>
   <Typography.Paragraph>首次注册成功后安全删除 token.txt。agent.json 包含设备凭据，请保持 0600 权限。Ctrl+C 停止；重启时自动重连。可以前台方式运行，或使用项目 backend/deploy/nt-agent.service 配置 Linux systemd 服务。Windows Agent 不在本轮 Linux amd64 交付范围。</Typography.Paragraph>
   <Typography.Paragraph>本机隔离开发环境可显式追加 --allow-insecure-development 并使用可达的 HTTP 后端地址；backend.localhost 仅指向运行 Agent 的本机，不能作为另一台机器的服务器地址。</Typography.Paragraph>
  </Modal>
  <Modal title="创建一次性注册令牌" open={open} confirmLoading={busy} onCancel={()=>setOpen(false)} onOk={()=>form.submit()}><Form form={form} layout="vertical" onFinish={v=>void create(v)} initialValues={{ports:'22'}}><Form.Item name="name" label="名称" rules={[{required:true}]}><Input maxLength={200}/></Form.Item><Form.Item name="cidrs" label="允许网段（CIDR，逗号或换行分隔）" rules={[{required:true}]}><Input.TextArea placeholder="10.0.0.0/24"/></Form.Item><Form.Item name="ports" label="允许端口" rules={[{required:true}]}><Input placeholder="22, 3389"/></Form.Item></Form></Modal>
  <Modal title="注册令牌仅显示一次" open={!!issued} footer={<Button onClick={()=>setIssued(undefined)}>已保存，关闭</Button>} onCancel={()=>setIssued(undefined)} destroyOnHidden><Typography.Paragraph>有效期 10 分钟，只能注册一个设备。关闭后无法再次查看。</Typography.Paragraph><Typography.Paragraph copyable style={{overflowWrap:'anywhere'}}>{issued?.token}</Typography.Paragraph></Modal>
 </Space>;
}
