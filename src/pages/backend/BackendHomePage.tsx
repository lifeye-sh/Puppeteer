import {Button,Card,Descriptions,Space,Typography} from 'antd';
import {useQueryClient} from '@tanstack/react-query';
import {useState} from 'react';
import {Link} from 'react-router-dom';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import accountApi from '@/api/account-api';
import {clearCurrentUser} from '@/utils/permission';
import BackendDashboard from './BackendDashboard';
import BackendSecurityPanel from './BackendSecurityPanel';
import BackendPlatformSettings from './BackendPlatformSettings';

export default function BackendHomePage({accountOnly=false}:{accountOnly?:boolean}){return accountOnly?<AccountWorkbench/>:<BackendDashboard/>}
export function BackendWorkbench({accountOnly=false}:{accountOnly?:boolean}){return accountOnly?<AccountWorkbench/>:<BackendPlatformSettings/>}
function AccountWorkbench(){
 const {account}=useBackendRuntime(),client=useQueryClient();const [leaving,setLeaving]=useState(false);
 const logout=async()=>{setLeaving(true);try{await accountApi.logout();clearCurrentUser();client.clear();window.location.replace('/login')}finally{setLeaving(false)}};
 return <section className="nt-page"><div className="flex flex-wrap items-center justify-between gap-4"><Typography.Title level={3} style={{margin:0}}>个人信息</Typography.Title><Space><Link to="/">返回首页</Link><Button loading={leaving} onClick={()=>void logout()}>退出登录</Button></Space></div>
  <Card title="当前账号"><Descriptions items={[
   {key:'username',label:'账号',children:account.username},{key:'nickname',label:'昵称',children:account.nickname},
   {key:'role',label:'类型',children:account.type==='admin'?'管理员':'普通用户'},{key:'id',label:'用户 ID',children:account.id},
  ]}/></Card><BackendSecurityPanel/>
 </section>;
}

