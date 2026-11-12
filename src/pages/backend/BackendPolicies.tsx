import Modal, {FormTabs} from './BackendFormModal';
import {pagedOptions} from './r08-options';
import {Alert,Button,Card,Checkbox,Form,Input,Popconfirm,Select,Space,Switch,Table,Tag,Typography} from 'antd';
import {MinusCircleOutlined,PlusOutlined} from '@ant-design/icons';
import {useQuery,useQueryClient} from '@tanstack/react-query';
import {useState} from 'react';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';

type Kind='access-policy'|'strategy'|'command-filter'|'ip-set';
type Named={id:string;name?:string;username?:string;nickname?:string};
type Policy={id:string;version:number;name:string;enabled:boolean;global:boolean;mode:string;timezone:string;userIds:string[];departmentIds:string[];resourceIds:string[];entries:string[];actions:Record<string,boolean>;patterns:{pattern:string;action:string}[];rules:Record<string,unknown>[]};
const labels:Record<Kind,string>={'access-policy':'访问策略','strategy':'动作策略','command-filter':'命令过滤','ip-set':'IP 集合'};
const actions:Record<string,string>={connect:'建立连接',upload:'上传',download:'下载',createFile:'新建文件',createDir:'新建目录',edit:'编辑文件',delete:'删除',rename:'重命名',chmod:'修改权限',copy:'远端剪贴板复制',paste:'粘贴到远端',execute:'执行命令 / 终端输入',observe:'观察会话',share:'分享会话'};
const modes=[{value:'whitelist',label:'白名单：未匹配允许规则时拒绝'},{value:'blacklist',label:'黑名单：匹配拒绝规则时拒绝'}];
const decisions=[{value:'allow',label:'允许'},{value:'reject',label:'拒绝'}];
const options=(v?:Named[])=>v?.map(x=>({value:x.id,label:x.name||`${x.username} · ${x.nickname||''}`}))??[];

export default function BackendPolicies({kind}:{kind:Kind}){
 const {account}=useBackendRuntime();const admin=account.actions?.includes('identity.manage');const client=useQueryClient();const [form]=Form.useForm();const [editing,setEditing]=useState<Policy|null>();const [error,setError]=useState('');const [busy,setBusy]=useState(false);const [geo,setGeo]=useState(false);const [geoForm]=Form.useForm();
 const list=useQuery({queryKey:['r08-policies',kind],queryFn:()=>requests.get<Policy[]>(`/admin/policies/${kind}`,{errorMode:'local'})});
 const users=useQuery({queryKey:['r08-users'],enabled:!!admin,queryFn:()=>pagedOptions<Named>('/admin/users/paging')});
 const departments=useQuery({queryKey:['r08-departments'],enabled:!!admin,queryFn:()=>requests.get<Named[]>('/admin/departments',{errorMode:'local'})});
 const resources=useQuery({queryKey:['r08-assets'],enabled:!!admin,queryFn:()=>pagedOptions<Named>('/admin/assets/paging').then(items=>({items}))});
 const groups=useQuery({queryKey:['r08-groups'],enabled:!!admin,queryFn:()=>requests.get<Named[]>('/admin/resource-groups',{errorMode:'local'})});
 const websites=useQuery({queryKey:['r09-policy-websites'],enabled:!!admin,queryFn:()=>requests.get<Named[]>('/admin/websites',{errorMode:'local'})});
 const ipsets=useQuery({queryKey:['r08-ipsets'],enabled:kind==='access-policy',queryFn:()=>requests.get<Policy[]>('/admin/policies/ip-set',{errorMode:'local'})});
 const geodata=useQuery({queryKey:['r08-geodata'],enabled:geo,queryFn:()=>requests.get<{id:string;name:string;active:boolean;entries:number}[]>('/admin/geodata',{errorMode:'local'})});
 const run=async(fn:()=>Promise<unknown>)=>{setError('');setBusy(true);try{await fn();setEditing(undefined);await client.invalidateQueries({predicate:q=>String(q.queryKey[0]).startsWith('r08-')})}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const edit=(p:Policy|null)=>{form.resetFields();form.setFieldsValue(p?{...p,entriesText:p.entries?.join('\n')}:{enabled:true,global:false,timezone:'Asia/Shanghai',mode:'blacklist',rules:[],patterns:[],actions:{},userIds:[],departmentIds:[],resourceIds:[]});setEditing(p)};
 const save=async()=>{const v=await form.validateFields();delete v.entriesText;const entries=(form.getFieldValue('entriesText')||'').split(/\n/).map((s:string)=>s.trim()).filter(Boolean);await run(()=>requests[editing?'put':'post'](`/admin/policies/${kind}${editing?'/'+editing.id:''}`,{...v,entries,version:editing?.version},{errorMode:'local'}))};
 return <section className="nt-page"><Typography.Title level={2}>{labels[kind]}</Typography.Title>
  <Alert type="info" showIcon title={kind==='command-filter'?'启用命令过滤后，SSH 仅允许使用受审计的命令执行框；原始终端输入被禁用。正则规则无法约束脚本内部执行的命令。':kind==='strategy'?'动作限制与资产授权共同生效。未配置的动作不增加限制，拒绝规则优先。':'策略按服务端时间和连接来源地址判断。地域规则缺少匹配数据时拒绝访问。'} style={{marginBottom:16}}/>
  {(error||list.error)&&<Alert type="error" title={error||(list.error as Error).message}/>}
  <Space style={{marginBottom:16}}><Button onClick={()=>list.refetch()}>刷新</Button>{admin&&<Button type="primary" onClick={()=>edit(null)}>新建{labels[kind]}</Button>}{admin&&kind==='access-policy'&&<Button onClick={()=>setGeo(true)}>地域数据</Button>}</Space>
  <Table rowKey="id" loading={list.isLoading} dataSource={list.data} columns={[{title:'名称',dataIndex:'name'},{title:'状态',dataIndex:'enabled',render:(v:boolean)=><Tag color={v?'green':'default'}>{v?'启用':'停用'}</Tag>},{title:'生效对象',render:(_:unknown,p:Policy)=>p.global?'全部用户':`${p.userIds?.length||0} 用户 / ${p.departmentIds?.length||0} 部门`},{title:'版本',dataIndex:'version'},{title:'操作',render:(_:unknown,p:Policy)=>admin&&<Space><Button onClick={()=>edit(p)}>编辑</Button><Popconfirm title="删除此策略？已被授权引用的策略不能删除。" onConfirm={()=>run(()=>requests.delete(`/admin/policies/${kind}/${p.id}?version=${p.version}`,{errorMode:'local'}))}><Button danger>删除</Button></Popconfirm></Space>}]}/>
  <Modal width={960} open={editing!==undefined} title={`${editing?'编辑':'新建'}${labels[kind]}`} confirmLoading={busy} onOk={save} onCancel={()=>setEditing(undefined)} destroyOnHidden>{error&&<Alert type="error" showIcon title={error}/>}
   <Form form={form} layout="vertical"><FormTabs sections={[{key:'scope',label:'名称与范围',description:'设置策略名称和生效对象；未绑定用户的策略可在资源授权时引用。',children:<><Form.Item name="name" label="名称" rules={[{required:true}]}><Input maxLength={100}/></Form.Item><Space><Form.Item name="enabled" label="启用" valuePropName="checked"><Switch/></Form.Item>{kind!=='ip-set'&&<Form.Item name="global" label="全部用户" valuePropName="checked"><Switch/></Form.Item>}</Space>
    {kind!=='ip-set'&&<><Form.Item name="userIds" label="用户"><Select mode="multiple" showSearch optionFilterProp="label" options={options(users.data)}/></Form.Item><Form.Item name="departmentIds" label="部门"><Select mode="multiple" options={options(departments.data)}/></Form.Item><Form.Item name="resourceIds" label="资源 / 分组（留空为全部资源）"><Select mode="multiple" showSearch optionFilterProp="label" options={[...options(groups.data),...options(resources.data?.items),...options(websites.data)]}/></Form.Item></>}
</>},{key:'rules',label:'规则配置',description:'按业务需要添加限制。不同规则共同生效，拒绝优先。',children:<>    {kind==='ip-set'&&<Form.Item name="entriesText" label="IP 地址，每行一项；支持 IPv4、IPv6、CIDR 和起止地址" rules={[{required:true}]}><Input.TextArea rows={10} placeholder={'192.0.2.0/24\n2001:db8::/64\n10.0.0.10-10.0.0.20'}/></Form.Item>}
    {(kind==='access-policy'||kind==='command-filter')&&<Form.Item name="mode" label="匹配模式"><Select options={modes}/></Form.Item>}
    {kind==='strategy'&&Object.entries(actions).map(([key,label])=><Form.Item key={key} name={['actions',key]} label={label}><Select allowClear placeholder="不增加限制" options={[{value:true,label:'允许（仍需资源授权）'},{value:false,label:'拒绝'}]}/></Form.Item>)}
    {kind==='command-filter'&&<Form.List name="patterns">{(fields,{add,remove})=><>{fields.map(f=><Space key={f.key} align="baseline"><Form.Item name={[f.name,'pattern']} rules={[{required:true}]}><Input style={{width:550}} placeholder="例如 ^(whoami|hostname)$"/></Form.Item><Form.Item name={[f.name,'action']}><Select style={{width:100}} options={decisions}/></Form.Item><Button icon={<MinusCircleOutlined/>} onClick={()=>remove(f.name)}/></Space>)}<Button onClick={()=>add({action:'reject',pattern:''})} icon={<PlusOutlined/>}>添加命令规则</Button></>}</Form.List>}
    {kind==='access-policy'&&<><Form.Item name="timezone" label="时区"><Input placeholder="Asia/Shanghai"/></Form.Item><Form.List name="rules">{(fields,{add,remove})=><>{fields.map(f=><Card key={f.key} size="small" title={`规则 ${f.name+1}`} extra={<Button danger onClick={()=>remove(f.name)}>删除规则</Button>} style={{marginBottom:12}}>
     <Form.Item name={[f.name,'name']} label="规则名称"><Input/></Form.Item><Form.Item name={[f.name,'enabled']} valuePropName="checked"><Checkbox>启用</Checkbox></Form.Item><Form.Item name={[f.name,'action']} label="匹配结果"><Select options={decisions}/></Form.Item>
     <Form.Item name={[f.name,'ips']} label="IP / 网段 / 地址范围"><Select mode="tags" tokenSeparators={[',',' ']} options={[]}/></Form.Item><Form.Item name={[f.name,'ipSetIds']} label="引用 IP 集合"><Select mode="multiple" options={options(ipsets.data)}/></Form.Item>
     {(['countries','provinces','cities'] as const).map((k,i)=><Form.Item key={k} name={[f.name,k]} label={['国家 / 地区代码','省份','城市'][i]}><Select mode="tags" options={[]}/></Form.Item>)}
     <Form.Item name={[f.name,'expiresAt']} label="规则截止时间（ISO 8601，可留空）" normalize={(v:string)=>v||undefined}><Input placeholder="2026-12-31T23:59:59+08:00"/></Form.Item>
     <Form.List name={[f.name,'periods']}>{(periods,{add:addPeriod,remove:removePeriod})=><>{periods.map(p=><Space key={p.key} align="baseline" wrap><Form.Item name={[p.name,'days']}><Select mode="multiple" style={{width:300}} placeholder="星期" options={['日','一','二','三','四','五','六'].map((n,i)=>({value:i,label:'周'+n}))}/></Form.Item><Form.Item name={[p.name,'start']}><Input placeholder="09:00" style={{width:90}}/></Form.Item><Form.Item name={[p.name,'end']}><Input placeholder="18:00" style={{width:90}}/></Form.Item><Button onClick={()=>removePeriod(p.name)}>移除时段</Button></Space>)}<Button onClick={()=>addPeriod({days:[1,2,3,4,5],start:'09:00',end:'18:00'})}>添加允许时段（不填为全天）</Button></>}</Form.List>
    </Card>)}<Button onClick={()=>add({enabled:true,action:'allow',name:'新规则',ips:[],periods:[]})}>添加访问规则</Button></>}</Form.List></>}
</>}]}/>   </Form>
  </Modal>
  <Modal open={geo} title="地域数据导入" onCancel={()=>setGeo(false)} footer={null}><Alert type="info" title="导入新的 IP 前缀库后原子切换版本；旧版本保留。格式为 JSON 数组，每项包含 prefix、country、province、city。"/><Table size="small" rowKey="id" dataSource={geodata.data} columns={[{title:'名称',dataIndex:'name'},{title:'条数',dataIndex:'entries'},{title:'当前',dataIndex:'active',render:(v:boolean)=>v?'是':'否'}]}/><Form form={geoForm} layout="vertical" onFinish={v=>run(async()=>{await requests.post('/admin/geodata',{name:v.name,entries:JSON.parse(v.entries)},{errorMode:'local'});geoForm.resetFields()})}><Form.Item name="name" label="版本名称" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="entries" label="地域记录" rules={[{required:true}]}><Input.TextArea rows={7} placeholder={'[{"prefix":"192.0.2.0/24","country":"TEST","province":"内网","city":"实验室"}]'}/></Form.Item><Button type="primary" htmlType="submit" loading={busy}>导入并启用</Button></Form></Modal>
 </section>
}
