export type ConfigurationValue=number|boolean|string;
export type Definition={key:string;label:string;min:number;max:number;default:ConfigurationValue;unit:string;effect:string;type:string};
export type NodeState={id:string;state:string;error:string;attempts:number;lastSeen:string;online:boolean;lastAppliedRevision?:number};
export type Configuration={id:number;values:Record<string,ConfigurationValue>;readOnly?:Record<string,ConfigurationValue>;secrets?:Record<string,{hasValue:boolean}>;securityPending?:boolean;changedKeys:string[];state:string;nodes:NodeState[];actor:string;createdAt:string;legacyWrite:boolean};
export type ConfigurationGroup='branding'|'sessions'|'locks'|'postgres'|'retention'|'resources'|'watermark'|'password'|'listeners'|'mail'|'network'|'loginPolicy'|'ai'|'aiModel'|'ldap'|'access'|'passkey'|'oauthLifetime'|'recording'|'oauthServer'|'databasePolicy'|'captcha';
export const configurationStates:Record<string,string>={requested:'等待应用',applying:'正在应用',effective:'已生效',failed:'应用失败',stalled:'应用超时'};
export function propertyGroup(key:string):ConfigurationGroup|undefined{
 if(key==='login-captcha-enabled')return 'captcha';
 if(['oidc-server-enabled','oidc-server-issuer','native-client-enabled'].includes(key))return 'oauthServer';
 if(key==='recording-enabled')return 'recording';
 if(key.startsWith('ssh-server-port-forwarding-'))return undefined; // dedicated asset/target editor
 if(['llm-profile-enabled','llm-base-url','llm-model','llm-api-key','llm-temperature','llm-max-tokens','llm-proxy-url','llm-allow-private','llm-protocol','llm-input-rate','llm-output-rate'].includes(key))return 'aiModel';
 if(['ssh-server-disable-password-auth','access-max-idle-second','access-require-mfa','access-mfa-expires-at'].includes(key))return 'access';
 if(key.startsWith('oidc-server-')&&key.endsWith('-ttl'))return 'oauthLifetime';
 if(key.startsWith('passkey-'))return 'passkey';
 if(key.startsWith('ldap-'))return 'ldap';
 if(key.startsWith('llm-'))return 'ai';
 if(key==='disable-password-login')return 'loginPolicy';
 if(key==='ip-extractor'||key==='ip-trust-list')return 'network';
 if(key.startsWith('mail-'))return 'mail';
 if(key==='db-proxy-block-dml')return 'databasePolicy';
 if(key.startsWith('ssh-server-')||key.startsWith('db-proxy-')||key.startsWith('postgres-proxy-')||key.startsWith('http-server-')||key.startsWith('website-server-')||key.startsWith('native-rdp-'))return 'listeners';
 if(key.startsWith('password-'))return 'password';
 if(key.startsWith('watermark-'))return 'watermark';
 if(key==='user-default-storage-size'||key==='user-client-cert-valid-days'||key==='agent-gateway-endpoint')return 'resources';
 if(key.endsWith('-saved-limit-days'))return 'retention';
 if(key.startsWith('system-'))return 'branding';
 if(key==='login-force-totp-enabled')return 'sessions';
 if(key.startsWith('login-session-'))return 'sessions';
 if(key.startsWith('login-lock-'))return 'locks';
 if(key.startsWith('postgres-'))return 'postgres';
}
export function displayConfiguration(value:ConfigurationValue|undefined,p:Pick<Definition,'key'|'unit'>):string{
 if(value===undefined)return '此版本未包含';
 if((p.key==='asset-group-tree'||p.key==='website-group-tree')&&typeof value==='string'){try{const count=(nodes:Array<{children?:unknown[]}>):number=>nodes.reduce((n,node)=>n+1+count((node.children??[]) as Array<{children?:unknown[]}>),0);return `${count(JSON.parse(value))} 个分组（当前共享资源目录）`}catch{return '分组数据不可用'}}
 if(p.key==='ldap-user-property-mapping'&&typeof value==='string'){try{const m=JSON.parse(value);return [m.username,m.nickname,m.mail].filter(Boolean).join(' / ')}catch{return '映射格式无效'}}
 if(p.key==='llm-api-key'||p.key==='ldap-password')return value?'已加密保存':'未配置';
 if(p.key==='passkey-domain'&&value==='')return '沿用部署 RP ID';
 if(p.key==='passkey-origins'&&typeof value==='string'){try{const list=JSON.parse(value);return list.length?list.join('、'):'沿用部署 HTTPS 来源'}catch{return '来源格式无效'}}
 if(p.key==='access-max-idle-second'&&value===-1)return '不限制空闲时间';
 if(p.key==='llm-temperature'&&value===-1)return '使用模型默认';
 if(p.key==='disable-password-login')return value?'已禁用密码登录':'允许密码登录';
 if(p.key==='llm-shell-prompt'||p.key==='llm-audit-prompt')return value?`已设置补充提示词（${Array.from(String(value)).length} 字符）`:'使用平台内置提示词';
 if(p.key==='mail-password'||p.key==='ssh-server-private-key')return value?'已加密保存':p.key==='ssh-server-private-key'?'平台自动生成的主机密钥':'未配置';
 if(p.key==='mail-ca-pem'||p.key==='ldap-ca-pem')return value?'已配置内网 CA':'使用系统信任库';
 if(['http-server-addr','website-server-addr','native-rdp-addr','native-rdp-public-address','ssh-server-addr','db-proxy-addr','postgres-proxy-addr'].includes(p.key)&&value==='')return '沿用部署配置（未配置则不监听）';
 if(p.key==='db-proxy-certificate-id')return value?'自选平台证书':'自动生成的平台证书';
 if(p.key==='password-expiration-period'&&value===-1)return '不设到期时间';
 if(p.key==='password-strength-type')return value==='recommend'?'推荐规则':'自定义规则';
 if(p.key==='password-strength-policy'&&typeof value==='string'){try{const v=JSON.parse(value);return `至少 ${v.minLength} 字符 / ${v.minCharacterType} 类字符${v.mustNotContainUsername?' / 禁止包含账号':''}${v.mustNotBePalindrome?' / 禁止回文':''}${v.mustNotWeak?' / 弱密码检查':''}`}catch{return '规则格式无效'}}
 if(p.key==='user-default-storage-size'&&typeof value==='number')return `${value} Byte（${Number((value/2**30).toFixed(3))} GiB）`;
 if(p.key==='system-logo')return value?'自定义 Logo':'默认图标';
 if(p.key.endsWith('-saved-limit-days')&&value===-1)return '永久保留';
 if(typeof value==='boolean')return value?'开启':'关闭';
 if(value==='')return '未设置';
 return `${value}${p.unit?' '+p.unit:''}`;
}
// Submit only registered fields in the selected group, never hidden values.
export function configurationPatch(definitions:Definition[],group:ConfigurationGroup,values:Record<string,ConfigurationValue>){
 return Object.fromEntries(definitions.filter(p=>propertyGroup(p.key)===group&&Object.prototype.hasOwnProperty.call(values,p.key)).map(p=>[p.key,values[p.key]]));
}
