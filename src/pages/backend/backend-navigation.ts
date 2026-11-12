export type NavigationPage = {path:string; label:string; permission?:string; feature?:string};
export type NavigationGroup = {key:string; label:string; pages:NavigationPage[]};

// One source for sidebar grouping, active items and breadcrumb ancestry.
export const navigationGroups:NavigationGroup[] = [
 {key:'workspace',label:'工作台',pages:[
  {path:'/x-asset',label:'我的资产'},
  {path:'/ai',label:'AI 助手',feature:'ai.chat'},
  {path:'/x-database-asset',label:'我的数据库',feature:'database.assets'},
  {path:'/x-website',label:'我的 Web 资产'},
  {path:'/x-snippet',label:'我的命令片段'},
  {path:'/x-access-request',label:'我的申请'},
  {path:'/x-db-work-order',label:'我的 SQL 工单',feature:'database.assets'},
  {path:'/storage',label:'存储空间',feature:'storage.spaces'},
  {path:'/notification-preferences',label:'通知偏好'},
 ]},
 {key:'resources',label:'资源管理',pages:[
  {path:'/asset',label:'主机资产',permission:'asset.read'},
  {path:'/database-asset',label:'数据库资产',permission:'database.read',feature:'database.assets'},
  {path:'/website',label:'Web 资产',permission:'website.read'},
  {path:'/credential',label:'授权凭据',permission:'credential.read'},
  {path:'/certificate',label:'证书管理',permission:'certificate.read'},
 ]},
 {key:'connectivity',label:'接入管理',pages:[
  {path:'/agent-gateway',label:'Agent 网关',permission:'identity.manage'},
  {path:'/ssh-gateway',label:'SSH 跳板',permission:'identity.manage'},
  {path:'/gateway-group',label:'网关组',permission:'identity.manage'},
  {path:'/network-proxy',label:'网络代理',permission:'identity.manage'},
  {path:'/website-setting',label:'Web 接入设置',permission:'identity.manage'},
  {path:'/oidc-client',label:'应用与原生接入',permission:'identity.manage',feature:'identity.oidc-server'},
 ]},
 {key:'access',label:'访问控制',pages:[
  {path:'/authorised-asset',label:'资源授权',permission:'grant.read'},
  {path:'/access-policy',label:'访问策略',permission:'policy.read'},
  {path:'/strategy',label:'动作策略',permission:'policy.read'},
  {path:'/command-filter',label:'命令过滤',permission:'policy.read'},
  {path:'/ip-set',label:'IP 集合',permission:'policy.read'},
  {path:'/access-request',label:'访问审批',permission:'approval.read'},
  {path:'/db-work-order',label:'SQL 工单审批',permission:'database.approve',feature:'database.assets'},
 ]},
 {key:'audit',label:'会话审计',pages:[
  {path:'/online-session',label:'在线会话'},
  {path:'/access-log',label:'访问记录'},
  {path:'/access-log-stats',label:'访问统计'},
  {path:'/exec-command-log',label:'命令记录'},
  {path:'/filesystem-log',label:'文件审计'},
  {path:'/database-sql-log',label:'数据库 SQL 审计',permission:'database.audit',feature:'database.assets'},
 ]},
 {key:'identity',label:'身份认证',pages:[
  {path:'/user',label:'用户管理',permission:'user.read'},
  {path:'/department',label:'部门管理',permission:'department.read'},
  {path:'/role',label:'角色权限',permission:'role.read'},
  {path:'/identity-settings',label:'登录与身份源',permission:'identity.manage',feature:'identity.oidc-config'},
  {path:'/login-locked',label:'登录锁定',permission:'lock.read'},
 ]},
 {key:'operations',label:'系统运维',pages:[
  {path:'/scheduled-task',label:'任务调度',permission:'task.manage'},
  {path:'/snippet',label:'系统命令片段'},
  {path:'/credential-rotation',label:'凭据轮换',permission:'credential.rotate'},
  {path:'/monitoring',label:'监控与告警',permission:'identity.manage'},
  {path:'/tools',label:'链路诊断',permission:'identity.manage'},
  {path:'/system-event',label:'备份与恢复',permission:'backup.manage'},
  {path:'/setting',label:'通知配置',permission:'identity.manage'},
  {path:'/system-status',label:'平台设置'},
 ]},
];
export function visibleNavigation(actions:readonly string[],features:Record<string,{state:string}>) {
 return navigationGroups.map(group=>({...group,pages:group.pages.filter(page=>
  (!page.permission||actions.includes(page.permission))&&(!page.feature||features[page.feature]?.state==='available')
 )})).filter(group=>group.pages.length>0);
}
const aliases:Record<string,string>={'/authorised-database-asset':'/authorised-asset','/authorised-website':'/authorised-asset'};
export function navigationLocation(pathname:string,groups:NavigationGroup[]) {
 const path=aliases[pathname]??pathname;
 for(const group of groups){const page=group.pages.find(p=>p.path===path);if(page)return{group,page};}
 return undefined;
}
