import BackendLoginPolicy from './BackendLoginPolicy';
import BackendContactSettings from './BackendContactSettings';
import BackendLDAPProviders from './BackendLDAPProviders';
import {Tabs, Typography} from 'antd';
import {Navigate} from 'react-router-dom';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import BackendNotifications from './BackendNotifications';
import BackendOIDCProviders from './BackendOIDCProviders';

export default function BackendSettings({section='notifications'}:{section?:'notifications'|'identity'|'preferences'}){
 const {account,capabilities}=useBackendRuntime();
 if(section==='notifications'&&!account.actions?.includes('identity.manage'))return <Navigate to="/notification-preferences" replace/>;
 if(section!=='identity')return <><Typography.Title level={3}>{section==='preferences'?'通知偏好':'通知配置'}</Typography.Title><BackendNotifications personalOnly={section==='preferences'}/></>;
 const items=[
  ...(account.type==='admin'&&capabilities.features['identity.login-policy']?.state==='available'?[{key:'login-policy',label:'登录策略',children:<BackendLoginPolicy/>}]:[]),
  ...(capabilities.features['identity.contacts']?.state==='available'?[{key:'contacts',label:'验证与恢复',children:<BackendContactSettings/>}]:[]),
  {key:'oidc',label:'OIDC / 企业微信',children:<BackendOIDCProviders/>},
  ...(capabilities.features['identity.ldap-config']?.state==='available'?[{key:'ldap',label:'LDAP 目录',children:<BackendLDAPProviders/>}]:[]),
 ];
 return <><Typography.Title level={3}>登录与身份源</Typography.Title><Typography.Paragraph type="secondary">配置用户如何登录平台、验证身份和同步组织目录。</Typography.Paragraph><Tabs items={items}/></>;
}
