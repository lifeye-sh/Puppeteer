import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Alert,Button,Card,Checkbox,Collapse,Form,Input,InputNumber,Select,Switch,Tabs,Tag,Typography,Modal as AntModal} from 'antd';
import {EditOutlined} from '@ant-design/icons';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';
import BackendLogoInput from './BackendLogoInput';
import BackendPasswordPolicyInput from './BackendPasswordPolicyInput';
import BackendLDAPMappingInput from './BackendLDAPMappingInput';
import BackendOriginListInput from './BackendOriginListInput';
import {useConfiguration,useConfigurationSchema} from './useConfiguration';
import {configurationPatch,displayConfiguration,propertyGroup,type Configuration,type Definition} from './configuration-model';

const groups={
 oauthServer:{title:'OIDC 与原生客户端',description:'管理平台作为身份提供方的入口与固定 Issuer。',note:'第三方 OIDC 与原生 PKCE／SSH 开关互相独立。关闭立即生效，开启等待在线节点确认；修改入口使旧令牌及授权失效。Issuer 必须是允许的 HTTPS 来源，首次保存后不可更换。修改需管理员密码及已绑定 MFA 复核。签名与 HMAC 密钥可在“OAuth 密钥”中导入或自动生成。'},
 recording:{title:"会话录屏",description:"决定新会话是否保存终端与桌面输出。",note:"默认开启。保存需要管理员密码及已绑定MFA复核；关闭等待在线节点确认，重新开启立即约束新会话。已创建会话沿用原决定，既有录像不会删除。访问记录与SSH命令审计始终保留；未录屏会话不提供依赖录屏流的观察、分享、回放和下载。原生RDP仍在内存检查首帧与传输，不编码或保存视频。"},
 oauthLifetime:{title:'OAuth 令牌时长',description:'配置平台签发授权码、访问令牌、ID Token 和刷新令牌的有效期。',note:'所有时长单位为秒，仅作用于新签发结果。已发出的令牌不因修改而续期；刷新令牌轮换不延长原令牌家族的到期时间。原生客户端访问令牌最多300秒、刷新家族最多7天；第三方客户端使用这里的时长。此页面不打开第三方或原生客户端登录入口。修改需要管理员密码及已绑定 MFA 复核。'},
 passkey:{title:'Passkey',description:'管理登录与注册开关、RP ID 和允许的 HTTPS 来源。',note:'空 RP ID 与空来源列表同时保存时沿用部署配置；未配置有效身份时仍不开放 Passkey。新来源必须同时包含在部署的 API Origin 白名单内。每次变更需管理员密码与已绑定 MFA 复核；关闭立即生效，重新开启等待在线节点确认。更换 RP ID 后原凭据不能用于新身份，需重新注册；原凭据仍保留。不能移除最后一个管理员登录方式。完成注册与登录前重查当前配置，现有登录会话不因关闭开关自动退出。'},
 access:{title:'远程会话',description:'控制远程资产接入二次验证与连接空闲时长。',note:'开启二次验证后，浏览器终端与桌面、原生 OAuth 连接和 SSH 公钥入口均需动态口令。验证绑定当前登录会话或原生授权，复用时间默认5分钟；0表示每次连接重新验证。未绑定用户须先在个人账号中绑定。修改配置清除旧接入验证，关闭等待在线节点确认；已有连接不因验证时间到期自动退出。数据库、Web代理和后台任务使用各自身份验证与授权。保存需管理员密码及已绑定 MFA 复核。空闲超时默认900秒，-1表示不限空闲；独立的授权租约和最长连接时间仍有效。SSH/Telnet输入、网页桌面键鼠和发送剪贴板计为活动，心跳、窗口大小变化、远端输出和观察者不延长。受控TCP转发以客户端发送业务数据计时；原生RDP当前为只读画面，不产生输入活动。此项不控制数据库、独立SFTP文件请求或登录会话。收紧在下一次续租（约2秒）断开超时连接，放宽等待在线节点确认。'},
 ldap:{title:'平台默认 LDAP',description:'配置一个默认身份目录，既有独立目录保持原有设置。',note:'仅支持 LDAPS 或强制 StartTLS。首次保存连接后，目录地址、Base DN 与稳定身份属性锁定；更换身份域请新建独立目录。开启登录或绑定须等待在线节点确认，关闭立即生效。每次修改均需管理员密码及已绑定 MFA 复核。同步先预览，超限整批拒绝；新用户默认停用，目录邮箱不自动验证或用于找回。目录检测、绑定和同步在 LDAP 目录管理中操作。'},
 aiModel:{title:'平台默认模型',description:'单独管理一个默认模型配置，既有独立模型保持原有设置。',note:'初始停用。模型地址默认要求HTTPS；仅可信内网模型可明确启用HTTP/内网访问，连接前重新校验并固定目标IP。API Key留空或掩码保持原值；替换和清除需密码与已绑定MFA复核。修改模型或代理地址时必须重新填写或明确清除密钥。输出上限64–4096 Token，价格由管理员按服务协议填写，平台不自动查询价格。'},
 ai:{title:'AI 策略',description:'统一管理助手、终端工具与命令审计分析。',note:'模型连接与 API Key 在 AI 助手管理中配置。开启工具不授予资产权限，也不跳过执行确认、命令规则、只读策略、预算和许可检查。提示词仅补充平台内置规则。保存后新任务读取新策略，工具执行仍实时复核；关闭助手会中止运行与排队任务。'},
 captcha:{title:'登录验证码',description:'控制新发起登录的拼图验证。',note:'默认开启。修改须管理员密码及已绑定的新鲜 MFA 复核。开启立即约束新的认证请求；关闭须等待在线节点确认。未消费的拼图和验证证明在变更后失效，现有登录会话不受影响；已发起的外部登录和动态口令流程按原有效期完成。隔离恢复重新开启。'},
 loginPolicy:{title:'密码登录入口',description:'控制是否允许使用平台本地密码登录。',note:'关闭前，当前管理员须在15分钟内用已绑定的备用方式完成登录及MFA。系统阻止移除最后一个已验证的管理员登录方式。重新开启须等待在线节点确认；本机应急恢复命令可用于身份服务故障恢复。此开关不取消敏感操作的密码复核。'},
 network:{title:'客户端 IP',description:'选择 API 请求的来源 IP 提取方式和可信反向代理。',note:'默认 direct 使用直接连接地址。仅当直接连接来自可信网段时才接受指定请求头；x-forwarded-for 从右向左剥离可信代理，其他头只接受单个 IP。重复、非法或过长头回退到直接连接地址。放宽可信网段等待节点确认；网站流量仍使用网站网关的独立可信代理设置。不要把不受控的客户端网段加入可信列表。'},
 mail:{title:'平台邮件',description:'统一配置默认 SMTP 渠道，并在通知配置中绑定接收人和规则。',note:'独立创建的其他通知渠道保持不变。开启隐式TLS一般使用465端口；关闭时强制STARTTLS，不允许明文降级或跳过证书校验。内网服务可提供CA证书。密码仅返回掩码，留空或保留掩码表示保持原值；替换和清除需要管理员密码复核及已绑定的TOTP。配置随下一次邮件或验证消息投递使用，隔离恢复后保持禁用。'},
 databasePolicy:{title:'数据库原生写入',description:'控制 MySQL 原生客户端的 DML 写入。',note:'默认阻止写入。关闭后，仅配置独立原生 DML 凭据且用户拥有“数据库原生受控 DML 写入”权限的新连接可以写入；已连接只读会话不会升级。仍拒绝 DDL、存储过程、文件操作和跨数据库写入。重新阻止将关闭写连接，未提交事务由目标数据库回滚，网络中断导致无法确认的结果不会自动重试。PostgreSQL 保持只读；SQL 工单独立审批执行。'},
 listeners:{title:'网关监听',description:'配置管理 HTTP、网站 TLS、SSH、数据库及原生 RDP 接入。',note:'地址留空沿用服务部署配置；部署也未配置时不监听。地址示例 0.0.0.0:2022 或 127.0.0.1:13306，仅接受 IP 与端口。保存后先准备新端口及证书，成功后切换，新连接采用新配置，旧连接自然结束。准备失败保留旧监听并暂停新连接，请查看配置发布并重试。容器端口映射及外部防火墙需与监听地址匹配；SSH 用户仍须获准使用原生客户端。关闭监听不主动踢出已建立连接。'},
 password:{title:'密码安全',description:'管理本地密码强度和有效期。',note:'强度规则仅约束新设定的密码，推荐至少12字符并检查账号包含、常见弱密码和重复字符。有效期默认不限；启用后到期会话须验证原密码及已绑定的 TOTP，再修改密码。LDAP、数据库与远端资产的密码由各自系统管理。'},
 watermark:{title:'会话水印',description:'在网页终端、远程桌面和只读观察画面上显示访问标识。',note:'仅新建会话采用新配置，已建立连接保留原快照。水印是网页显示标识，不会写入远端桌面或录屏文件，也不覆盖原生客户端。不能替代权限控制和审计。'},
 resources:{title:'资源默认值',description:'设置个人存储、客户端证书期限和 Agent 安装默认地址。',note:'容量按 Byte 保存，默认 5 GiB；仅首次创建个人空间时应用，已有配额保持不变。0 表示新空间没有写入容量。证书默认90天，显式期限须为1–825天且不超过签发CA有效期。Agent 地址仅用于新安装和注册说明，已有设备不会重新指向其他服务器。'},
 branding:{title:'品牌与外观',description:'统一管理平台名称、Logo 与页脚信息。',note:'文字按纯文本展示，不支持 HTML。Logo 上传后需要保存配置才会生效；恢复默认不删除历史版本中的图片。'},
 sessions:{title:'登录会话',description:'控制登录有效期与每人同时登录数量。',note:'时长从新登录签发时开始，不随页面操作延长。已有会话不修改，MFA 临时会话仍为5分钟且不占正式登录名额。不同登录方式共用数量限制，原生 OAuth 和数据库连接另行管理。浏览器会话恢复可能保留会话 Cookie，关闭窗口不保证立即退出。'},
 locks:{title:'失败登录保护',description:'设置密码校验失败的统计窗口与锁定策略。',note:'成功密码验证或无新失败超过统计窗口后清除账户计数。总开关关闭会暂停账户/IP锁定检查与计数，重新开启后未到期锁定仍有效。锁定时长为0只停止新锁定；已锁定账户需到期或手动解锁。IP仅统计本地与LDAP密码失败，成功登录不清除共享IP计数；验证码、限流和其他认证方式仍有独立保护。'},
 postgres:{title:'PostgreSQL 查询限制',description:'限制新建数据库连接中的查询时间与结果规模。',note:'保存仅影响新连接，现有连接保留原参数。降低限制后，新连接中的大查询可能被中止。当前 PostgreSQL 代理为只读预览；凭据、目标 TLS 与连接诊断在数据库资产中管理。'},
} as const;
type EditorGroup=keyof typeof groups;
const fieldHints:Record<string,string>={
 'http-server-addr':'留空沿用部署地址。修改后需同步反向代理和端口映射；管理端口不可通过开关关闭。进程重启先在部署地址提供恢复入口，成功应用配置后再切换。',
 'website-server-addr':'网站代理的 TLS 接入地址；证书按网站域名选择，沿用各网站的证书和访问策略。',
 'native-rdp-addr':'仅已部署原生 RDP Worker 的服务可启用。普通部署限本机回环；显式本机容器 profile 使用宿主回环端口映射。',
 'native-rdp-public-address':'客户端实际访问的域名或 IP 加端口，必须匹配原生 RDP 服务证书。留空沿用部署配置。',
 'passkey-domain':'例如 backend.localhost 或 example.com；不是 URL。留空时来源也必须留空，以沿用部署配置。',
 'passkey-origins':'每行一个精确 HTTPS 来源，最多16个；不含路径、通配符、查询参数。',
 'access-max-idle-second':'填写 1–86400 秒；-1 表示不限空闲，不能填写 0。默认 900 秒。',
 'ldap-url':'ldaps:// 或 ldap://（强制 StartTLS）。保存后不可更换身份域；请确认地址再保存。',
 'ldap-user':'完整服务账号 DN。更换服务账号时须重新填写密码，或明确清除密码并关闭登录与绑定。',
 'ldap-password':'留空或掩码保持原值；明确清除前须关闭平台 LDAP 登录与绑定。',
 'ldap-base-dn':'目录根 DN，例如 dc=example,dc=com；首次创建后不可更换。',
 'ldap-identity-attribute':'OpenLDAP 使用 entryUUID，Active Directory 使用 objectGUID；首次创建后不可更换。',
 'ldap-user-search-size-limit':'1–10000；读取数量超过上限时拒绝整个预览，不导入部分结果。',
 'ldap-user-property-mapping':'账号属性必填；昵称仅用于新账号，邮箱仅作预览，不合并同名或同邮箱账号。',
 'ldap-ca-pem':'内部 CA 公钥证书 PEM；留空使用系统信任库，不接受私钥或跳过证书验证。',
 'agent-gateway-endpoint':'Agent 实际可访问的后端 HTTPS 根地址，不含 /api、查询串或凭据。留空时每次安装手动填写；已有设备地址不变。',
 'llm-base-url':'API 根地址，例如 https://model.example/v1；不含凭据、查询串或片段。',
 'llm-proxy-url':'支持 HTTP/HTTPS CONNECT、SOCKS5/SOCKS5h（需明确端口）；不得含用户名或密码。两跳地址均由平台解析和校验，SOCKS5h 同样固定目标 IP。',
 'llm-temperature':'-1 表示不发送此参数，使用模型默认；指定时为0–2。',
 'llm-allow-private':'只对可信内网模型开启；仍禁止链路本地、组播和未指定地址。',
 'llm-api-key':'空值或掩码保持，修改连接地址时须重新填写或明确清除密钥。',
 'ip-extractor':'direct、x-forwarded-for、x-real-ip，或合法的小写自定义头名称；禁止认证和 Cookie 等敏感头。',
 'ip-trust-list':'逗号分隔 IPv4 / IPv6 CIDR，最多1024项。例如 127.0.0.1/32,::1/128；使用请求头时必须配置。',
 'ssh-server-disable-password-auth':'默认开启，仅允许原生 SSH 密钥认证。关闭后可用平台本地密码登录（不是资产密码），仍受本地密码登录总开关、账户锁定、有效期和 MFA 约束；LDAP 密码不支持。禁用立即阻止密码连接并终止已有密码会话，允许密码需等待在线节点确认。',
 'ssh-server-private-key':'用于 SSH 网关身份，不是资产登录密钥。支持 Ed25519、ECDSA、至少2048位RSA；不接受密码保护或DSA密钥。轮换只影响新握手，客户端需重新核对主机指纹。',
 'mail-password':'留空或保留掩码表示不修改；需要移除时使用下方“清除已保存的 SMTP 密码”。',
 'mail-use-ssl':'关闭时使用强制 STARTTLS；不会降级为明文连接。',
 'mail-insecure-skip-verify':'平台始终验证证书，不支持跳过验证；内网请配置 CA。',
 'mail-ca-pem':'只接受 PEM CA 证书，不含私钥。留空使用系统信任库。',
 'db-proxy-certificate-id':'可留空使用平台自动生成的证书。自选证书必须包含代理公开主机名及私钥，未过期且未吊销；切换后客户端需要信任新证书。',
 'ssh-server-addr':'留空继承部署设置。格式为 IP:端口，例如 0.0.0.0:2022。',
 'db-proxy-addr':'留空继承部署设置。格式为 IP:端口，例如 0.0.0.0:13306。',
 'postgres-proxy-addr':'留空继承部署设置。格式为 IP:端口，例如 0.0.0.0:15432。',
 'password-expiration-period':'-1 表示不过期，1–3650 天；0 无效。已有本地密码到期后，下一次访问需要改密；升级前密码从本次升级时开始计时。外部身份提供方独立管理其密码期限。',
 'watermark-content':'纯文字，可用 {user}、{assetUser}、{asset} 分别插入平台用户、资产账户和资产名称。不支持 HTML。',
 'watermark-font-color':'支持 #RRGGBB、#RRGGBBAA、rgb() 或 rgba()；例如 rgba(128, 128, 128, 0.3)。',
 'login-force-totp-enabled':'开启后，未绑定的用户需先绑定验证器；未留下 TOTP 验证记录的现有浏览器会话需重新验证。原生 SSH 公钥登录同样要求 TOTP。已有个人 API Key 的非交互调用不使用此登录开关。',
 'login-lock-ip-duration':'0 表示不锁定；共享代理或 NAT 环境请谨慎开启。',
 'login-lock-account-duration':'0 表示不产生新锁定，已有锁定不会提前解除。',
 'login-session-count-limit':'仅在“限制每人登录会话数”开启时生效。',
};
export default function BackendConfiguration({section='branding'}:{section?:'branding'|'login'|'postgres'|'resources'|'watermark'|'listeners'|'mail'|'network'|'ai'|'ldap'|'access'|'passkey'|'oauthLifetime'|'recording'|'oauthServer'}){
 const {account}=useBackendRuntime();const canWrite=!!account.actions?.includes('identity.manage');const cache=useQueryClient();
 const current=useConfiguration(),schema=useConfigurationSchema();
 const [editing,setEditing]=useState<{snapshot:Configuration;group:EditorGroup}>(),[error,setError]=useState(''),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false);
 const [form]=Form.useForm();const [confirm,confirmHolder]=AntModal.useModal();const definitions=schema.data?.properties??[];
 const [mailTab,setMailTab]=useState('connection');const [aiTab,setAITab]=useState('switches');
 const passwordType=Form.useWatch('password-strength-type',form);
 const ldapPassword=Form.useWatch('ldap-password',form);const modelKey=Form.useWatch('llm-api-key',form);const mailPassword=Form.useWatch('mail-password',form),sshPrivateKey=Form.useWatch('ssh-server-private-key',form),clearMailPassword=Form.useWatch('_clearSecret',form);
 const secretKey=editing?.group==='ldap'?'ldap-password':editing?.group==='aiModel'?'llm-api-key':editing?.group==='mail'?'mail-password':editing?.group==='listeners'&&definitions.some(p=>p.key==='ssh-server-private-key')?'ssh-server-private-key':undefined;
 const secretValue=secretKey==='ldap-password'?ldapPassword:secretKey==='llm-api-key'?modelKey:secretKey==='mail-password'?mailPassword:sshPrivateKey;
 const secretChanged=!!secretKey&&(clearMailPassword||!!secretValue&&secretValue!=='*****');
 const certificates=useQuery({queryKey:['configuration-certificates'],enabled:editing?.group==='listeners',queryFn:()=>requests.get<Array<{id:string;name:string;kind:string;revokedAt?:string;notAfter:string}>>('/admin/certificates',{errorMode:'local'}),retry:false});
 const loginPolicy=useQuery({queryKey:['login-policy'],enabled:editing?.group==='loginPolicy',queryFn:()=>requests.get<{passwordEnabled:boolean;canDisable:boolean}>('/admin/login-policy',{errorMode:'local'}),retry:false});
 const needsProof=secretChanged||editing?.group==='captcha'||editing?.group==='databasePolicy'||editing?.group==='access'||editing?.group==='oauthServer'||editing?.group==='recording'||editing?.group==='loginPolicy'||editing?.group==='ldap'||editing?.group==='passkey'||editing?.group==='oauthLifetime';
 const selected:EditorGroup[]=section==='login'?[...(definitions.some(p=>p.key==='disable-password-login')?['loginPolicy' as const]:[]),...(definitions.some(p=>p.key==='login-captcha-enabled')?['captcha' as const]:[]),'sessions','locks','password']:section==='postgres'?['postgres',...(definitions.some(p=>p.key==='db-proxy-block-dml')?['databasePolicy' as const]:[])]:section==='ai'?['ai',...(definitions.some(p=>p.key==='llm-base-url')?['aiModel' as const]:[])]:[section];
 const open=(group:EditorGroup)=>{if(!current.data)return;form.resetFields();form.setFieldsValue(configurationPatch(definitions,group,current.data.values));setError('');setMailTab('connection');setAITab('switches');setEditing({snapshot:current.data,group})};
 const close=()=>{if(busy)return;const discard=()=>{form.resetFields();setEditing(undefined)};const changed=clearMailPassword||editing&&definitions.filter(p=>propertyGroup(p.key)===editing.group).some(p=>form.getFieldValue(p.key)!==editing.snapshot.values[p.key]);if(changed){confirm.confirm({title:'放弃尚未保存的修改？',content:'当前填写内容尚未提交，关闭后需要重新填写。',okText:'放弃修改',cancelText:'继续编辑',onOk:discard})}else discard()};
 const save=async()=>{
  if(!editing)return;setBusy(true);setError('');
  try{
   const values=await form.validateFields();const patch=configurationPatch(definitions,editing.group,values);
   const clearSecrets=secretKey&&values._clearSecret?[secretKey]:[];
   if(secretKey&&clearSecrets.length)delete patch[secretKey];
   const response=await fetch('/api/settings',{method:'PATCH',headers:{'Content-Type':'application/json','If-Match':`"settings-${editing.snapshot.id}"`},body:JSON.stringify({set:patch,clearSecrets,...(needsProof?{password:values._adminPassword,totp:values._adminTOTP}:{})})});
   if(!response.ok){const body=await response.json();throw Error(body.message??(response.status===409?'配置或备用登录状态已变化，请刷新后重试。':'配置提交失败'))}
   setEditing(undefined);form.resetFields();setNotice('已保存新版本；请以页面顶部的应用状态为准。');
   await Promise.all(['configuration-current','configuration-history','backend-settings','backend-branding','login-policy','ai-admin-settings','ai-settings','ai-status','ai-admin-profiles','ai-profiles','agent-install-defaults','r13-ldap-providers'].map(key=>cache.invalidateQueries({queryKey:[key]})));
  }catch(e){if(e instanceof Error)setError(e.message);else if(editing.group==='ai'&&e&&typeof e==='object'&&'errorFields' in e){setAITab('prompts')}else if((editing.group==='mail'||editing.group==='listeners'||editing.group==='aiModel'||editing.group==='ldap')&&e&&typeof e==='object'&&'errorFields' in e){const fields=(e as {errorFields:Array<{name:Array<string|number>}>}).errorFields;const key=String(fields[0]?.name[0]??'');setMailTab(key.startsWith('native-rdp-')?'rdp':/^(http-server|website-server)-/.test(key)?'public':['ldap-password','ldap-ca-pem','mail-password','mail-ca-pem','mail-insecure-skip-verify','ssh-server-private-key','llm-api-key','llm-proxy-url','llm-allow-private','_adminPassword','_adminTOTP'].includes(key)?'security':'connection')}}finally{setBusy(false)}
 };
 const field=(p:Definition)=>{
  const full=p.key==='passkey-origins'||p.key==='ldap-user-property-mapping'||p.key==='ldap-ca-pem'||p.key==='llm-shell-prompt'||p.key==='llm-audit-prompt'||p.key==='ip-trust-list'||p.key==='mail-ca-pem'||p.type==='secret'||p.key==='password-strength-policy'||p.key==='system-logo'||p.key==='system-copyright'||p.key==='watermark-content'||p.key==='watermark-font-color';
  return <Form.Item key={p.key} className={full?'nt-settings-field-full':undefined} name={p.key} label={p.label} valuePropName={p.type==='boolean'?'checked':'value'} extra={fieldHints[p.key]??(p.type==='integer'?`${p.min}–${p.max} ${p.unit} · 默认 ${p.default} ${p.unit}`:undefined)} rules={p.type==='number'?[{required:true},{type:'number',min:p.min,max:p.max},{validator:(_,v)=>v<0&&v!==-1?Promise.reject(Error('仅支持 -1 或 0–2')):Promise.resolve()}]:p.type==='integer'?[{required:true},{type:'integer',min:p.min,max:p.max},{validator:(_,v)=>p.key==='access-max-idle-second'&&v===0?Promise.reject(Error('请填写 -1 或 1–86400 秒')):Promise.resolve()}]:p.type==='string'?[{required:p.min>0,max:p.max}]:[]}>
   {p.key==='passkey-origins'?<BackendOriginListInput/>:p.key==='ldap-user-property-mapping'?<BackendLDAPMappingInput/>:p.key==='ldap-identity-attribute'?<Select disabled={!!editing?.snapshot.values['ldap-url']} options={[{value:'entryUUID',label:'entryUUID（OpenLDAP）'},{value:'objectGUID',label:'objectGUID（Active Directory）'}]}/>:p.key==='llm-shell-prompt'||p.key==='llm-audit-prompt'?<Input.TextArea rows={4} maxLength={p.max} showCount placeholder="补充说明；留空使用平台内置规则"/>:p.key==='ssh-server-private-key'?<Input.TextArea rows={4} autoComplete="off" maxLength={p.max} disabled={!!clearMailPassword} placeholder="粘贴未加密的 OpenSSH / PEM 私钥；留空保持原值"/>:p.type==='secret'?<Input.Password autoComplete="new-password" maxLength={p.max} disabled={!!clearMailPassword}/>:(p.key==='ldap-ca-pem'||p.key==='mail-ca-pem'||p.key==='ip-trust-list')?<Input.TextArea rows={3} maxLength={p.max}/>:p.key==='llm-protocol'?<Select options={[{value:'openai',label:'OpenAI Chat Completions'},{value:'responses',label:'OpenAI Responses'},{value:'anthropic',label:'Anthropic Messages'}]}/>:p.key==='db-proxy-certificate-id'?<Select loading={certificates.isFetching} options={[{value:'',label:'自动生成的平台证书'},...(certificates.data??[]).filter(c=>c.kind==='server'&&!c.revokedAt&&Date.parse(c.notAfter)>Date.now()).map(c=>({value:c.id,label:c.name}))]} notFoundContent={certificates.error?'证书读取失败':'没有可用证书'}/>:p.key==='password-strength-type'?<Select options={[{value:'recommend',label:'推荐规则'},{value:'customize',label:'自定义规则'}]}/>:p.key==='password-strength-policy'?<BackendPasswordPolicyInput disabled={passwordType!=='customize'}/>:p.key==='system-logo'?<BackendLogoInput/>:p.type==='boolean'?<Switch disabled={p.key==='mail-insecure-skip-verify'}/>:p.type==='string'?<Input maxLength={p.max} disabled={p.key==='oidc-server-issuer'?!!editing?.snapshot.values[p.key]:['ldap-url','ldap-base-dn'].includes(p.key)&&!!editing?.snapshot.values['ldap-url']}/>:<InputNumber min={p.min} max={p.max} precision={p.type==='number'?undefined:0} step={p.type==='number'?0.1:1} style={{width:'100%'}}/>}
  </Form.Item>
 };
 return <div className="nt-settings-stack">{confirmHolder}
  {(current.error||schema.error||(!editing&&error))&&<Alert type="error" showIcon title={current.error?.message||schema.error?.message||error}/>}
  {notice&&<Alert type={current.data?.state==='effective'?'success':'info'} showIcon closable onClose={()=>setNotice('')} title={current.data?.state==='effective'?'配置已由在线节点应用，各参数按对应生效时机执行。':notice}/>}
  {selected.map(group=><Card key={group} title={groups[group].title} loading={current.isPending||schema.isPending} extra={canWrite&&<Button icon={<EditOutlined/>} disabled={!current.data||!schema.data} onClick={()=>open(group)}>编辑{group==='sessions'?'登录会话':group==='locks'?'失败保护':'配置'}</Button>}>
   <p className="nt-settings-description">{groups[group].description}</p>
   {group==='listeners'&&current.data?.readOnly&&<Typography.Paragraph type="secondary">SSH 网关身份：{current.data.readOnly['ssh-server-private-key-exists']?'已有主机密钥':'监听启用时自动生成'}（只读状态；当前公开指纹见原生客户端入口）</Typography.Paragraph>}
   {group==='branding'&&<div className="nt-brand-preview"><img src={`/api/logo?v=${current.data?.id??0}`} alt="当前系统 Logo"/><div><strong>{String(current.data?.values['system-name']??'Puppeteer')}</strong><span>{String(current.data?.values['system-copyright']??'')}</span></div><Tag variant="filled">当前展示</Tag></div>}
   <div className="nt-settings-values">{definitions.filter(p=>propertyGroup(p.key)===group).map(p=><div className="nt-settings-value" key={p.key}><span>{p.label}</span><strong>{p.key==='password-strength-policy'&&current.data?.values['password-strength-type']!=='customize'?'未启用（使用推荐规则）':displayConfiguration(current.data?.values[p.key],p)}</strong></div>)}</div>
   <Collapse ghost size="small" items={[{key:'effect',label:'生效范围与注意事项',children:<Typography.Paragraph type="secondary" style={{margin:0}}>{groups[group].note}</Typography.Paragraph>}]}/>
  </Card>)}
  <Modal className="nt-settings-editor" width={760} open={!!editing} title={`编辑${editing?groups[editing.group].title:''}`} onCancel={close} onOk={()=>void save()} confirmLoading={busy} mask={{closable:false}} keyboard={!busy} cancelButtonProps={{disabled:busy}} forceRender>
   <div className="nt-settings-editor-note"><Tag color="blue">基于版本 {editing?.snapshot.id}</Tag><Typography.Text type="secondary">仅保存当前分组，其他配置保持不变。</Typography.Text></div>
   {error&&<Alert type="error" showIcon title={error}/>}
   <Form form={form} layout="vertical" preserve={false}>{editing?.group==='ldap'?<Tabs activeKey={mailTab} onChange={setMailTab} items={[
 {key:'connection',label:'目录与账号',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>propertyGroup(p.key)==='ldap'&&!['ldap-password','ldap-ca-pem'].includes(p.key)).map(field)}</div>},
 {key:'security',label:'密码、证书与复核',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>['ldap-password','ldap-ca-pem'].includes(p.key)).map(field)}<Form.Item className="nt-settings-field-full" name="_clearSecret" valuePropName="checked"><Checkbox>清除 LDAP 服务账号密码</Checkbox></Form.Item><Form.Item name="_adminPassword" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="_adminTOTP" label="新的动态口令" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item></div>},
]}/>:editing?.group==='mail'?<Tabs className="nt-mail-tabs" activeKey={mailTab} onChange={setMailTab} items={[
    {key:'connection',label:'连接与发件人',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>propertyGroup(p.key)==='mail'&&!['mail-password','mail-ca-pem','mail-insecure-skip-verify'].includes(p.key)).map(field)}</div>},
    {key:'security',label:'密码与 TLS',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>['mail-password','mail-ca-pem','mail-insecure-skip-verify'].includes(p.key)).map(field)}
     <Form.Item className="nt-settings-field-full" name="_clearSecret" valuePropName="checked"><Checkbox>清除已保存的 SMTP 密码</Checkbox></Form.Item>
     {secretChanged&&<><Form.Item name="_adminPassword" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="_adminTOTP" label="新的动态口令" extra={account.mfaEnabled?'已绑定 MFA，必须使用未消费的新口令。':'未绑定 MFA 时留空。'} rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item></>}
    </div>},
   ]}/>:editing?.group==='listeners'&&secretKey?<Tabs activeKey={mailTab} onChange={setMailTab} items={[
    {key:'connection',label:'SSH 与数据库',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>propertyGroup(p.key)==='listeners'&&p.key!=='ssh-server-private-key'&&!/^(http-server|website-server|native-rdp)-/.test(p.key)).map(field)}</div>},
    {key:'public',label:'HTTP 与网站',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>/^(http-server|website-server)-/.test(p.key)).map(field)}</div>},
    {key:'rdp',label:'原生 RDP',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>p.key.startsWith('native-rdp-')).map(field)}</div>},
    {key:'security',label:'SSH 主机身份',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>p.key==='ssh-server-private-key').map(field)}<Form.Item className="nt-settings-field-full" name="_clearSecret" valuePropName="checked"><Checkbox>恢复平台自动生成的原主机密钥</Checkbox></Form.Item>{secretChanged&&<><Form.Item name="_adminPassword" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="_adminTOTP" label="新的动态口令" extra="已绑定 MFA 时需输入未消费的新口令。" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item></>}</div>},
   ]}/>:editing?.group==='loginPolicy'?<>
    {(loginPolicy.error||!loginPolicy.data?.canDisable)&&<Alert showIcon type={loginPolicy.error?'error':'warning'} title={loginPolicy.error?.message??'关闭前请先完成当前管理员的备用登录验证。'}/>}
    <Form.Item name="disable-password-login" label="关闭本地密码登录" valuePropName="checked"><Switch disabled={!editing.snapshot.values['disable-password-login']&&!loginPolicy.data?.canDisable}/></Form.Item>
    <Form.Item name="_adminPassword" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item>
    <Form.Item name="_adminTOTP" label="新的动态口令" extra="已绑定 MFA 时需输入未消费的新口令。" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
   </>:(editing?.group==='captcha'||editing?.group==='databasePolicy'||editing?.group==='access'||editing?.group==='oauthServer'||editing?.group==='recording'||editing?.group==='passkey'||editing?.group==='oauthLifetime')?<>
    {definitions.filter(p=>propertyGroup(p.key)===editing.group).map(field)}
    <Form.Item name="_adminPassword" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item>
    <Form.Item name="_adminTOTP" label="新的动态口令" extra="已绑定 MFA 时需输入未消费的新口令。" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
   </>:editing?.group==='aiModel'?<Tabs activeKey={mailTab} onChange={setMailTab} items={[
    {key:'connection',label:'服务与用量',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>propertyGroup(p.key)==='aiModel'&&!['llm-api-key','llm-proxy-url','llm-allow-private'].includes(p.key)).map(field)}</div>},
    {key:'security',label:'密钥与网络',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>['llm-api-key','llm-proxy-url','llm-allow-private'].includes(p.key)).map(field)}<Form.Item className="nt-settings-field-full" name="_clearSecret" valuePropName="checked"><Checkbox>清除平台默认模型 API Key</Checkbox></Form.Item>{secretChanged&&<><Form.Item name="_adminPassword" label="当前管理员密码" rules={[{required:true,max:1024}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="_adminTOTP" label="新的动态口令" rules={[{required:account.mfaEnabled},{pattern:/^\d{6}$/}]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item></>}</div>},
   ]}/>:editing?.group==='ai'?<Tabs activeKey={aiTab} onChange={setAITab} items={[{key:'switches',label:'功能开关',forceRender:true,children:<div className="nt-mail-fields">{definitions.filter(p=>propertyGroup(p.key)==='ai'&&p.type==='boolean').map(field)}</div>},{key:'prompts',label:'补充提示词',forceRender:true,children:definitions.filter(p=>propertyGroup(p.key)==='ai'&&p.type==='string').map(field)}]}/>:definitions.filter(p=>propertyGroup(p.key)===editing?.group).map(field)}</Form>
  </Modal>
 </div>;
}

