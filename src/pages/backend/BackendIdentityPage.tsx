import Modal from './BackendFormModal';
import {Alert, Button, Card, Form, Input, InputNumber, Popconfirm, Select, Space, Table, Tag, Typography} from 'antd';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';
import BackendChannelPolicy from './BackendChannelPolicy';
import BackendStorageQuota from './BackendStorageQuota';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';

type Row={id:string;name?:string;username?:string;nickname?:string;status?:string;type?:string;version?:number;parentId?:string;roleIds?:string[];departmentIds?:string[];permissions?:string[];enabledTotp?:boolean;lockedUntil?:string;failures?:number};
type Kind='users'|'departments'|'roles'|'login-locked';
const title:Record<Kind,string>={users:'用户管理',departments:'部门管理',roles:'角色权限', 'login-locked':'登录锁定'};
export default function BackendIdentityPage({kind}:{kind:Kind}){
    const {account}=useBackendRuntime();const admin=account.actions?.includes('identity.manage');const client=useQueryClient();
    const [channelUser,setChannelUser]=useState<Row>();
    const [quotaUser,setQuotaUser]=useState<Row>();
    const [page,setPage]=useState(1);const [search,setSearch]=useState('');const [error,setError]=useState('');const [editing,setEditing]=useState<Row|null>();const [resetUser,setResetUser]=useState<Row>();const [busy,setBusy]=useState(false);const [form]=Form.useForm();const [resetForm]=Form.useForm();
    const query=useQuery({queryKey:['r02-list',kind,page,search],queryFn:()=>requests.get<Row[]|{items:Row[];total:number}>(`/admin/${kind}${kind==='users'?`/paging?current=${page}&pageSize=20&search=${encodeURIComponent(search)}`:''}`,{errorMode:'local'}),retry:false,refetchInterval:5000});
    const roles=useQuery({queryKey:['r02-roles'],enabled:kind==='users'&&admin,queryFn:()=>requests.get<Row[]>('/admin/roles',{errorMode:'local'})});
    const departments=useQuery({queryKey:['r02-departments'],enabled:kind==='users'&&admin,queryFn:()=>requests.get<Row[]>('/admin/departments',{errorMode:'local'})});
    const permissions=useQuery({queryKey:['r02-permissions'],enabled:kind==='roles',queryFn:()=>requests.get<{action:string;label:string}[]>('/admin/permissions',{errorMode:'local'})});
    const rows=Array.isArray(query.data)?query.data:query.data?.items??[];
    const run=async(fn:()=>Promise<unknown>)=>{setError('');setBusy(true);try{await fn();setEditing(undefined);setResetUser(undefined);await client.invalidateQueries({predicate:q=>String(q.queryKey[0]).startsWith('r02-')});await client.invalidateQueries({queryKey:['backend-runtime']})}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
    const edit=(row:Row|null)=>{setError('');setEditing(row);form.resetFields();form.setFieldsValue(row?{...row,password:''}:{type:'user',status:'enabled',roleIds:[],departmentIds:[],permissions:[],weight:0,parentId:''})};
    const operations=(row:Row)=><Space wrap>{kind!=='login-locked'&&<Button size="small" onClick={()=>edit(row)}>编辑</Button>}
        {kind==='users'&&<><Button size="small" onClick={()=>setChannelUser(row)}>通道策略</Button><Button size="small" onClick={()=>setQuotaUser(row)}>存储配额</Button><Popconfirm title="撤销该用户全部会话？" onConfirm={()=>run(()=>requests.post(`/admin/users/${row.id}/revoke-sessions`,undefined,{errorMode:'local'}))}><Button size="small">撤销会话</Button></Popconfirm><Button size="small" onClick={()=>{setError('');resetForm.resetFields();setResetUser(row)}}>重置 MFA</Button></>}
        <Popconfirm title={kind==='login-locked'?'解除锁定？':'删除此记录？被引用记录和最后一个管理员不能删除。'} onConfirm={()=>run(()=>requests.delete(`/admin/${kind}/${encodeURIComponent(row.id)}`,{errorMode:'local'}))}><Button size="small" danger>{kind==='login-locked'?'解锁':'删除'}</Button></Popconfirm>
    </Space>;
    const columns=kind==='users'?[
        {title:'账号',dataIndex:'username'},{title:'昵称',dataIndex:'nickname'},{title:'类型',dataIndex:'type'},{title:'状态',dataIndex:'status'},
        {title:'MFA',dataIndex:'enabledTotp',render:(v:boolean)=><Tag color={v?'green':'default'}>{v?'已绑定':'未绑定'}</Tag>},
        {title:'角色数 / 部门数',render:(_:unknown,r:Row)=>`${r.roleIds?.length??0} / ${r.departmentIds?.length??0}`}
    ]:kind==='departments'?[
        {title:'部门',dataIndex:'name'},{title:'上级部门',dataIndex:'parentId',render:(v:string)=>rows.find(r=>r.id===v)?.name||'根部门'},{title:'排序',dataIndex:'weight'}
    ]:kind==='roles'?[
        {title:'角色',dataIndex:'name'},{title:'查看权限',dataIndex:'permissions',render:(v:string[])=>v.map(p=><Tag key={p}>{permissions.data?.find(x=>x.action===p)?.label||p}</Tag>)}
    ]:[{title:'类型',dataIndex:'kind',render:(v:string)=>v==='ip'?'来源 IP':'账号'},{title:'锁定对象',dataIndex:'id'},{title:'失败次数',dataIndex:'failures'},{title:'锁定到',dataIndex:'lockedUntil',render:(v:string)=>new Date(v).toLocaleString()}];
    return <section className="nt-page"><Modal title={`通道策略 · ${channelUser?.username??''}`} open={!!channelUser} footer={null} onCancel={()=>setChannelUser(undefined)} destroyOnHidden>{channelUser&&<BackendChannelPolicy userId={channelUser.id}/>}</Modal><Modal title={`存储配额 · ${quotaUser?.username??''}`} open={!!quotaUser} footer={null} onCancel={()=>setQuotaUser(undefined)} destroyOnHidden>{quotaUser&&<BackendStorageQuota userId={quotaUser.id}/>}</Modal><Typography.Title level={2}>{title[kind]}</Typography.Title>
        <Alert type="info" showIcon title={admin?'系统管理员可以维护身份与权限':'当前为按角色授权的只读访问'} description="本轮角色可授予各模块查看权限；身份修改、角色授权、解锁及 MFA 重置为系统管理员专属操作。服务端会独立校验权限。"/>
        {(error||query.isError)&&<Alert type="error" showIcon title={error||query.error?.message}/>}
        <Card extra={<Space>{kind==='users'&&<Input.Search placeholder="搜索账号或昵称" allowClear onSearch={v=>{setPage(1);setSearch(v)}}/>}<Button onClick={()=>void query.refetch()}>刷新</Button>{admin&&kind!=='login-locked'&&<Button type="primary" onClick={()=>edit(null)}>新建</Button>}</Space>}>
            <Table<Row> rowKey="id" loading={query.isPending} dataSource={rows} columns={[...columns,...(admin?[{title:'操作',render:(_:unknown,r:Row)=>operations(r)}]:[])]} scroll={{x:850}}
                pagination={kind==='users'?{current:page,pageSize:20,total:!Array.isArray(query.data)?query.data?.total:0,onChange:setPage,showSizeChanger:false}:false}/>
        </Card>
        <Modal title={`${editing?'编辑':'新建'}${title[kind]}`} open={editing!==undefined} onCancel={()=>setEditing(undefined)} confirmLoading={busy} onOk={()=>form.submit()} forceRender okText="保存" cancelText="取消">
            {error&&<Alert type="error" title={error}/>}
            <Form form={form} layout="vertical" onFinish={values=>void run(()=>{const payload={...editing,...values};return editing?requests.put(`/admin/${kind}/${editing.id}`,payload,{errorMode:'local'}):requests.post(`/admin/${kind}`,payload,{errorMode:'local'})})}>
                {kind==='users'?<>
                    <Form.Item name="username" label="账号" rules={[{required:true,pattern:/^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$/}]}><Input disabled={!!editing}/></Form.Item>
                    <Form.Item name="nickname" label="昵称" rules={[{required:true,max:100}]}><Input/></Form.Item>
                    <Form.Item name="password" label={editing?'新密码（留空保持不变）':'初始密码'} extra="密码须符合平台设置中的密码强度策略。" rules={[{required:!editing,max:1024}]}><Input.Password autoComplete="new-password"/></Form.Item>
                    <Form.Item name="type" label="类型"><Select options={[{value:'user',label:'普通用户'},{value:'admin',label:'系统管理员'}]}/></Form.Item>
                    <Form.Item name="status" label="状态"><Select options={[{value:'enabled',label:'启用'},{value:'disabled',label:'禁用'}]}/></Form.Item>
                    <Form.Item name="roleIds" label="角色"><Select mode="multiple" options={roles.data?.map(r=>({value:r.id,label:r.name}))}/></Form.Item>
                    <Form.Item name="departmentIds" label="部门"><Select mode="multiple" options={departments.data?.map(r=>({value:r.id,label:r.name}))}/></Form.Item>
                    <Form.Item name="mail" label="邮箱"><Input/></Form.Item><Form.Item name="remark" label="备注"><Input.TextArea maxLength={1000}/></Form.Item>
                </>:<><Form.Item name="name" label="名称" rules={[{required:true,max:100}]}><Input/></Form.Item>
                    {kind==='departments'?<><Form.Item name="parentId" label="上级部门"><Select options={[{value:'',label:'根部门'},...rows.filter(r=>r.id!==editing?.id).map(r=>({value:r.id,label:r.name}))]}/></Form.Item><Form.Item name="weight" label="排序"><InputNumber min={0} max={100000}/></Form.Item></>:<Form.Item name="permissions" label="查看权限"><Select mode="multiple" options={permissions.data?.map(p=>({value:p.action,label:p.label}))}/></Form.Item>}
                </>}
            </Form>
        </Modal>
        <Modal title={`重置 ${resetUser?.username??''} 的 MFA`} open={!!resetUser} onCancel={()=>setResetUser(undefined)} onOk={()=>resetForm.submit()} confirmLoading={busy} forceRender okText="保存" cancelText="取消">
            <Alert type="warning" className="mb-4" title="会撤销目标全部会话，下次登录必须重新绑定。操作者和原因会写入审计。"/>
            {error&&<Alert type="error" title={error}/>}
            <Form form={resetForm} layout="vertical" onFinish={v=>void run(()=>requests.post(`/admin/users/${resetUser?.id}/reset-mfa`,v,{errorMode:'local'}))}>
                <Form.Item name="password" label="你的管理员密码" rules={[{required:true}]}><Input.Password/></Form.Item>
                {account.enabledTotp&&<Form.Item name="totp" label="你的动态口令" rules={[{required:true,pattern:/^\d{6}$/}]}><Input/></Form.Item>}
                <Form.Item name="reason" label="重置原因" rules={[{required:true,min:5,max:500}]}><Input.TextArea/></Form.Item>
            </Form>
        </Modal>
    </section>;
}
