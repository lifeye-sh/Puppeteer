import {useState} from 'react';
import {Link} from 'react-router-dom';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Space,Tag,Typography} from 'antd';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import {type ConfigurationStatus,displayedDimension,operationStages,operationStates} from './configuration-status-model';

const routeDetails={scope:'检查已登记引用的实际连接路径、凭据与目标 TCP/TLS；不登录目标，不回退直连，不自动安装远程服务。',path:'/agent-gateway',action:'接入资源与安装任务'};
export const dependencyDetails:Record<string,{scope:string;path:string;action:string}>={
 oauthServer:{scope:'只读检查签名密钥、固定 Issuer、公开 HTTPS 发现文档和 JWKS；不生成密钥或令牌。',path:'/integration-settings?section=oauthServer',action:'OIDC 服务端与客户端'},
 'native-client':{scope:'检查客户端登记、发起人许可和当前节点协议入口；具体资产权限及完整会话仍需逐次验证。',path:'/info',action:'个人账号与原生接入'},
 forward:{scope:'检查精确目标规则、固定目标 TCP 和 SSH 网关；关闭后等待转发记录结束，不新增公开端口。',path:'/integration-settings?section=forward',action:'受控转发规则'},
 databasePolicy:{scope:'检查 MySQL 独立 DML 凭据及权限模式；不执行写 SQL。关闭后核验写会话记录结束；未知事务结果仍须在目标库核验。',path:'/database-asset',action:'数据库资产与凭据'},
 'agent-gateway':routeDetails,'ssh-jump':routeDetails,'gateway-group':routeDetails,'network-proxy':routeDetails,
 'backup-destination':{scope:'只读检查连接、证书或主机指纹及目标路径；写入、摘要回读和清理须点击目标的显式测试。',path:'/system-event',action:'备份复制与读写验证'},
 'acme-order':{scope:'检查账号密钥、HTTPS 目录和签名 DNS 查询；不注册账号、写 TXT 或签发证书。实际挑战与清理以签发日志为准。',path:'/certificate',action:'证书订单与签发日志'},
 aiModel:{scope:'检查出站策略、代理、TCP/TLS、许可与发起人的预算；不发送 API Key 或调用模型，不产生模型用量。真实调用仍会重查预算及资产权限。',path:'/ai',action:'模型与显式调用验证'},
 mail:{scope:'检查 DNS/TCP、TLS 和 SMTP 认证；不发送邮件，发件权限及投递需要显式测试。',path:'/setting',action:'接收人、规则与测试投递'},
 ldap:{scope:'检查 TLS、服务绑定、Base DN、目录查询与身份映射；不创建用户或执行同步。',path:'/identity-settings?tab=ldap',action:'目录与用户登录验证'},
 oidcProvider:{scope:'检查发现文档、协议及凭据配置；实际客户端认证、回调和身份映射须验证授权。',path:'/identity-settings?tab=oidc',action:'身份源与验证授权'},
 wecomProvider:{scope:'检查应用凭据；提供方可信域名、回调和用户身份仍需验证授权。',path:'/identity-settings?tab=oidc',action:'身份源与验证授权'},
 passkey:{scope:'服务端检查 HTTPS 来源、RP ID 与部署白名单；当前浏览器支持及设备注册单独判断。',path:'/info',action:'个人账号与设备注册'},
};

export default function BackendDependencyCheck({feature}:{feature:string}){
 const {account}=useBackendRuntime();const cache=useQueryClient();const [busy,setBusy]=useState(false),[error,setError]=useState('');
 const resource=feature.includes(':');const prefix=feature.split(':')[0];
 const query=useQuery({queryKey:resource?['configuration-operations',feature]:['configuration-operations'],queryFn:()=>requests.get<ConfigurationStatus&{evidence?:string}>('/settings/operations'+(resource?'/'+encodeURIComponent(feature):''),{errorMode:'local'}),refetchInterval:5000,retry:false});
 const row=query.data?.items.find(i=>i.id===feature);const task=row?.lastCheck;const details=dependencyDetails[feature]??dependencyDetails[prefix]??dependencyDetails[({'notification-channel':'mail','ldap-directory':'ldap','oidc-provider':'oidcProvider','ai-profile':'aiModel','backup-destination':'backup-destination','acme-order':'acme-order'} as Record<string,string>)[prefix]];
 const canCheck=account.type==='admin'&&!!account.actions?.includes('identity.manage')&&(prefix!=='backup-destination'||!!account.actions?.includes('backup.manage'));
 const act=async(cancel=false,verify=false)=>{if(!query.data)return;setBusy(true);setError('');try{
  const path=cancel?`/api/settings/operation-tasks/${task?.id}/cancel`:`/api/settings/operations/${feature}/${verify?'verify':'preflight'}`;
  const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json','If-Match':`"settings-${query.data.revision}"`},body:JSON.stringify(resource&&!cancel?{evidence:query.data.evidence}:{})});
  if(!response.ok){const body=await response.json();throw Error(body.message??'依赖预检操作失败')}
  await Promise.all(['configuration-operations','configuration-operation-tasks'].map(key=>cache.invalidateQueries({queryKey:[key]})));
 }catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 if(!details)return null;
 return <Space orientation="vertical" style={{width:'100%',marginBottom:16}}>
  <Typography.Text type="secondary">针对已保存配置和当前节点。{details.scope}</Typography.Text>
  {(query.error||error)&&<Alert type="error" title={error||'依赖状态刷新失败，原记录已过期'} description={query.error?.message}/>}
  {row&&<><Space wrap>{([['期望',row.desired],['配置应用',row.applied],['依赖',row.dependencies],['实际业务',row.service]] as const).map(([label,value])=>{const d=displayedDimension(value,!!query.error);return <Tag key={label} color={d.state==='ready'?'green':d.state==='blocked'?'red':'default'}>{label}：{operationStates[d.state]??d.state}</Tag>})}</Space><Typography.Text type="secondary">{displayedDimension(row.dependencies,!!query.error).reason}</Typography.Text></>}
  {task&&<Alert type={task.state==='failed'?'warning':'info'} title={`最近${task.mode==='verify'?'读写验证':'预检'}：${operationStates[task.state]??task.state} · ${operationStages[task.stage]??task.stage}`} description={<>{task.checks.map(c=><div key={c.code}>{c.summary}{c.state==='blocked'?`；${c.remediation??''}`:''}</div>)}<Typography.Text type="secondary">配置版本 {task.revision} · {new Date(task.updated).toLocaleString()}</Typography.Text></>}/>}
  {feature==='passkey'&&<Typography.Text>当前浏览器：{window.isSecureContext&&typeof window.PublicKeyCredential!=='undefined'?'具备 WebAuthn 基础支持，仍需实际注册验证':'缺少安全上下文或 WebAuthn 支持'}</Typography.Text>}
  <Space wrap>{canCheck&&<Button disabled={!row?.checkSupported||!!query.error||busy||task?.state==='running'} loading={busy} onClick={()=>void act()}>依赖预检</Button>}{canCheck&&prefix==='backup-destination'&&<Button disabled={!row?.checkSupported||!!query.error||busy||task?.state==='running'} onClick={()=>void act(false,true)}>读写、摘要与清理验证</Button>}{task?.mode==='verify'&&task.state==='running'&&<Typography.Text>正在验证并清理隔离测试对象，此阶段不可取消。</Typography.Text>}{canCheck&&task?.state==='running'&&task.mode!=='verify'&&<Button disabled={busy} onClick={()=>void act(true)}>取消预检</Button>}{(feature==='passkey'||account.actions?.includes('identity.manage'))&&<Link to={details.path}>{details.action}</Link>}</Space>
  {row&&!row.checkSupported&&<Alert type="warning" title="当前服务端尚未支持此项统一预检，请使用专项检测或升级匹配版本"/>}
 </Space>;
}
