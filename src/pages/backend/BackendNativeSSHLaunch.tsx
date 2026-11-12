import {Alert,Button,Checkbox,Descriptions,InputNumber,Modal,Select,Space,Spin,Typography} from 'antd';
import {useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';

type Launch={passwordAuthentication?:boolean;passwordCommand?:string;forwardTargets?:{host:string;port:number}[];assetName:string;host:string;port:number;username:string;fingerprint:string;config:string;configFilename:string;knownHosts:string;knownHostsFilename:string;command:string;termarkUri:string};
function download(name:string,content:string){
 const url=URL.createObjectURL(new Blob([content],{type:'text/plain;charset=utf-8'}));
 const link=document.createElement('a');link.href=url;link.download=name;link.click();
 setTimeout(()=>URL.revokeObjectURL(url),1000);
}
export default function BackendNativeSSHLaunch({assetId,disabled}:{assetId:string;disabled?:boolean}){
 const [once,setOnce]=useState(false);
 const [open,setOpen]=useState(false);const [localPort,setLocalPort]=useState(15432),[target,setTarget]=useState(0);
 const {capabilities}=useBackendRuntime();
 const supported=capabilities.features['native.ssh-launch']?.state==='available';
 const query=useQuery({queryKey:['r14-native-ssh-launch',assetId],enabled:open&&supported,queryFn:()=>requests.get<Launch>(`/native/ssh-launch/${assetId}`,{errorMode:'local'}),retry:false});
 if(!supported)return null;
 const data=!query.isFetching&&!query.error?query.data:undefined;
 const destination=data?.forwardTargets?.[target];
 const forward=destination&&data?data.command.replace(' -i ',` -N -o ClearAllForwardings=no -L "127.0.0.1:${localPort}:${destination.host.includes(':')?'['+destination.host+']':destination.host}:${destination.port}" -i `):'';
 return <>
  <Button type="text" disabled={disabled} onClick={()=>setOpen(true)}>原生 SSH</Button>
  <Modal title={data?`原生 SSH · ${data.assetName}`:'原生 SSH'} open={open} onCancel={()=>setOpen(false)} footer={<Button onClick={()=>setOpen(false)}>关闭</Button>} width={760} styles={{body:{maxHeight:'70vh',overflowY:'auto'}}}>
   {query.isFetching&&<Spin/>}
   {query.error&&<Alert type="error" showIcon title="暂时无法生成连接配置" description={query.error.message} action={<Button onClick={()=>void query.refetch()}>重试</Button>}/>}
   {data&&<Space orientation="vertical" size="middle" style={{width:'100%'}}>
    <Alert type="info" showIcon title="使用个人 SSH 私钥登录" description={<>请先在<a href="/x-info" target="_blank" rel="noopener noreferrer">个人信息 → SSH 公钥</a>登记公钥。已启用 MFA 时，还需输入动态口令；连接受当前资产授权和访问策略限制。</>}/>
    <Descriptions size="small" column={1} items={[
     {key:'address',label:'网关地址',children:`${data.host}:${data.port}`},
     {key:'username',label:'登录用户名',children:<Typography.Text copyable>{data.username}</Typography.Text>},
     {key:'fingerprint',label:'主机指纹',children:<Typography.Text code copyable>{data.fingerprint}</Typography.Text>},
    ]}/>
    <Typography.Title level={5} style={{margin:0}}>OpenSSH</Typography.Title>
    <Typography.Text>下载以下两个文件到同一个目录，在该目录打开终端。将命令中的“你的私钥路径”替换为自己的私钥文件路径。</Typography.Text>
    <Space wrap><Button onClick={()=>download(data.configFilename,data.config)}>下载 SSH 配置</Button><Button onClick={()=>download(data.knownHostsFilename,data.knownHosts)}>下载可信主机公钥</Button></Space>
    <Typography.Paragraph copyable code style={{overflowWrap:'anywhere',margin:0}}>{data.command}</Typography.Paragraph>
    {data.passwordAuthentication&&data.passwordCommand&&<><Typography.Title level={5} style={{margin:0}}>平台密码登录</Typography.Title><Typography.Text>也可运行以下命令，按客户端提示输入平台本地密码和动态口令。无需上传私钥；不要输入远端资产密码。密码到期时请先在平台更换。</Typography.Text><Typography.Paragraph copyable code style={{overflowWrap:'anywhere',margin:0}}>{data.passwordCommand}</Typography.Paragraph></>}
    <Typography.Text type="secondary">配置启用严格主机校验。网关指纹变化时请重新核对并下载，不要关闭校验。</Typography.Text>
    {!!data.forwardTargets?.length&&<>
     <Typography.Title level={5} style={{margin:0}}>受控 TCP 转发</Typography.Title>
     <Typography.Text>选择目标及本机监听端口，单独运行以下命令。保持进程运行后访问本机端口；仅绑定 127.0.0.1。转发从平台后端发起，不经过资产跳板，仅审计目标和流量。</Typography.Text>
     <Space wrap><Select aria-label="转发目标" style={{minWidth:230}} value={target} onChange={setTarget} options={data.forwardTargets.map((r,i)=>({value:i,label:r.host+':'+r.port}))}/><InputNumber aria-label="本地监听端口" value={localPort} min={1024} max={65535} precision={0} onChange={v=>setLocalPort(v??15432)}/></Space>
     {forward&&<Typography.Paragraph copyable code style={{overflowWrap:'anywhere',margin:0}}>{forward}</Typography.Paragraph>}
     {destination&&capabilities.features['native.https-forward']?.state==='available'&&<><Typography.Title level={5} style={{margin:0}}>通过 HTTPS 转发</Typography.Title><Typography.Text>使用新版 nt-client，通过浏览器登录并确认授权，无需个人 SSH 私钥。仅访问平台 HTTPS 入口；平台仍按上述白名单连接目标。</Typography.Text><Checkbox checked={once} onChange={e=>setOnce(e.target.checked)}>一次性连接（仅 HTTPS）</Checkbox>{once&&<Alert type="info" title="需要支持 --once 的新版客户端" description="接入首个连接后关闭监听，连接结束即退出。不适用于连接池或自动重连；本地应用仍需完成目标服务认证。"/>}<Typography.Paragraph copyable code style={{overflowWrap:'anywhere',margin:0}}>{`nt-client forward --server "${location.origin}" --asset ${assetId} --target "${destination.host.includes(':')?'['+destination.host+']':destination.host}:${destination.port}" --local-port ${localPort}${once?' --once':''}`}</Typography.Paragraph></>}
    </>}
    <Typography.Title level={5} style={{margin:0}}>Termark</Typography.Title>
    <Typography.Text>需要已安装并注册链接协议的 Termark。打开后选择本机私钥，并核对上方主机指纹。</Typography.Text>
    <Space wrap><Button href={data.termarkUri}>用 Termark 打开</Button><Typography.Text copyable={{text:data.termarkUri}}>复制连接链接</Typography.Text></Space>
   </Space>}
  </Modal>
 </>;
}
