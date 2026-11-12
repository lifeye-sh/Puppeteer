import {useState} from 'react';
import {Alert,Button,Form,Input,InputNumber,Modal,Select,Space,Switch,Typography} from 'antd';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
type Rule={assetId:string;host:string;port:number;addresses?:string[]};
type Settings={enabled:boolean;desiredEnabled?:boolean;configurationRevision?:number;pending?:boolean;version:number;rules:Rule[]};
export default function BackendNativeForwardSettings(){
 const {capabilities}=useBackendRuntime();const cache=useQueryClient();const [form]=Form.useForm();const [open,setOpen]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[settings,setSettings]=useState<Settings>(),[search,setSearch]=useState('');
 const assets=useQuery({queryKey:['r14-forward-assets',search],enabled:open,queryFn:()=>requests.get<{items:{id:string;name:string;protocol:string}[]}>('/admin/assets/paging?pageSize=100&search='+encodeURIComponent(search),{errorMode:'local'})});
 if(capabilities.features['native.ssh-forward']?.state!=='available')return null;
 const close=()=>{setOpen(false);form.resetFields();setSettings(undefined);setError('')};
 const show=async()=>{setOpen(true);setBusy(true);setError('');try{const data=await requests.get<Settings>('/admin/native-forward',{errorMode:'local'});setSettings(data);form.setFieldsValue({...data,enabled:data.desiredEnabled??data.enabled,password:'',totp:''})}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
const save=async()=>{if(!settings)return;setBusy(true);setError('');try{const v=await form.validateFields();await requests.put('/admin/native-forward',{...v,version:settings.version},{errorMode:'local'});await Promise.all([cache.invalidateQueries({queryKey:['r14-native-ssh-gateway']}),cache.invalidateQueries({queryKey:['configuration-current']}),cache.invalidateQueries({queryKey:['configuration-history']})]);close()}catch(e){if(e instanceof Error)setError(e.message)}finally{setBusy(false)}};
 return <><Button onClick={()=>void show()}>SSH 转发设置</Button><Modal title="SSH 受控端口转发" open={open} width={900} centered styles={{body:{maxHeight:'70vh',overflowY:'auto'}}} onCancel={close} onOk={()=>void save()} confirmLoading={busy} okButtonProps={{disabled:!settings}} destroyOnHidden>
  <Alert type="info" showIcon title="默认关闭，仅允许精确目标" description="连接从平台后端发起，不经过资产的 Agent 或跳板。用户须同时拥有该资产的执行、上传和下载权限，命令过滤会阻止转发。修改目标会断开正在传输的连接；开启和目标变更等待在线节点确认。保存结果可在平台设置的配置发布中查看。仅记录目标与流量，不录制 TCP 内容。"/>
  {settings?.pending&&<Alert type="warning" showIcon title="配置正在等待节点确认" description="期望开启状态已保存，实际连接仍按当前生效策略检查。"/>}
  {settings?.configurationRevision&&<Typography.Paragraph type="secondary">配置版本 {settings.configurationRevision} · 实际转发{settings.enabled?'已开启':'已关闭'}</Typography.Paragraph>}
  {error&&<Alert type="error" title={error}/>} {assets.error&&<Alert type="error" title={assets.error.message}/>}
  <Form form={form} layout="vertical" style={{marginTop:16}} preserve={false}>
   <Form.Item name="enabled" label="启用原生 SSH 转发" valuePropName="checked"><Switch/></Form.Item>
   <Typography.Paragraph>每条规则绑定资产、目标主机和端口。域名启用时固定解析 IP；变更后需重新保存。最多 100 条规则、每连接 8 个通道、每用户每分钟 20 次；不支持远程监听（-R）、Agent 或 X11 转发。</Typography.Paragraph>
   <Form.List name="rules">{(fields,{add,remove})=><><div style={{maxHeight:280,overflowY:'auto'}}>{fields.map(({key,name,...rest})=><Space key={key} align="start" wrap style={{display:'flex'}}>
    <Form.Item {...rest} name={[name,'assetId']} rules={[{required:true,message:'选择授权资产'}]}><Select style={{width:260}} placeholder="授权资产" showSearch filterOption={false} onSearch={setSearch} options={assets.data?.items.filter(a=>a.protocol==='ssh').map(a=>({value:a.id,label:a.name}))}/></Form.Item>
    <Form.Item {...rest} name={[name,'host']} rules={[{required:true,message:'填写目标主机'}]}><Input style={{width:240}} placeholder="目标 IP 或域名" maxLength={253}/></Form.Item>
    <Form.Item {...rest} name={[name,'port']} rules={[{required:true,message:'填写端口'}]}><InputNumber placeholder="端口" min={1} max={65535}/></Form.Item><Button danger onClick={()=>remove(name)}>删除</Button>
   </Space>)}</div><Button onClick={()=>add({port:443})} disabled={fields.length>=100}>添加目标</Button></>}</Form.List>
   {settings&&settings.rules.length>0&&<details style={{margin:'12px 0'}}><summary>当前固定的目标 IP</summary>{settings.rules.map((r,i)=><div key={i}><code>{r.host}:{r.port} → {r.addresses?.join(', ')}</code></div>)}</details>}
   <Space align="start" wrap style={{marginTop:16}}><Form.Item name="password" label="当前平台密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item><Form.Item name="totp" label="动态口令（启用 MFA 时必填）"><Input maxLength={6} autoComplete="one-time-code"/></Form.Item></Space>
  </Form>
 </Modal></>;
}
