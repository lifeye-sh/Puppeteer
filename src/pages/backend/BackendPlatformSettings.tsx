import {useState} from 'react';
import {useSearchParams} from 'react-router-dom';
import {Alert,Card,Descriptions,Input,Space,Table,Tabs,Tag,Typography} from 'antd';
import {AppstoreOutlined,SafetyOutlined,DatabaseOutlined,HistoryOutlined,KeyOutlined,CloudSyncOutlined,DashboardOutlined} from '@ant-design/icons';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import BackendConfiguration from './BackendConfiguration';
import BackendOAuthKeys from './BackendOAuthKeys';
import BackendNativeForwardSettings from './BackendNativeForwardSettings';
import BackendFederationConfiguration from './BackendFederationConfiguration';
import BackendConfigurationHistory from './BackendConfigurationHistory';
import BackendAuditRetention from './BackendAuditRetention';
import BackendEntitlements from './BackendEntitlements';
import {useConfiguration} from './useConfiguration';
import {configurationStates} from './configuration-model';
import './backend-settings.css';

function RuntimeStatus(){
 const {runtime,capabilities}=useBackendRuntime();const [search,setSearch]=useState('');
 const features=Object.entries(capabilities.features).map(([key,value])=>({key,...value})).filter(v=>`${v.key} ${v.reason??''}`.toLowerCase().includes(search.toLowerCase()));
 const states:Record<string,string>={available:'已开放',not_implemented:'尚未实现',not_configured:'尚未配置',not_entitled:'许可未开放',unavailable:'不可用'};
 return <div className="nt-settings-stack">
  <Card title="服务实例"><Descriptions column={{xs:1,sm:2}} items={[
   {key:'release',label:'发布版本',children:runtime.release},{key:'instance',label:'实例 ID',children:runtime.instanceId},
   {key:'schema',label:'数据库版本',children:runtime.schemaVersion},{key:'capabilities',label:'能力版本',children:runtime.capabilitiesRevision},
  ]}/></Card>
  <Card title="协议能力"><p className="nt-settings-description">协议可用性同时取决于部署配置、许可、用户授权与远端服务。</p><Table rowKey="protocol" size="small" pagination={false} scroll={{x:750}} dataSource={Object.entries(capabilities.protocols??{}).map(([protocol,flags])=>({protocol,...flags}))} columns={[
   {title:'协议',dataIndex:'protocol',render:v=><strong>{String(v).toUpperCase()}</strong>},
   ...['connect','recording','observe','share','files','clipboard','resize'].map((key,i)=>({title:['连接','录屏','观察','分享','文件','剪贴板','调分辨率'][i],dataIndex:key,render:(v:boolean)=><Tag variant="filled" color={v?'green':'default'}>{v?'支持':'未开放'}</Tag>})),
  ]}/></Card>
  <Card title="功能开放状态" extra={<Input.Search allowClear placeholder="查找功能标识或原因" aria-label="查找功能" value={search} onChange={e=>setSearch(e.target.value)} style={{width:230,maxWidth:'100%'}}/>}>
   <Table size="small" rowKey="key" scroll={{x:520}} dataSource={features} pagination={{pageSize:8,showSizeChanger:false,showTotal:n=>`共 ${n} 项`}} columns={[
    {title:'功能标识',dataIndex:'key'},{title:'状态',dataIndex:'state',render:v=><Tag color={v==='available'?'green':'default'}>{states[v]??v}</Tag>},{title:'说明',dataIndex:'reason',render:v=>v||'—'},
   ]}/>
  </Card>
 </div>;
}

export default function BackendPlatformSettings(){
 const {account,capabilities}=useBackendRuntime();const [params,setParams]=useSearchParams();
 const read=!!account.actions?.includes('settings.read'),revisions=read&&capabilities.features['system.settings.revisions']?.state==='available';
 const current=useConfiguration(revisions);
 const sections=[
  {key:'branding',label:'品牌设置',note:'名称、Logo 与页脚',icon:<AppstoreOutlined/>,show:revisions,children:<BackendConfiguration section="branding"/>},
  {key:'login',label:'登录安全',note:'会话与失败保护',icon:<SafetyOutlined/>,show:revisions,children:<BackendConfiguration section="login"/>},
  {key:'postgres',label:'数据库参数',note:'查询限制与受控写入',icon:<DatabaseOutlined/>,show:revisions,children:<BackendConfiguration section="postgres"/>},
  {key:'listeners',label:'网关监听',note:'接入端口与证书',icon:<CloudSyncOutlined/>,show:revisions,children:<BackendConfiguration section="listeners"/>},
  {key:'forward',label:'SSH 转发',note:'授权资产与精确目标',icon:<CloudSyncOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['ssh-server-port-forwarding-enabled']!==undefined,children:<Card title="受控端口转发"><Typography.Paragraph>按资产配置可连接的主机和端口。每次连接仍检查用户权限，域名由服务端解析并固定地址。</Typography.Paragraph><BackendNativeForwardSettings/></Card>},
  {key:'network',label:'客户端 IP',note:'来源识别与可信代理',icon:<CloudSyncOutlined/>,show:revisions&&current.data?.values['ip-extractor']!==undefined,children:<BackendConfiguration section="network"/>},
  {key:'mail',label:'平台邮件',note:'SMTP 与加密凭据',icon:<CloudSyncOutlined/>,show:revisions&&current.data?.values['mail-enabled']!==undefined,children:<BackendConfiguration section="mail"/>},
  {key:'oidcProvider',label:'OIDC 身份源',note:'默认外部身份提供方',icon:<KeyOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['oidc-enabled']!==undefined,children:<BackendFederationConfiguration kind="oidc"/>},
  {key:'wecomProvider',label:'企业微信身份源',note:'默认企业应用登录',icon:<SafetyOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['wechat-work-enabled']!==undefined,children:<BackendFederationConfiguration kind="wecom"/>},
  {key:'oauthServer',label:'OIDC 服务端',note:'原生入口与固定身份',icon:<KeyOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['oidc-server-enabled']!==undefined,children:<BackendConfiguration section="oauthServer"/>},
  {key:'oauthKeys',label:'OAuth 密钥',note:'签名与 HMAC 轮换',icon:<KeyOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['oidc-server-signing-key']!==undefined,children:<BackendOAuthKeys/>},
  {key:'oauthLifetime',label:'OAuth 时长',note:'授权码与令牌有效期',icon:<KeyOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['oidc-server-access-token-ttl']!==undefined,children:<BackendConfiguration section="oauthLifetime"/>},
  {key:'passkey',label:'Passkey',note:'通行密钥与 HTTPS 来源',icon:<KeyOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['passkey-enabled']!==undefined,children:<BackendConfiguration section="passkey"/>},
  {key:'ldap',label:'LDAP 目录',note:'默认身份源与属性映射',icon:<SafetyOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['ldap-enabled']!==undefined,children:<BackendConfiguration section="ldap"/>},
  {key:'ai',label:'AI 策略',note:'工具开关与提示词',icon:<AppstoreOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['llm-enabled']!==undefined,children:<BackendConfiguration section="ai"/>},
  {key:'access',label:'远程会话',note:'空闲超时与连接控制',icon:<SafetyOutlined/>,show:revisions&&current.data?.values['access-max-idle-second']!==undefined,children:<BackendConfiguration section="access"/>},
  {key:'watermark',label:'会话水印',note:'访问标识与显示样式',icon:<SafetyOutlined/>,show:revisions,children:<BackendConfiguration section="watermark"/>},
  {key:'resources',label:'资源默认值',note:'存储容量与证书期限',icon:<AppstoreOutlined/>,show:revisions,children:<BackendConfiguration section="resources"/>},
  {key:'recording',label:'会话录屏',note:'新会话录制与审计',icon:<HistoryOutlined/>,show:revisions&&account.type==='admin'&&current.data?.values['recording-enabled']!==undefined,children:<BackendConfiguration section="recording"/>},
  {key:'retention',label:'日志保留',note:'保留周期与保护',icon:<HistoryOutlined/>,show:read,children:<BackendAuditRetention/>},
  {key:'license',label:'许可配额',note:'许可状态与用量',icon:<KeyOutlined/>,show:read&&account.type==='admin'&&capabilities.features['system.entitlements']?.state==='available',children:<BackendEntitlements/>},
  {key:'publish',label:'配置发布',note:'节点反馈与版本历史',icon:<CloudSyncOutlined/>,show:revisions,children:<BackendConfigurationHistory/>},
  {key:'runtime',label:'运行状态',note:'服务与协议能力',icon:<DashboardOutlined/>,show:true,children:<RuntimeStatus/>},
 ].filter(v=>v.show);
 const active=sections.some(s=>s.key===params.get('section'))?params.get('section')!:sections[0].key;
 return <section className="nt-page nt-platform-settings">
  <header className="nt-settings-header"><div><Typography.Title level={3}>平台设置</Typography.Title><Typography.Text type="secondary">按功能管理平台配置，查看变更历史与应用结果。</Typography.Text></div>
   {revisions&&<div className="nt-settings-summary"><span>配置版本 <strong>{current.data?.id??'—'}</strong></span><Tag color={current.error?'red':current.data?.state==='effective'?'green':'orange'}>{current.error?'读取失败':configurationStates[current.data?.state??'']??'读取中'}</Tag><span>{current.data?.nodes.filter(n=>n.online).length??'—'} 个在线节点</span></div>}
  </header>
  {current.error&&<Alert type="error" showIcon title="无法读取当前配置" description={current.error.message}/>}
  {current.data?.securityPending&&<Alert type="warning" showIcon title="安全策略放宽正在等待节点确认" description="收紧部分已经执行；在相关在线节点应用成功前，仍保留原有较严格的登录、MFA、密码及可信代理限制。详情见配置发布。"/>}
  <Tabs className="nt-settings-tabs" tabPlacement="start" activeKey={active} onChange={key=>{const next=new URLSearchParams(params);next.set('section',key);setParams(next)}} destroyOnHidden={false} items={sections.map(s=>({key:s.key,label:<span className="nt-settings-nav-item">{s.icon}<span><strong>{s.label}</strong><small>{s.note}</small></span></span>,children:s.children}))}/>
  <Space className="nt-settings-footer"><Typography.Text type="secondary">配置按对应生效时机应用；保存后可在“配置发布”中查看节点反馈。</Typography.Text></Space>
 </section>;
}

