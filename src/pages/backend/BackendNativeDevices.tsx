import {Alert, Button, Form, Input, Space, Table, Tag, Typography} from 'antd';
import {ReloadOutlined} from '@ant-design/icons';
import {useState} from 'react';
import {useQuery, useQueryClient} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal from './BackendFormModal';

type Device = {
  id: string; name: string; os: string; arch: string; clientVersion: string;
  sourceIp: string; registeredAt: string; lastSeenAt?: string; expiresAt: string;
  revokedAt?: string; scopes: string[]; status: 'active'|'revoked'|'expired'|'invalid';
};
const statusLabels = {active:'已授权', revoked:'已撤销', expired:'已过期', invalid:'已失效'};
const time = (value: string) => new Date(value).toLocaleString();
const scopeLabels: Record<string,string> = {'assets:read':'查看资产', 'sessions:connect':'连接会话', offline_access:'保持登录'};

export default function BackendNativeDevices() {
  const {account} = useBackendRuntime();
  const cache = useQueryClient();
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Device>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [form] = Form.useForm();
  const query = useQuery({
    queryKey:['native-devices', account.id, page],
    queryFn:()=>requests.get<{items:Device[]; total:number}>(`/account/native-devices?page=${page}`,{errorMode:'local'}),
    refetchInterval:30000,
  });
  const close = () => { setSelected(undefined); setError(''); form.resetFields(); };
  const revoke = async () => {
    if (!selected) return;
    setError('');
    try {
      const proof = await form.validateFields();
      setBusy(true);
      const response = await fetch(`/api/account/native-devices/${selected.id}`, {
        method:'DELETE', headers:{'Content-Type':'application/json'}, body:JSON.stringify(proof),
      });
      if (!response.ok) throw Error((await response.json()).message ?? '撤销失败');
      await cache.invalidateQueries({queryKey:['native-devices', account.id]});
      close();
    } catch (e) { if (e instanceof Error) setError(e.message); }
    finally { setBusy(false); }
  };
  return <Space orientation="vertical" size={16} style={{width:'100%'}}>
    <Alert type="info" showIcon title="管理原生客户端登录"
      description="每次浏览器授权对应一条独立登录记录。撤销后，该记录的访问与刷新令牌立即失效，活动连接将在授权检查时断开；其他设备不受影响。设备名称、系统和版本由客户端上报，不代表可信硬件身份。"/>
    <Space style={{width:'100%', justifyContent:'space-between'}}>
      <Typography.Text type="secondary">共 {query.data?.total ?? 0} 条登录记录</Typography.Text>
      <Button icon={<ReloadOutlined/>} loading={query.isFetching} onClick={()=>void query.refetch()}>刷新</Button>
    </Space>
    {query.error && <Alert type="error" showIcon title={query.error.message}/>}
    <Table<Device> rowKey="id" size="small" loading={query.isLoading} dataSource={query.data?.items ?? []}
      locale={{emptyText:'暂无原生客户端登录。使用 nt-client 完成浏览器授权后自动登记。'}}
      pagination={{current:page, pageSize:20, total:query.data?.total ?? 0, showSizeChanger:false, onChange:setPage}}
      scroll={{x:950}} columns={[
        {title:'设备 / 客户端', key:'device', width:220, render:(_,v)=><><Typography.Text strong>{v.name}</Typography.Text><div><Typography.Text type="secondary">{v.os} · {v.arch} · {v.clientVersion}</Typography.Text></div></>},
        {title:'授权范围', key:'scope', render:(_,v)=><Space size={[0,4]} wrap>{v.scopes.map(s=><Tag key={s}>{scopeLabels[s] ?? s}</Tag>)}</Space>},
        {title:'登录来源', dataIndex:'sourceIp', render:v=>v || '未记录'},
        {title:'最近使用 / 登记时间', key:'time', width:210, render:(_,v)=><>{v.lastSeenAt ? time(v.lastSeenAt) : '尚无使用记录'}<div><Typography.Text type="secondary">登记于 {time(v.registeredAt)}</Typography.Text></div></>},
        {title:'状态', key:'status', width:110, render:(_,v)=><Tag color={v.status==='active'?'green':v.status==='expired'?'orange':'default'}>{statusLabels[v.status]}</Tag>},
        {title:'操作', key:'action', width:90, render:(_,v)=><Button type="link" danger disabled={!!v.revokedAt || v.status==='expired'} onClick={()=>{form.resetFields();setError('');setSelected(v)}}>撤销</Button>},
      ]}/>
    <Modal open={!!selected} width={520} title={`撤销 ${selected?.name ?? '设备登录'}`} okText="撤销登录"
      okButtonProps={{danger:true}} confirmLoading={busy} closable={!busy} maskClosable={!busy}
      cancelButtonProps={{disabled:busy}} onCancel={close} onOk={revoke}>
      {error && <Alert type="error" showIcon title={error} style={{marginBottom:16}}/>}
      <Typography.Paragraph>该客户端需要重新完成浏览器授权才能再次连接。请输入当前平台密码确认。</Typography.Paragraph>
      <Form name="native-device-revoke" form={form} layout="vertical">
        <Form.Item name="password" label="当前平台密码" rules={[{required:true,message:'请输入当前平台密码'}]}><Input.Password autoComplete="current-password"/></Form.Item>
        <Form.Item name="totp" label="动态口令（启用 MFA 时必填）"><Input maxLength={6} autoComplete="one-time-code"/></Form.Item>
      </Form>
    </Modal>
  </Space>;
}
