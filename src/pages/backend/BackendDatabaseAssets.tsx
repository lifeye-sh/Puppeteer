import {Alert,Button,Card,Form,Input,InputNumber,Popconfirm,Select,Space,Switch,Table,Tag,Typography} from 'antd';
import {useState} from 'react';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {Link} from 'react-router-dom';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal,{FormTabs} from './BackendFormModal';
import BackendPostgresConnection from './BackendPostgresConnection';
import BackendCreateCredential from './BackendCreateCredential';

type DatabaseAsset={tlsMode?:string;id:string;engine:'mysql'|'postgresql';name:string;alias:string;host:string;port:number;database:string;readonlyCredentialId:string;writeCredentialId:string;nativeWriteCredentialId:string;nativeWriteAvailable?:boolean;tlsServerName:string;tlsCa:string;connectTimeout:number;description:string;enabled:boolean;version:number};
type Credential={id:string;name:string;kind:string;username:string};
type Diagnostic={profile:string;ok:boolean;message:string;durationMs:number};
type ProxyEndpoint={ready:boolean;host:string;port:string};
export default function BackendDatabaseAssets({portal=false}:{portal?:boolean}){
 const {account}=useBackendRuntime();const admin=!portal&&account.actions?.includes('identity.manage');const cache=useQueryClient();
 const [search,setSearch]=useState(''),[error,setError]=useState(''),[busy,setBusy]=useState(false),[editing,setEditing]=useState<DatabaseAsset|null>();const [form]=Form.useForm();
 const [credentialTarget,setCredentialTarget]=useState<'readonlyCredentialId'|'writeCredentialId'|'nativeWriteCredentialId'>();
 const [connecting,setConnecting]=useState<DatabaseAsset>();
 const engine=Form.useWatch('engine',form); const tlsMode=Form.useWatch('tlsMode',form);
 const [diagnostics,setDiagnostics]=useState<{name:string;items:Diagnostic[]}>();
 const list=useQuery({queryKey:['r12-databases',portal],queryFn:()=>requests.get<{items:DatabaseAsset[];total:number}>(portal?'/portal/database-assets':'/admin/database-assets',{errorMode:'local'}),refetchInterval:15000});
 const proxy=useQuery({queryKey:['r12-proxy-status'],queryFn:()=>requests.get<ProxyEndpoint&{leaseSeconds:number;postgres?:ProxyEndpoint}>('/database-proxy/status',{errorMode:'local'}),refetchInterval:10000});
 const credentials=useQuery({queryKey:['r12-database-credentials'],enabled:!!admin,queryFn:()=>requests.get<Credential[]>('/admin/credentials',{errorMode:'local'})});
 const run=async(fn:()=>Promise<void>)=>{setBusy(true);setError('');try{await fn();await cache.invalidateQueries({queryKey:['r12-databases']})}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const edit=(asset:DatabaseAsset|null)=>{form.resetFields();form.setFieldsValue(asset?{...asset,tlsMode:asset.tlsMode||"verify-full"}:{tlsMode:"verify-full",engine:'mysql',port:3306,connectTimeout:10,enabled:true,writeCredentialId:'',nativeWriteCredentialId:''});setError('');setEditing(asset)};
 const options=credentials.data?.filter(c=>c.kind==='password').map(c=>({value:c.id,label:`${c.name} · ${c.username}`}));
 const quote=(v:string)=>"'"+v.replaceAll("'","'\\''")+"'";
 const pg=connecting?.engine==='postgresql';
 const endpoint=pg?proxy.data?.postgres:proxy.data;
 const pgURI=connecting&&endpoint?`postgresql://${encodeURIComponent(`${account.username}@${connecting.alias}`)}@${endpoint.host.includes(':')?`[${endpoint.host}]`:endpoint.host}:${Number(endpoint.port)}/${encodeURIComponent(connecting.database)}?sslmode=verify-full&sslrootcert=next-terminal-mysql.pem`:'';
 const command=connecting&&endpoint?(pg?`psql --dbname=${quote(pgURI)} --password`:`mysql --host=${quote(endpoint.host)} --port=${Number(endpoint.port)} --user=${quote(`${account.username}@${connecting.alias}`)} --database=${quote(connecting.database)} --ssl-mode=VERIFY_IDENTITY --ssl-ca='./next-terminal-mysql.pem' --password`):'';
 return <section className="nt-page"><Typography.Title level={2}>{portal?'我的数据库':'数据库资产'}</Typography.Title>
 <Alert style={{marginBottom:16}} type={proxy.data?.ready?'info':'warning'} title={proxy.data?.ready?`MySQL 代理：${proxy.data.host}:${proxy.data.port} · TLS 证书校验必需`:'MySQL 代理尚未就绪'} description={<Space wrap><span>客户端请选择 caching_sha2_password 与 VERIFY_IDENTITY；密码在“我的账号”创建。</span><Button disabled={!proxy.data?.ready} href="/api/database-proxy/certificate" download="next-terminal-mysql.pem">下载代理证书</Button><Link to="/x-db-work-order">我的 SQL 工单</Link></Space>}/>
 <Alert style={{marginBottom:16}} type={proxy.data?.postgres?.ready?'info':'warning'} title={proxy.data?.postgres?.ready?`PostgreSQL 只读预览：${proxy.data.postgres.host}:${proxy.data.postgres.port}`:'PostgreSQL 代理尚未就绪'} description={<Space wrap><span>TLS verify-full / SCRAM-SHA-256；支持查询、参数绑定与取消。SQL 工单尚未开放。</span><Button disabled={!proxy.data?.postgres?.ready} href="/api/database-proxy/certificate" download="next-terminal-mysql.pem">下载代理证书（两种协议通用）</Button></Space>}/>
 {(error||list.error)&&<Alert type="error" title={error||list.error?.message} showIcon/>}
 <Card extra={<Space><Input.Search placeholder="搜索名称、别名、主机" allowClear onChange={e=>setSearch(e.target.value)}/><Button loading={list.isFetching} onClick={()=>void list.refetch()}>刷新</Button>{admin&&<Button type="primary" onClick={()=>edit(null)}>新建数据库</Button>}</Space>}>
 <Table<DatabaseAsset> rowKey="id" loading={list.isLoading} scroll={{x:950}} dataSource={(list.data?.items??[]).filter(a=>`${a.name} ${a.alias} ${a.host} ${a.database}`.toLowerCase().includes(search.toLowerCase()))} columns={[
 {title:'名称',dataIndex:'name',render:(name,a)=><Space orientation="vertical" size={0}><strong>{name}</strong><Typography.Text type="secondary">{a.description}</Typography.Text></Space>},
 {title:'连接别名',dataIndex:'alias'}, {title:'数据库',dataIndex:'database'}, {title:'目标地址',render:(_,a)=>`${a.host}:${a.port}`},
 {title:'协议',render:(_,a)=> <Tag color={a.engine==='postgresql'?'cyan':'blue'}>{a.engine==='postgresql'?'PostgreSQL':'MySQL'} · TLS</Tag>}, {title:'状态',render:(_,a)=><Tag color={a.enabled?'green':'default'}>{a.enabled?'启用':'停用'}</Tag>},
 ...(portal?[{title:'连接身份',render:(_:unknown,a:DatabaseAsset)=><Space orientation="vertical"><Typography.Text copyable>{`${account.username}@${a.alias}`}</Typography.Text><Button type="link" disabled={!a.enabled||!(a.engine==='postgresql'?proxy.data?.postgres?.ready:proxy.data?.ready)} onClick={()=>setConnecting(a)}>连接说明 / 复制命令</Button></Space>}]:[]),
 ...(admin?[{title:'操作',render:(_:unknown,a:DatabaseAsset)=><Space><Button type="link" onClick={()=>edit(a)}>编辑</Button><Button type="link" disabled={!a.enabled||busy} onClick={()=>void run(async()=>{const result=await requests.post<{items:Diagnostic[]}>(`/admin/database-assets/${a.id}/test`,{},{errorMode:'local'});setDiagnostics({name:a.name,items:result.items})})}>测试连接</Button><Popconfirm title="删除此数据库资产？" description="现有授权将不再提供此资产的访问。" onConfirm={()=>run(async()=>{await requests.delete(`/admin/database-assets/${a.id}?version=${a.version}`,{errorMode:'local'})})}><Button type="link" danger disabled={busy}>删除</Button></Popconfirm></Space>}]:[])
 ]}/></Card>
 {portal&&<Typography.Paragraph style={{marginTop:16}}>数据库专用密码在 <Link to="/info">我的账号</Link> 中创建和撤销。连接使用平台代理地址，目标数据库凭据由平台保管。</Typography.Paragraph>}
 <Modal open={!!diagnostics} title={`${diagnostics?.name??''} · 连接测试`} onCancel={()=>setDiagnostics(undefined)} footer={<Button onClick={()=>setDiagnostics(undefined)}>关闭</Button>}>
 <Typography.Paragraph>分别校验 TLS、凭据、数据库范围和最小权限，并执行 SELECT 1。此测试不会写入目标数据。</Typography.Paragraph>
 <Space orientation="vertical" style={{width:'100%'}}>{diagnostics?.items.map(v=><Alert key={v.profile} type={v.ok?'success':'error'} showIcon title={`${v.profile==='readonly'?'原生只读连接':v.profile==='native-dml'?'原生 DML 连接':'审批执行连接'} · ${v.durationMs} ms`} description={v.message}/>)}</Space>
 </Modal>
 <Modal open={!!connecting} title={`${connecting?.name??''} · 客户端连接`} onCancel={()=>setConnecting(undefined)} footer={<Button onClick={()=>setConnecting(undefined)}>关闭</Button>}>
 {pg&&connecting&&endpoint?<BackendPostgresConnection host={endpoint.host} port={endpoint.port} database={connecting.database} username={`${account.username}@${connecting.alias}`} command={command}/>:<>
 <Typography.Paragraph>下载代理证书，放在命令执行目录；在“我的账号”生成数据库专用密码。下列 Linux / macOS 命令会单独提示输入密码，不包含目标数据库凭据。</Typography.Paragraph>
 <Typography.Paragraph copyable={{text:command}} style={{fontFamily:'monospace',whiteSpace:'pre-wrap',overflowWrap:'anywhere'}}>{command}</Typography.Paragraph>
 <Typography.Paragraph>{pg?'PostgreSQL 使用平台专用密码，旧版 MySQL 专用密码需先由本人重置以生成 SCRAM 材料。DBeaver 选择 PostgreSQL 驱动，在 SSL 中选择 verify-full 并指定下载的根证书；DBeaver 完整兼容验收仍在进行。':'DBeaver 选择 MySQL 驱动，在 SSL 中信任下载的代理证书，启用 VERIFY_IDENTITY。'}主机、端口和用户名按上方命令填写；backend.localhost 在本机应解析到 127.0.0.1。</Typography.Paragraph>
 </>}
 {connecting?.nativeWriteAvailable&&<Alert type="warning" title="当前允许受控 DML：此连接可执行 INSERT / UPDATE / DELETE。请确认事务提交和回滚；仍禁止 DDL、CALL、文件操作及跨库写入。"/>}
 <Alert type="info" title={`${connecting?.nativeWriteAvailable?'此资产启用独立原生 DML 写入；需要审批的变更请提交 SQL 工单':`原生客户端只允许只读查询${pg?'；PostgreSQL SQL 工单尚未开放':'；写操作请提交 SQL 工单'}`}。个人密码撤销、授权失效或资产停用后，连接会在下一条命令或 5 秒租约检查时关闭。`}/>
 </Modal>
 <Modal open={editing!==undefined} title={editing?'编辑数据库':'新建数据库'} onCancel={()=>setEditing(undefined)} onOk={()=>form.submit()} confirmLoading={busy} forceRender>
 {error&&<Alert type="error" title={error}/>}<Form form={form} layout="vertical" onFinish={v=>void run(async()=>{const body={...v,tlsMode:v.tlsMode||'',tlsServerName:v.tlsMode==='disable'?'':v.tlsServerName,tlsCa:v.tlsMode==='disable'?'':v.tlsCa,writeCredentialId:v.writeCredentialId||'',nativeWriteCredentialId:v.nativeWriteCredentialId||'',description:v.description||'',version:editing?.version??0};if(editing)await requests.put('/admin/database-assets/'+editing.id,body,{errorMode:'local'});else await requests.post('/admin/database-assets',body,{errorMode:'local'});setEditing(undefined)})}>
 <FormTabs sections={[
 {key:'connection',label:'连接信息',description:'别名用于客户端登录身份，请使用字母开头的唯一名称。',children:<>
 <Form.Item name="engine" label="数据库类型" rules={[{required:true}]} extra={editing?'类型创建后不可修改，请为不同类型新建资产。':undefined}><Select disabled={!!editing} options={[{value:'mysql',label:'MySQL'},{value:'postgresql',label:'PostgreSQL'}]} onChange={value=>{const port=form.getFieldValue('port');if(port===3306||port===5432)form.setFieldValue('port',value==='postgresql'?5432:3306);if(value==='postgresql'){form.setFieldValue('writeCredentialId','');form.setFieldValue('nativeWriteCredentialId','')}}}/></Form.Item>
 {engine==='postgresql'&&<Alert className="nt-form-span" type="info" title="支持 TLS、SCRAM 和只读权限诊断。原生代理已开放只读预览，连接入口见“我的数据库”；SQL 工单尚未开放。"/>}
 <Form.Item name="name" label="名称" rules={[{required:true}]}><Input maxLength={128}/></Form.Item>
 <Form.Item name="alias" label="连接别名" rules={[{required:true},{pattern:/^[A-Za-z][A-Za-z0-9_-]{0,63}$/,message:'使用字母开头，最多64位字母、数字、下划线或短横线'}]}><Input placeholder="inventory"/></Form.Item>
 <Form.Item name="host" label="主机" extra="填写后端服务可访问的数据库目标地址；容器部署时 localhost 指后端容器自身。DBeaver 的本机代理地址应填写在 DBeaver 连接中。" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="port" label="端口" rules={[{required:true}]}><InputNumber min={1} max={65535}/></Form.Item>
 <Form.Item name="database" label="默认数据库" rules={[{required:true}]}><Input maxLength={64}/></Form.Item><Form.Item name="connectTimeout" label="连接超时（秒）" rules={[{required:true}]}><InputNumber min={1} max={60}/></Form.Item>
 <Form.Item name="enabled" label="启用" valuePropName="checked"><Switch/></Form.Item><Form.Item name="description" label="说明"><Input.TextArea rows={2} maxLength={2000}/></Form.Item></>},
 {key:'security',label:'凭据与 TLS',description:engine==='postgresql'?'使用 SCRAM 密码的独立只读登录角色，移除角色继承、CREATE/TEMP、写权限、对象所有权及自定义函数执行权限（包括 PUBLIC 授权）。默认验证 TLS 名称与信任链；未启用 TLS 的内网目标可明确选择兼容模式。':'只读凭据须在 MySQL 中仅拥有查询权限；审批执行使用独立凭据。默认验证 TLS 名称与信任链；未启用 TLS 的内网目标可明确选择兼容模式。',children:<>
 <Form.Item name="readonlyCredentialId" label="只读凭据" extra={admin&&<Button type="link" size="small" onClick={()=>setCredentialTarget('readonlyCredentialId')}>新建只读凭据</Button>} rules={[{required:true}]}><Select options={options} showSearch optionFilterProp="label"/></Form.Item>
 <Form.Item name="writeCredentialId" label="审批执行凭据" extra={admin&&<Button type="link" size="small" onClick={()=>setCredentialTarget('writeCredentialId')}>新建审批执行凭据</Button>} hidden={engine==='postgresql'} dependencies={['readonlyCredentialId']} rules={[({getFieldValue})=>({validator:(_,value)=>!value||value!==getFieldValue('readonlyCredentialId')?Promise.resolve():Promise.reject(new Error('必须使用独立凭据'))})]}><Select allowClear options={options} showSearch optionFilterProp="label"/></Form.Item>
 <Form.Item name="nativeWriteCredentialId" label="原生 DML 凭据（可选）" hidden={engine==='postgresql'} dependencies={['readonlyCredentialId','writeCredentialId']} extra={<><span>独立账号仅授予当前数据库 SELECT / INSERT / UPDATE / DELETE；不得有 DDL、管理或其他库权限。全局允许且用户具有写权限后，仅新连接生效。</span>{admin&&<Button type="link" size="small" onClick={()=>setCredentialTarget('nativeWriteCredentialId')}>新建 DML 凭据</Button>}</>} rules={[({getFieldValue})=>({validator:(_,value)=>!value||![getFieldValue('readonlyCredentialId'),getFieldValue('writeCredentialId')].includes(value)?Promise.resolve():Promise.reject(new Error('必须与只读和审批执行使用不同凭据'))})]}><Select allowClear options={options} showSearch optionFilterProp="label"/></Form.Item>
 <Form.Item name="tlsMode" label="目标数据库传输模式" initialValue="verify-full"><Select options={[{value:"verify-full",label:"TLS 证书校验（默认）"},{value:"disable",label:"无 TLS 兼容模式"}]}/></Form.Item>
 {tlsMode==='disable'?<Alert type="warning" title="仅目标数据库连接不使用 TLS" description="目标连接中的 SQL、结果和部分认证交换可能未经加密，仅用于可信内网或受保护链路。客户端连接平台仍须使用 TLS，权限和审计保持生效。"/>:<> <Form.Item name="tlsServerName" label="TLS 服务器名称" rules={[{required:true}]}><Input placeholder="db.internal.example"/></Form.Item>
 <Form.Item name="tlsCa" label="受信任 CA（PEM）" rules={[{required:true}]}><Input.TextArea rows={6} maxLength={32768} placeholder="-----BEGIN CERTIFICATE-----"/></Form.Item></>}</>}
 ]}/></Form></Modal>
 {credentialTarget&&editing!==undefined&&<BackendCreateCredential passwordOnly onCancel={()=>setCredentialTarget(undefined)} onCreated={credential=>{form.setFieldValue(credentialTarget,credential.id);setCredentialTarget(undefined)}}/>}
 </section>
}
