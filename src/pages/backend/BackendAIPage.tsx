import {useEffect, useRef, useState} from 'react';
import {Alert, Button, Card, Collapse, Empty, Form, Input, InputNumber, Popconfirm, Select, Space, Switch, Table, Tag, Typography} from 'antd';
import {useQuery, useQueryClient} from '@tanstack/react-query';
import {Link} from 'react-router-dom';
import requests from '@/api/core/requests';
import {isApiError} from '@/api/core/api-error';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import Modal, {FormTabs} from './BackendFormModal';
import './backend-ai.css';
import {aiExecutionState} from './ai-entitlement';
import {AITools,AIPolicy,AIUsage,AIPreferences,type AIToolCall} from './BackendAIWorkflows';
import {AIAssetSettings, AIContextChooser, type AIScope} from './BackendAIContext';

type Profile={managed?:boolean;id:string;name:string;baseUrl:string;model:string;hasKey:boolean;allowPrivate:boolean;enabled:boolean;maxOutputTokens:number;version:number;apiKey?:string;clearKey?:boolean;protocol?:string;proxyUrl?:string;temperature?:number;inputMicrosPerThousand?:number;outputMicrosPerThousand?:number};
type Settings={enabled:boolean;readonlyEnabled:boolean;version:number};
type Conversation={id:string;profileId:string;title:string;updatedAt:string;contextType:string;contextId:string;parentId?:string;summary?:string};
type Turn={toolCalls?:AIToolCall[];id:string;revision:number;cancelRequested:boolean;prompt:string;output:string;state:string;reason:string;model:string;promptTokens:number;completionTokens:number;createdAt:string};
const opts={errorMode:'local' as const};
const states:Record<string,string>={queued:'排队中',running:'正在生成',succeeded:'已完成',failed:'失败',interrupted:'已中断'};
const reasons:Record<string,string>={user_cancelled:'已停止生成，以上内容可能不完整。',model_request_failed:'模型响应失败或未完整结束，已有内容仅供参考；请检查服务是否支持流式响应。',authorization_changed:'权限、许可或模型配置已变更，本次响应未保存。',request_interrupted:'请求已中断；未自动重试，请按需重新提问。'};

function Profiles(){
 const {capabilities}=useBackendRuntime();const execution=aiExecutionState(capabilities.features);
 const cache=useQueryClient();const [form]=Form.useForm<Profile>();const [editing,setEditing]=useState<Profile|null>();const [models,setModels]=useState<string[]>([]);const [error,setError]=useState('');const [busy,setBusy]=useState(false);
 const profiles=useQuery({queryKey:['ai-admin-profiles'],queryFn:()=>requests.get<Profile[]>('/admin/ai/profiles',opts)});
 const settings=useQuery({queryKey:['ai-admin-settings'],queryFn:()=>requests.get<Settings>('/admin/ai/settings',opts)});
 const refresh=async()=>{await Promise.all([cache.invalidateQueries({queryKey:['ai-admin-profiles']}),cache.invalidateQueries({queryKey:['ai-profiles']}),cache.invalidateQueries({queryKey:['ai-admin-settings']}),cache.invalidateQueries({queryKey:['ai-status']})])};
 const run=async(fn:()=>Promise<unknown>)=>{setBusy(true);setError('');try{await fn();await refresh()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const edit=(p:Profile|null)=>{setEditing(p);setModels([]);form.resetFields();form.setFieldsValue(p?{...p,apiKey:'',clearKey:false}:{name:'',baseUrl:'',model:'',protocol:'openai',maxOutputTokens:1024,enabled:false,allowPrivate:false})};
 return <Space orientation="vertical" style={{width:'100%'}} size="middle">
  <Alert type="info" showIcon title="模型配置" description="支持 OpenAI Chat Completions、Responses 和 Anthropic Messages。模型密钥加密保存；留空保留原密钥。启用后，平台用户可使用已启用的模型发送自己输入的内容。"/>
  {(error||profiles.error||settings.error)&&<Alert type="error" title={error||profiles.error?.message||settings.error?.message}/>}
  <Space wrap><Switch checked={settings.data?.enabled??false} disabled={!settings.data||busy} onChange={enabled=>void run(()=>requests.put('/admin/ai/settings',{...settings.data,enabled},opts))}/><span>启用 AI 对话</span><Button type="primary" onClick={()=>edit(null)}>新建模型配置</Button></Space>
  <Space><Switch aria-label="启用只读上下文" checked={settings.data?.readonlyEnabled??false} disabled={!settings.data||busy} onChange={readonlyEnabled=>void run(()=>requests.put('/admin/ai/settings',{...settings.data,readonlyEnabled},opts))}/><span>启用资产与会话只读上下文</span></Space>
  <Collapse items={[{key:"assets",label:"逐个资产开放 AI",children:<AIAssetSettings/>},{key:"policy",label:"执行策略、提示词与预算",children:<AIPolicy/>}]}/>
  <Table rowKey="id" dataSource={profiles.data||[]} loading={profiles.isLoading} scroll={{x:650}} pagination={false} columns={[
   {title:'名称',dataIndex:'name'},{title:'模型',dataIndex:'model'},
   {title:'状态',render:(_,p:Profile)=><Tag color={p.enabled?'green':'default'}>{p.enabled?'启用':'停用'}</Tag>},
   {title:'密钥',render:(_,p:Profile)=>p.hasKey?'已配置':'未配置'},
   {title:'操作',render:(_,p:Profile)=>p.managed?<Link to="/system-status?section=ai">平台设置管理</Link>:<Space><Button type="link" onClick={()=>edit(p)}>编辑</Button><Popconfirm title="删除此模型配置？已有对话历史会保留。" onConfirm={()=>run(()=>requests.delete('/admin/ai/profiles/'+p.id,opts))}><Button type="link" danger disabled={busy}>删除</Button></Popconfirm></Space>}
  ]}/>
  <Modal title={editing?'编辑模型配置':'新建模型配置'} open={editing!==undefined} onCancel={()=>setEditing(undefined)} confirmLoading={busy} onOk={async()=>{const values=await form.validateFields();await run(async()=>{const payload={...values,version:editing?.version||0};if(editing)await requests.put('/admin/ai/profiles/'+editing.id,payload,opts);else await requests.post('/admin/ai/profiles',payload,opts);setEditing(undefined)})}}>
   {error&&<Alert type="error" title={error}/>}
   <Form form={form} layout="vertical"><FormTabs sections={[
    {key:'connection',label:'连接配置',description:'填写模型服务的 API 根地址，例如 https://model.example/v1。',children:<>
     <Form.Item name="name" label="配置名称" rules={[{required:true}]}><Input maxLength={100}/></Form.Item>
     <Form.Item name="protocol" label="接口协议"><Select options={[{value:"openai",label:"OpenAI Chat Completions"},{value:"responses",label:"OpenAI Responses"},{value:"anthropic",label:"Anthropic Messages"}]}/></Form.Item>
     <Form.Item name="model" label="模型标识" rules={[{required:true}]}><Input maxLength={200}/></Form.Item>
     {editing&&<Button disabled={!execution.allowed} onClick={()=>void run(async()=>{const models=await requests.get<string[]>('/admin/ai/profiles/'+editing.id+'/models',opts);if(!models.length)throw new Error('模型服务未返回可用标识');setModels(models)})}>发现已保存配置的模型</Button>}
     {models.length>0&&<Select aria-label="发现的模型" style={{width:'100%'}} options={models.map(value=>({value,label:value}))} onChange={model=>form.setFieldValue('model',model)}/>}
     <Form.Item name="baseUrl" label="API 根地址" rules={[{required:true}]}><Input placeholder="https://model.example/v1" maxLength={2048}/></Form.Item>
     <Form.Item name="apiKey" label={editing?.hasKey?'API Key（留空保留）':'API Key'}><Input.Password autoComplete="new-password" maxLength={4096}/></Form.Item>
     <Form.Item name="enabled" label="启用此配置" valuePropName="checked"><Switch/></Form.Item>
     {editing&&<Form.Item name="clearKey" label="清除已保存密钥" valuePropName="checked"><Switch/></Form.Item>}
    </>},
    {key:'limits',label:'连接与用量限制',description:'仅可信内网模型需要开启内网访问。单次最长等待 55 秒，不自动重试。',children:<>
     <Form.Item name="allowPrivate" label="允许内网地址及 HTTP" valuePropName="checked"><Switch/></Form.Item>
     <Form.Item name="proxyUrl" label="模型网络代理（可选）" extra="HTTP(S) CONNECT 或 SOCKS5/SOCKS5h；SOCKS 需端口。平台固定目标 IP，不委托代理解析；URL 不含凭据。"><Input placeholder="https://proxy.example:8443" maxLength={2048}/></Form.Item>
     <Form.Item name="temperature" label="Temperature（留空用模型默认）"><InputNumber min={0} max={2} step={0.1}/></Form.Item>
     <Form.Item name="inputMicrosPerThousand" label="输入价格（微美元 / 千 Token）"><InputNumber min={0} max={1000000000}/></Form.Item>
     <Form.Item name="outputMicrosPerThousand" label="输出价格（微美元 / 千 Token）"><InputNumber min={0} max={1000000000}/></Form.Item>
     <Form.Item name="maxOutputTokens" label="最大输出 Token" rules={[{required:true}]}><InputNumber min={64} max={4096}/></Form.Item>
     <div>每人同时 1 个请求，每小时最多 60 次；每段上下文最多 30 轮，可通过整理摘要继续；单个对话最多保留 1000 轮。</div>
    </>}
   ]}/></Form>
  </Modal>
 </Space>;
}

export default function BackendAIPage({embedded=false}:{embedded?:boolean}){
 const [showHistory,setShowHistory]=useState(false);const [connection,setConnection]=useState('未连接');const [panel,setPanel]=useState(''),[summary,setSummary]=useState('');
 const {account,capabilities}=useBackendRuntime();const execution=aiExecutionState(capabilities.features);const cache=useQueryClient();
 const selectionKey='nt-ai-conversation:'+account.id;
 const [selected,setSelected]=useState(()=>{try{return sessionStorage.getItem(selectionKey)||''}catch{return ''}}),[profile,setProfile]=useState<string>(),[text,setText]=useState(''),[error,setError]=useState(''),[busy,setBusy]=useState(false),[config,setConfig]=useState(false);
 const [live,setLive]=useState<{conversation:string;turn:Turn}>();const [stopping,setStopping]=useState(false);const stream=useRef<AbortController|null>(null);
 const [scope,setScope]=useState<AIScope>({contextType:'',contextId:''}),[contextDigest,setContextDigest]=useState('');
 const messages=useRef<HTMLDivElement>(null),followLatest=useRef(true);
 useEffect(()=>{try{if(selected)sessionStorage.setItem(selectionKey,selected);else sessionStorage.removeItem(selectionKey)}catch{/* Storage can be unavailable in private browsing. */}},[selected,selectionKey]);
 useEffect(()=>()=>stream.current?.abort(),[]);
 const sending=useRef(false);const draft=useRef<{text:string;id:string;conversation:string}|null>(null);
 const preferences=useQuery({queryKey:['ai-preferences'],queryFn:()=>requests.get<{defaultProfileId:string;displayLevel:string;includeCommandSnippets:boolean}>('/ai/preferences',opts)});
 const status=useQuery({queryKey:['ai-status'],queryFn:()=>requests.get<Settings>('/ai/status',opts)});
 const profiles=useQuery({queryKey:['ai-profiles'],queryFn:()=>requests.get<Profile[]>('/ai/profiles',opts)});
 const conversations=useQuery({queryKey:['ai-conversations'],queryFn:()=>requests.get<Conversation[]>('/ai/conversations',opts)});
 const history=useQuery({queryKey:['ai-conversation',selected],enabled:!!selected,queryFn:()=>requests.get<{conversation:Conversation;turns:Turn[]}>('/ai/conversations/'+selected,opts),refetchInterval:query=>query.state.data?.turns.some(t=>(t.state==='running'||t.state==='queued'))?2000:false});
 const chosenScope=selected?(history.data?.conversation||conversations.data?.find(c=>c.id===selected)||scope):scope;
 const contextReady=!chosenScope.contextType||!!contextDigest;
 const turns=[...(history.data?.turns||[])];
 if(live?.conversation===selected){const index=turns.findIndex(t=>t.id===live.turn.id);if(index<0)turns.push(live.turn);else if(turns[index].revision<live.turn.revision)turns[index]=live.turn}
 const activeTurn=turns.find(t=>(t.state==='running'||t.state==='queued'));const active=!!activeTurn;
 useEffect(()=>{followLatest.current=true},[selected]);
 useEffect(()=>{if(followLatest.current&&messages.current)messages.current.scrollTop=messages.current.scrollHeight},[selected,turns[turns.length-1]?.id,turns[turns.length-1]?.revision]);
 useEffect(()=>{
  if(!selected)return;let closed=false,socket:WebSocket|undefined,timer:ReturnType<typeof setTimeout>;let delay=1000;
  const connect=()=>{setConnection('连接中，轮询恢复可用');socket=new WebSocket((location.protocol==='https:'?'wss://':'ws://')+location.host+'/api/ai/ws?conversationId='+selected);socket.onopen=()=>setConnection('实时连接正常');socket.onmessage=event=>{try{const value=JSON.parse(event.data);if(value.type==='snapshot'&&Array.isArray(value.turns)){delay=1000;cache.setQueryData<{conversation:Conversation;turns:Turn[]}>(['ai-conversation',selected],old=>old?{...old,turns:value.turns}:old)}}catch{/* Polling fallback remains available. */}};socket.onclose=()=>{if(!closed){setConnection('连接已断开，自动重连中');timer=setTimeout(connect,delay);delay=Math.min(delay*2,10000)}}};connect();return()=>{closed=true;clearTimeout(timer);socket?.close()};
 },[selected,cache]);
 const historyAction=async(action:string,through:string)=>{setError('');try{const result=await requests.post<{id:string}>('/ai/conversations/'+selected+'/history/'+action,{through,summary},opts);await cache.invalidateQueries({queryKey:['ai-conversations']});await history.refetch();if(action==='branch')setSelected(result.id);setPanel('')}catch(e){setError((e as Error).message)}};
 const stop=async()=>{
  if(!activeTurn)return;const turnId=activeTurn.id;setStopping(true);setError('');
  try{await requests.post('/ai/conversations/'+selected+'/turns/'+turnId+'/cancel',{},opts);await history.refetch()}
  catch(e){
   // Completion can race the stop click. Show the confirmed final state instead of a generic conflict.
   if(isApiError(e)&&e.status===409){
    const refreshed=await history.refetch();const turn=refreshed.data?.turns.find(t=>t.id===turnId);
    if(!refreshed.isError&&turn&&['succeeded','failed','interrupted'].includes(turn.state))return;
   }
   setError((e as Error).message);
  }finally{setStopping(false)}
 };
 const send=async()=>{
  if(!execution.allowed||sending.current||!text.trim()||!contextReady)return;sending.current=true;followLatest.current=true;setBusy(true);setError('');
  try{
   let id=selected;
   if(!id){const p=profile||preferences.data?.defaultProfileId||profiles.data?.[0]?.id;if(!p)throw new Error('请先配置并启用一个模型');const c=await requests.post<{id:string}>('/ai/conversations',{profileId:p,title:[...text.trim()].slice(0,60).join(''),...scope},opts);id=c.id;setSelected(id);await cache.invalidateQueries({queryKey:['ai-conversations']})}
   if(!draft.current||draft.current.text!==text||draft.current.conversation!==id)draft.current={text,id:crypto.randomUUID(),conversation:id};
   const turn=await requests.post<Turn>('/ai/conversations/'+id+'/messages/queued',{text,clientMessageId:draft.current.id,contextDigest},opts);
   setLive({conversation:id,turn});draft.current=null;setText('');setContextDigest('');
   await cache.invalidateQueries({queryKey:['ai-conversation',id]});await cache.invalidateQueries({queryKey:['ai-conversations']});
  }catch(e){if((e as Error).name!=='AbortError')setError((e as Error).message);await cache.invalidateQueries({queryKey:['ai-conversation']})}finally{stream.current=null;sending.current=false;setBusy(false)}
 };
 const remove=async(id:string)=>{setError('');try{await requests.delete('/ai/conversations/'+id,opts);if(selected===id){setSelected('');draft.current=null};await cache.invalidateQueries({queryKey:['ai-conversations']})}catch(e){setError((e as Error).message)}};
 return <section className={(embedded?`nt-ai-embedded ${showHistory?'show-history':''}`:'nt-page')+' nt-ai-density-'+(preferences.data?.displayLevel||'standard')}>
  <div className="nt-ai-heading">{embedded?<Space><Button size="small" onClick={()=>setShowHistory(v=>!v)}>{showHistory?'收起历史':'历史对话'}</Button><Button size="small" disabled={busy} onClick={()=>{setSelected('');setText('');draft.current=null;setError('')}}>新对话</Button></Space>:<div><Typography.Title level={3} style={{margin:0}}>AI 助手</Typography.Title><Typography.Text type="secondary">运维咨询与个人对话历史</Typography.Text></div>}{account.actions?.includes('identity.manage')&&<Button size={embedded?'small':'middle'} onClick={()=>setConfig(true)}>模型与开关</Button>}</div>
  <Space wrap><Button size="small" onClick={()=>setPanel('usage')}>用量与费用</Button><Button size="small" onClick={()=>setPanel('preferences')}>个人偏好</Button>{selected&&turns.length>0&&<Button size="small" disabled={busy||active} onClick={()=>{setSummary(history.data?.conversation.summary||'');setPanel('compact')}}>整理上下文摘要</Button>}</Space>
  <Alert type="info" showIcon title="支持问答、上下文分析和逐项审批的工具操作。请勿输入密码及密钥；工具结果不会自动发送给模型。"/>
  {!execution.allowed&&<Alert type="warning" showIcon title={execution.message}/>}
  {!status.isLoading&&!status.data?.enabled&&<Alert type="warning" title="AI 对话尚未启用，历史记录仍可查看。"/>}
  {(error||status.error||profiles.error||conversations.error||history.error)&&<Alert type="error" closable onClose={()=>setError('')} title={error||status.error?.message||profiles.error?.message||conversations.error?.message||history.error?.message}/>}
  <div className="nt-ai-layout">
   <Card className="nt-ai-history-card" size="small" title="我的对话" extra={<Button size="small" disabled={busy} onClick={()=>{setSelected('');setText('');draft.current=null;setError('')}}>新对话</Button>}>
    <div className="nt-ai-history">{conversations.data?.length?conversations.data.map(c=><div key={c.id} className={'nt-ai-history-row '+(selected===c.id?'selected':'')}><Button type="text" disabled={busy} title={c.title} onClick={()=>{setSelected(c.id);setText('');draft.current=null;setError('')}}>{c.title}</Button><Popconfirm title="删除该对话和消息？" onConfirm={()=>remove(c.id)}><Button type="text" danger disabled={busy||(selected===c.id&&active)} aria-label={'删除对话 '+c.title}>×</Button></Popconfirm></div>):<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无对话"/>}</div>
   </Card>
   <Card className="nt-ai-chat" title={history.data?.conversation.title||'开始新对话'} extra={!selected&&<Select aria-label="选择模型" style={{width:220}} placeholder="选择模型" value={profile||preferences.data?.defaultProfileId||profiles.data?.[0]?.id} onChange={setProfile} options={profiles.data?.map(p=>({value:p.id,label:p.name+' · '+p.model}))}/>}>
    <div ref={messages} className="nt-ai-messages" aria-live="polite" onScroll={e=>{const el=e.currentTarget;followLatest.current=el.scrollHeight-el.scrollTop-el.clientHeight<80}}>{turns.map(t=><article key={t.id} className="nt-ai-turn"><Typography.Text strong>我</Typography.Text><p>{t.prompt}</p><Typography.Text strong>AI · {t.model}</Typography.Text><Tag style={{marginLeft:8}}>{states[t.state]||t.state}</Tag>{t.output?<p>{t.output}</p>:<p className="nt-ai-muted">{reasons[t.reason]||((t.state==='running'||t.state==='queued')?'模型正在生成回复…':'')}</p>}{t.reason&&t.output&&<p className="nt-ai-muted">{reasons[t.reason]||'响应未完整结束'}</p>}{t.state==='succeeded'&&preferences.data?.displayLevel!=='compact'&&<Typography.Text type="secondary">输入 {t.promptTokens} / 输出 {t.completionTokens} Token（服务方返回值）</Typography.Text>}{!active&&!busy&&<Button size="small" type="link" onClick={()=>void historyAction('branch',t.id)}>从此处分支</Button>}{preferences.data?.displayLevel==='detailed'&&<Typography.Paragraph type="secondary">{t.id} · {new Date(t.createdAt).toLocaleString()}</Typography.Paragraph>}</article>)}{!turns.length&&!busy&&<Empty description="输入问题开始对话" image={Empty.PRESENTED_IMAGE_SIMPLE}/>}</div>
    {selected&&chosenScope.contextType&&<AITools conversation={selected} turns={turns} showSnippets={preferences.data?.includeCommandSnippets??false} onCopy={value=>{if(new TextEncoder().encode(value).length>8000){setError('工具结果超过问题长度上限，请在详情中手动选取需要的片段。');return}setText(value)}}/>}
    <AIContextChooser scope={chosenScope} onChange={setScope} locked={!!selected} disabled={busy||active} digest={contextDigest} onConfirm={setContextDigest}/>
    <Input.TextArea aria-label="输入问题" value={text} onChange={e=>setText(e.target.value)} disabled={busy||active} autoSize={{minRows:3,maxRows:6}} maxLength={8000} placeholder="描述你的问题。Ctrl + Enter 发送" onKeyDown={e=>{if(e.key==='Enter'&&e.ctrlKey&&!active&&execution.allowed&&status.data?.enabled){e.preventDefault();void send()}}}/>
    <div className="nt-ai-send"><Typography.Text type="secondary">后台生成，刷新可恢复进度{selected?' · '+connection:''}</Typography.Text>{active?<Button danger loading={stopping} disabled={activeTurn?.cancelRequested} onClick={()=>void stop()}>{activeTurn?.cancelRequested?'正在停止…':'停止生成'}</Button>:<Button type="primary" loading={busy} disabled={!execution.allowed||!status.data?.enabled||!text.trim()||!contextReady} onClick={()=>void send()}>发送</Button>}</div>
   </Card>
  </div>
  <Modal open={panel==='usage'} title="用量与费用" width={1000} footer={null} onCancel={()=>setPanel('')}><AIUsage admin={account.actions?.includes('identity.manage')||false}/></Modal>
  <Modal open={panel==='preferences'} title="个人 AI 偏好" footer={null} onCancel={()=>setPanel('')}><AIPreferences profiles={profiles.data||[]} onSaved={()=>setPanel('')}/></Modal>
  <Modal open={panel==='compact'} title="整理上下文摘要" onCancel={()=>setPanel('')} onOk={()=>historyAction('compact',turns[turns.length-1].id)}><Alert type="info" title="摘要替代后续模型请求中的旧消息，原始历史仍保留" description="填写并核对摘要后保存。也可先在对话中请模型整理摘要，再粘贴至此审阅。"/><Input.TextArea aria-label="上下文摘要" value={summary} onChange={e=>setSummary(e.target.value)} rows={8} maxLength={8000}/></Modal>
  <Modal open={config} title="AI 模型与开关" width={1000} footer={null} onCancel={()=>setConfig(false)} destroyOnHidden><Profiles/></Modal>
 </section>;
}
