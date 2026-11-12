import {Alert,Descriptions,Typography} from 'antd';
import {useQuery} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';

type Gateway={passwordAuthentication?:boolean;enabled:boolean;ready:boolean;host:string;port:number;publicKey:string;fingerprint:string};
export default function BackendNativeSSHGateway(){
 const {capabilities}=useBackendRuntime();
 const supported=capabilities.features['native.ssh-gateway']?.state==='available';
 const query=useQuery({queryKey:['r14-native-ssh-gateway'],enabled:supported,queryFn:()=>requests.get<Gateway>('/native/ssh-gateway',{errorMode:'local'}),refetchInterval:15000});
 if(!supported)return null;
 if(query.error)return <Alert type="error" title="无法读取 SSH 网关状态" description={query.error.message}/>;
 if(!query.data)return null;
 const gateway=query.data;
 return <div style={{marginBottom:16}}>
  <Alert type={gateway.enabled&&gateway.ready?'success':'info'} showIcon title={gateway.enabled&&gateway.ready?'原生 SSH 网关可用':'原生 SSH 网关尚未开放'} description="使用下方登记公钥对应的私钥登录，用户名格式为“平台用户名:资产别名”。已启用或强制 MFA 的账号还需输入 TOTP。管理员可在 OIDC 应用客户端 → 服务端设置中开启原生访问。"/>
  <Descriptions size="small" column={1} style={{marginTop:12}} items={[
   {key:'authentication',label:'认证方式',children:gateway.passwordAuthentication?'个人 SSH 密钥或平台本地密码；绑定或强制 MFA 时还需动态口令':'个人 SSH 密钥；密码认证已禁用'},
   {key:'address',label:'网关地址',children:gateway.host&&gateway.port?`${gateway.host}:${gateway.port}`:'尚未配置监听地址'},
   {key:'fingerprint',label:'可信主机指纹',children:gateway.fingerprint?<Typography.Text copyable code>{gateway.fingerprint}</Typography.Text>:'服务启动后生成'},
   {key:'host-key',label:'网关主机公钥',children:gateway.publicKey?<Typography.Text copyable code style={{overflowWrap:'anywhere'}}>{gateway.publicKey}</Typography.Text>:'尚未生成'},
   {key:'limits',label:'当前能力',children:'单个 shell / 显式命令、PTY、审计录屏；TCP 端口转发需管理员单独配置白名单；不开放 SFTP 和 Agent 转发。'},
  ]}/>
 </div>;
}
