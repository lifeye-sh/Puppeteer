import {Alert,Button,Form,Input,Select,Space,Table,Typography} from 'antd';
import {useQuery} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';
type Settings={defaultCertificateId:string;consoleUrl:string;apiTrustedProxies:string[];websiteTrustedProxies:string[];version:number};
type Impact={id:string;name:string;domain:string;certificateValid:boolean};
export default function BackendWebsiteSettings(){
 const [form]=Form.useForm();const [error,setError]=useState('');const [busy,setBusy]=useState(false);const [preview,setPreview]=useState<Impact[]>();
 const settings=useQuery({queryKey:['r09-website-settings'],queryFn:()=>requests.get<Settings>('/admin/website-settings',{errorMode:'local'})});
 const certs=useQuery({queryKey:['r09-certificates'],queryFn:()=>requests.get<{id:string;name:string;kind:string;revokedAt?:string}[]>('/admin/certificates',{errorMode:'local'})});
 const load=()=>{if(settings.data){form.setFieldsValue(settings.data);setPreview(undefined)}};
 const run=async(save:boolean)=>{setBusy(true);setError('');try{const value=await form.validateFields();if(save){await requests.put('/admin/website-settings',value,{errorMode:'local'});await settings.refetch();setPreview(undefined)}else setPreview(await requests.post<Impact[]>('/admin/website-settings/preview',value,{errorMode:'local'}))}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <section className="nt-page"><Typography.Title level={2}>网站网关设置</Typography.Title><Alert type="info" title="API 与网站分别配置可信代理；默认不信任转发 IP。仅填写实际反向代理的 IP / CIDR。修改默认服务器证书前请预览受影响的网站。"/>{(error||settings.error||certs.error)&&<Alert type="error" title={error||((settings.error||certs.error) as Error).message}/>}
 <Button onClick={load} disabled={!settings.data}>载入当前设置</Button><Form form={form} layout="vertical" onValuesChange={()=>setPreview(undefined)}><Form.Item name="version" hidden><Input/></Form.Item><Form.Item name="consoleUrl" label="管理控制台地址" rules={[{required:true}]}><Input placeholder="https://console.example.internal"/></Form.Item><Form.Item name="defaultCertificateId" label="默认服务器证书"><Select allowClear options={certs.data?.filter(c=>c.kind==='server'&&!c.revokedAt).map(c=>({label:c.name,value:c.id}))}/></Form.Item><Form.Item name="apiTrustedProxies" label="API 可信代理"><Select mode="tags" tokenSeparators={[',',' ']} options={[]}/></Form.Item><Form.Item name="websiteTrustedProxies" label="网站可信代理"><Select mode="tags" tokenSeparators={[',',' ']} options={[]}/></Form.Item><Space><Button loading={busy} onClick={()=>void run(false)}>预览影响</Button><Button type="primary" loading={busy} disabled={!preview} onClick={()=>void run(true)}>保存设置</Button></Space></Form>
 {preview&&<Table rowKey="id" dataSource={preview} pagination={false} columns={[{title:'网站',dataIndex:'name'},{title:'域名',dataIndex:'domain'},{title:'证书匹配',dataIndex:'certificateValid',render:v=>v?'有效':'不匹配 / 未配置'}]}/>}</section>;
}
