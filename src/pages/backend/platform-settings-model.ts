export type SettingsCategory='deployment'|'dependencies'|'policies'|'status';
export const settingsCategories:Record<SettingsCategory,{label:string;path:string;description:string;notice:string}>={
 deployment:{label:'服务部署',path:'/deployment-settings',description:'代理与图形组件、更新仓库、归档及入口限制',notice:'服务启停可能调整容器和端口。请以部署验证结果为准；端口检查不包含防火墙或云安全组。'},
 dependencies:{label:'外部依赖',path:'/integration-settings',description:'邮件、身份源、模型与接入依赖',notice:'保存配置后，还需检查连接、认证与授权。检测结果以各功能页面为准；发送测试邮件等操作需单独执行。'},
 policies:{label:'业务策略',path:'/policy-settings',description:'登录、会话、录屏、配额与展示规则',notice:'这些设置按各自规则生效。允许录屏不代表 MP4 可用，允许文件传输不代表共享盘已挂载。'},
 status:{label:'状态与发布',path:'/configuration-status',description:'配置版本、节点反馈与运行能力',notice:'配置应用、依赖就绪和实际服务状态分别判断。能力声明和历史成功记录不能替代当前运行检测。'},
};
const sections:Record<string,SettingsCategory>={
 'agent-repository':'deployment',wal:'deployment','web-access':'deployment',mp4:'deployment',drive:'deployment',components:'deployment',deployment:'deployment',listeners:'deployment',
 mail:'dependencies',oidcProvider:'dependencies',wecomProvider:'dependencies',oauthServer:'dependencies',oauthKeys:'dependencies',passkey:'dependencies',ldap:'dependencies',aiModel:'dependencies',databasePolicy:'dependencies',forward:'dependencies',
 branding:'policies',login:'policies',postgres:'policies',network:'policies',ai:'policies',access:'policies',watermark:'policies',resources:'policies',recording:'policies',retention:'policies',oauthLifetime:'policies',
 operations:'status',license:'status',publish:'status',runtime:'status',
};
export function categoryForSection(section:string){return Object.prototype.hasOwnProperty.call(sections,section)?sections[section]:undefined;}
export function settingsDestination(section:string){const category=categoryForSection(section);return category?`${settingsCategories[category].path}?section=${encodeURIComponent(section)}`:'/system-status';}
export const configurationStateDimensions=[
 {title:'用户期望状态',description:'希望启用、停用或采用的配置'},
 {title:'配置应用状态',description:'配置版本是否已被节点确认'},
 {title:'依赖就绪状态',description:'连接、凭据、组件等预检是否通过'},
 {title:'实际服务状态',description:'当前运行探测或业务验证的结果'},
];
