import {useState} from 'react';
import {Alert,Button,Card,Descriptions,Form,Input,Space,Table,Tag,Typography} from 'antd';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';

type Status={source:string;state:string;instanceId:string;licenseId?:string;licenseRevision:number;revision:string;expiresAt?:string;allowImplemented:boolean;features:Record<string,boolean>;quotas:Record<string,number|null>;used:Record<string,number>;controlledFeatures:string[];importAllowed:boolean};
type Import={token:string;password:string;totp?:string};
const labels:Record<string,string>={'identity.users':'新增用户（含 LDAP 导入）','resource.assets':'新增主机、数据库和网站资产','session.ssh':'SSH 连接','session.telnet':'Telnet 连接','session.rdp':'RDP 连接','session.vnc':'VNC 连接','database.mysql':'MySQL 连接','database.postgres':'PostgreSQL 连接','ai.assistant':'AI 请求、模型探测与工具执行','automation.execute':'任务执行与凭据轮换','notification.send':'业务通知发送（不含登录验证）','files.write':'SFTP 与共享盘文件写入'};
export default function BackendEntitlements(){
 const cache=useQueryClient(),[open,setOpen]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[form]=Form.useForm<Import>();
 const query=useQuery({queryKey:['backend-entitlements'],queryFn:()=>requests.get<Status>('/admin/entitlements',{errorMode:'local'}),refetchInterval:30000});
 const v=query.data;
 const save=async()=>{setError('');setBusy(true);try{const fields=await form.validateFields();await requests.post('/admin/entitlements',{...fields,revision:v!.licenseRevision},{errorMode:'local'});form.resetFields();setOpen(false);await query.refetch();await cache.invalidateQueries({queryKey:['backend-runtime']})}catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}};
 return <Card title="部署许可与配额" loading={query.isPending} extra={<Button disabled={busy} onClick={()=>void query.refetch()}>刷新许可</Button>}>
  {query.error&&<Alert type="error" title={query.error.message}/>}
  {v&&<Space orientation="vertical" style={{width:'100%'}}>
   <Descriptions column={{xs:1,sm:2,lg:3}} items={[
    {key:'source',label:'许可来源',children:v.source==='local'?'本地部署配置':'签名许可'},
    {key:'state',label:'状态',children:<Tag color={v.state==='valid'?'green':'red'}>{({valid:'有效',missing:'未导入',expired:'已过期',invalid:'无效',not_yet_valid:'尚未生效'} as Record<string,string>)[v.state]??v.state}</Tag>},
    {key:'expiry',label:'到期时间',children:v.expiresAt?new Date(v.expiresAt).toLocaleString():'无到期限制'},
    {key:'instance',label:'实例 ID',children:<Typography.Text copyable>{v.instanceId}</Typography.Text>},
    {key:'revision',label:'许可版本',children:v.source==='local'?'部署文件（重启生效）':v.licenseRevision},
   ]}/>
   <Table size="small" rowKey="key" pagination={false} dataSource={['users','resources','sessions'].map((key,i)=>({key,label:['用户','资产','并发会话'][i],used:v.used[key],limit:v.quotas[key]}))} columns={[
    {title:'配额项目',dataIndex:'label'},{title:'当前使用',dataIndex:'used'},{title:'上限',dataIndex:'limit',render:(n:number|null)=>n??'不限'},
    {title:'剩余',render:(_,row)=>row.limit==null?'不限':Math.max(0,row.limit-row.used)},
   ]}/>
   <Typography.Paragraph type="secondary">用户包含停用账号；资产包含主机、数据库和网站，不含分组。会话统计有效租约中的终端及数据库连接，不含浏览器登录。既有配额下降时保留当前会话，新增连接受限；许可过期或协议被禁用后，会话续租失败。登录、审计查询、删除和恢复入口继续可用。</Typography.Paragraph>
   <Space wrap>{v.controlledFeatures.map(key=><Tag key={key} color={v.state==='valid'&&(v.allowImplemented||v.features[key])?'green':'default'}>{labels[key]??key}：{v.state==='valid'&&(v.allowImplemented||v.features[key])?'已启用':'未启用'}</Tag>)}</Space>
   {v.importAllowed?<Button type="primary" onClick={()=>{setError('');setOpen(true)}}>导入签名许可</Button>:<Typography.Text type="secondary">本地模式由部署管理员配置；此页面不修改信任公钥或切换许可模式。</Typography.Text>}
  </Space>}
  <Modal title="导入本项目签名许可" open={open} onCancel={()=>{if(!busy){setOpen(false);form.resetFields()}}} onOk={()=>void save()} confirmLoading={busy} destroyOnHidden>
   <Alert type="info" showIcon title="只接受本实例和已配置公钥签发的许可" description="导入需要管理员身份复核。并发变更会被拒绝，请刷新后重试。许可正文和复核密码不会写入审计日志。"/>
   {error&&<Alert type="error" title={error}/>}
   <Form form={form} layout="vertical" preserve={false}>
    <Form.Item label="签名许可" name="token" rules={[{required:true},{max:32768}]}><Input.TextArea rows={5} autoComplete="off"/></Form.Item>
    <Form.Item label="管理员密码" name="password" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
    <Form.Item label="动态验证码（已启用 TOTP 时必填）" name="totp"><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
   </Form>
  </Modal>
 </Card>
}
