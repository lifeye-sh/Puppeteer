import {Alert, Button, Form, Input, Space, Table, Tag, Typography} from 'antd';
import {useState} from 'react';
import {useQuery, useQueryClient} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import Modal from './BackendFormModal';

type Certificate = {id:string; name:string; fingerprint:string; notBefore:string; notAfter:string; revokedAt?:string; version:number};
export default function BackendPersonalCertificates() {
    const cache = useQueryClient();
    const query = useQuery({queryKey:['r13-personal-certificates'], queryFn:()=>requests.get<{items:Certificate[]}>('/account/certificates',{errorMode:'local'})});
    const [selected,setSelected] = useState<Certificate>();
    const [action,setAction] = useState<'p12'|'revoke'>('p12');
    const [busy,setBusy] = useState(false), [error,setError] = useState('');
    const [form] = Form.useForm();
    const close = () => {setSelected(undefined); setError(''); form.resetFields()};
    const submit = async () => {
        setError('');
        try {
            const values = await form.validateFields();
            if (!selected) return;
            setBusy(true);
            const response = await fetch(`/api/account/certificates/${selected.id}/${action}`, {
                method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({...values,version:selected.version}),
            });
            if (!response.ok) {const body = await response.json(); throw new Error(body.message ?? '操作失败')}
            if (action === 'p12') {
                const url = URL.createObjectURL(await response.blob());
                const link = document.createElement('a'); link.href=url; link.download=`client-${selected.id}.p12`;
                document.body.appendChild(link); link.click(); link.remove();
                setTimeout(()=>URL.revokeObjectURL(url), 1000);
            }
            await cache.invalidateQueries({queryKey:['r13-personal-certificates']});
            close();
        } catch (e) {if(e instanceof Error) setError(e.message)} finally {setBusy(false)}
    };
    const valid = (v:Certificate) => !v.revokedAt && Date.parse(v.notBefore)<=Date.now() && Date.parse(v.notAfter)>Date.now();
    return <>
        <Alert type="info" showIcon title="管理员签发给你的客户端证书" description="下载为加密的 P12 文件，需要当前密码及已绑定验证器的动态口令。用于启用客户端证书认证的网站接入；平台仍会检查账户与资源权限。新增或补发请联系管理员。"/>
        {query.error&&<Alert type="error" title={query.error.message}/>}
        <Table<Certificate> style={{marginTop:12}} size="small" rowKey="id" dataSource={query.data?.items??[]} loading={query.isLoading} pagination={{pageSize:5}} scroll={{x:680}} columns={[
            {title:'名称',dataIndex:'name'},
            {title:'SHA-256 指纹',render:(_,v)=><Typography.Text title={v.fingerprint} copyable={{text:v.fingerprint}}><code>{v.fingerprint.slice(0,16)}…{v.fingerprint.slice(-8)}</code></Typography.Text>},
            {title:'有效期至',dataIndex:'notAfter',render:v=>new Date(v).toLocaleString()},
            {title:'状态',render:(_,v)=><Tag color={valid(v)?'green':'default'}>{v.revokedAt?'已撤销':Date.parse(v.notBefore)>Date.now()?'尚未生效':valid(v)?'有效':'已过期'}</Tag>},
            {title:'操作',render:(_,v)=><Space>{(['p12','revoke'] as const).map(a=><Button key={a} type="link" danger={a==='revoke'} disabled={a==='p12'?!valid(v):!!v.revokedAt} onClick={()=>{form.resetFields();setError('');setAction(a);setSelected(v)}}>{a==='p12'?'下载 P12':'撤销'}</Button>)}</Space>},
        ]}/>
        <Modal open={!!selected} title={`${action==='p12'?'下载':'撤销'}客户端证书`} onCancel={close} onOk={()=>void submit()} confirmLoading={busy} okText={action==='p12'?'验证并下载':'确认撤销'}>
            {error&&<Alert type="error" title={error}/>}
            <Typography.Paragraph>{selected?.name}</Typography.Paragraph>
            {action==='revoke'&&<Alert type="warning" showIcon title="撤销后此证书将无法再用于接入，且不能恢复。"/>}
            <Form name="personal-certificate" form={form} layout="vertical" preserve={false}>
                <Form.Item name="password" label="当前登录密码" rules={[{required:true}]}><Input.Password autoComplete="current-password"/></Form.Item>
                <Form.Item name="totp" label={action==='p12'?'验证器动态口令（必填）':'动态口令（已启用双因素认证时必填）'} rules={action==='p12'?[{required:true,pattern:/^\d{6}$/,message:'请输入 6 位动态口令'}]:[]}><Input autoComplete="one-time-code" maxLength={6}/></Form.Item>
                {action==='p12'&&<Form.Item name="exportPassword" label="P12 文件保护密码" extra="导入证书时需要此密码，请妥善保管。" rules={[{required:true,min:12,max:128,message:'请输入 12–128 位保护密码'}]}><Input.Password autoComplete="new-password" maxLength={128}/></Form.Item>}
            </Form>
        </Modal>
    </>;
}
