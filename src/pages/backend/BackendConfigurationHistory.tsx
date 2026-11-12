import {useState} from 'react';
import {useQuery,keepPreviousData} from '@tanstack/react-query';
import {Alert,Button,Card,Descriptions,Empty,Segmented,Space,Switch,Table,Tag,Typography} from 'antd';
import {ReloadOutlined,LeftOutlined,RightOutlined} from '@ant-design/icons';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import {useConfiguration,useConfigurationSchema} from './useConfiguration';
import {configurationStates,displayConfiguration,type Configuration,type ConfigurationValue,type NodeState} from './configuration-model';
import Modal from './BackendFormModal';

type Revision={id:number;previousId:number;changedKeys:string[];actor:string;legacyWrite:boolean;createdAt:string};
type Page={items:Revision[];currentId:number;hasMore:boolean;nextBefore:number};
type Change={key:string;label:string;type:string;unit:string;before:ConfigurationValue;after:ConfigurationValue;beforePresent:boolean;afterPresent:boolean};
type Diff={fromId:number;toId:number;changes:Change[]};
export default function BackendConfigurationHistory(){
 const {account,capabilities}=useBackendRuntime(),current=useConfiguration(),schema=useConfigurationSchema();
 const canWrite=!!account.actions?.includes('identity.manage'),historyReady=capabilities.features['system.settings.history']?.state==='available';
 const [cursors,setCursors]=useState([0]),[showOffline,setShowOffline]=useState(false),[selected,setSelected]=useState<Revision>(),[baseline,setBaseline]=useState('previous'),[snapshotID,setSnapshotID]=useState<number>(),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const before=cursors[cursors.length-1];
 const history=useQuery({queryKey:['configuration-history',before],enabled:historyReady,queryFn:()=>requests.get<Page>(`/settings/revisions?limit=10&before=${before}`,{errorMode:'local'}),placeholderData:keepPreviousData,refetchInterval:before===0?10000:false});
 const from=baseline==='current'?current.data?.id:selected?.previousId;
 const diff=useQuery({queryKey:['configuration-diff',selected?.id,from],enabled:!!selected&&from!==undefined&&historyReady,queryFn:()=>requests.get<Diff>(`/settings/revisions/${selected!.id}/diff?from=${from}`,{errorMode:'local'})});
 const snapshot=useQuery({queryKey:['configuration-snapshot',snapshotID],enabled:!!snapshotID,queryFn:()=>requests.get<Configuration>(`/settings/revisions/${snapshotID}`,{errorMode:'local'})});
 const labels=Object.fromEntries((schema.data?.properties??[]).map(p=>[p.key,p.label]));
 const nodes=current.data?.nodes??[],offline=nodes.filter(n=>!n.online).length,pending=nodes.filter(n=>n.online&&n.state!=='effective').length;
 const refresh=()=>{setCursors([0]);void current.refetch();void history.refetch()};
 const retry=async()=>{if(!current.data)return;setError('');setBusy(true);try{const id=current.data.id;const r=await fetch(`/api/settings/revisions/${id}/retry`,{method:'POST',headers:{'If-Match':`"settings-${id}"`}});if(!r.ok){const body=await r.json();throw Error(body.message??'重试失败')}await current.refetch()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <div className="nt-settings-stack">
  {(error||current.error)&&<Alert type="error" title={error||current.error?.message} showIcon/>}
  <Card title="配置发布状态" extra={<Space wrap><Button size="small" icon={<ReloadOutlined/>} loading={current.isFetching} onClick={()=>void current.refetch()}>刷新状态</Button><Typography.Text type="secondary">显示离线节点</Typography.Text><Switch size="small" checked={showOffline} onChange={setShowOffline} aria-label="显示离线节点"/></Space>}>
   <div className="nt-settings-publish-note"><div><Tag color={current.data?.state==='effective'?'green':'orange'}>{configurationStates[current.data?.state??'']??'读取中'}</Tag><span>目标版本 {current.data?.id??'—'}</span></div>{canWrite&&nodes.some(n=>n.state==='failed')&&<Button loading={busy} onClick={()=>void retry()}>重试失败节点</Button>}</div>
   <p className="nt-settings-description">{nodes.length-offline} 个在线节点，{pending} 个尚未确认目标版本。最近成功版本仅表示节点确认记录，不代表现有会话已切换配置。新会话参数仍按各自生效范围执行。</p>
   {offline>0&&<Alert style={{marginBottom:16}} type="info" showIcon title={`另有 ${offline} 个离线节点（含已停止实例），未计入在线节点的发布结果。`} description="可打开“显示离线节点”核对版本与最后心跳；节点恢复后须重新读取当前配置。重试只重置失败记录，不会启动离线服务。"/>}
   <Table<NodeState> size="small" rowKey="id" scroll={{x:600}} dataSource={current.data?.nodes.filter(n=>showOffline||n.online)??[]} pagination={{pageSize:5,hideOnSinglePage:true,showSizeChanger:false}} locale={{emptyText:'暂无在线配置节点，请检查服务运行状态。'}} columns={[
    {title:'节点',dataIndex:'id',render:v=><Typography.Text title={v}>{v.slice(0,12)}</Typography.Text>},
    {title:'连接状态',dataIndex:'online',render:v=><Tag color={v?'green':'default'}>{v?'在线':'已停止 / 失联'}</Tag>},
    {title:'版本反馈',dataIndex:'state',render:v=>configurationStates[v]??v},
    {title:'最近成功版本',dataIndex:'lastAppliedRevision',render:(v:number|undefined)=>v===undefined?'服务升级后可见':v===0?'尚无成功记录':v},
    {title:'应用轮次',dataIndex:'attempts'},
    {title:'最后心跳',dataIndex:'lastSeen',render:v=>new Date(v).toLocaleString()},
    {title:'失败原因',dataIndex:'error',render:v=>v||'—'},
   ]}/>
  </Card>
  <Card title="当前只读信息">
   <p className="nt-settings-description">以下为当前运行状态和资源目录，不是所选历史版本的快照。资产、数据库和 Web 资产共用分组，在资源管理中修改。</p>
   <Descriptions size="small" column={{xs:1,md:2}} items={(schema.data?.readOnlyProperties??[]).map(p=>({key:p.key,label:p.label,children:displayConfiguration(current.data?.readOnly?.[p.key],p)}))}/>
  </Card>
  <Card title="配置变更记录" extra={<Button icon={<ReloadOutlined/>} loading={history.isFetching} onClick={refresh}>刷新记录</Button>}>
   <p className="nt-settings-description">查看修改人、变更字段和版本差异。历史版本只读，恢复旧值需要重新编辑并保存为新版本。</p>
   {!historyReady&&<Alert type="info" title="当前服务尚未提供历史列表，请完成后端升级。"/>}
   {history.error&&<Alert type="error" title={history.error.message}/>}
   <Table<Revision> size="small" rowKey="id" loading={historyReady&&history.isFetching} dataSource={history.data?.items??[]} pagination={false} scroll={{x:690}} columns={[
    {title:'版本',dataIndex:'id',width:95,render:id=><Space size={4}><strong>#{id}</strong>{id===current.data?.id&&<Tag color="blue">当前</Tag>}</Space>},
    {title:'修改时间',dataIndex:'createdAt',width:175,render:v=>new Date(v).toLocaleString()},
    {title:'修改人',dataIndex:'actor',width:120,render:v=>v||'系统迁移'},
    {title:'变更内容',render:(_,v)=><><span>{v.changedKeys.map(k=>labels[k]??k).join('、')||'初始配置'}</span>{v.legacyWrite&&<Tag>旧接口写入</Tag>}</>},
    {title:'查看',width:128,render:(_,v)=><Space size={0}><Button type="link" size="small" onClick={()=>{setBaseline('previous');setSelected(v)}}>差异</Button><Button type="link" size="small" onClick={()=>setSnapshotID(v.id)}>快照</Button></Space>},
   ]}/>
   <div className="nt-settings-pagination"><Typography.Text type="secondary">第 {cursors.length} 页 · 按版本由新到旧</Typography.Text><Space><Button icon={<LeftOutlined/>} disabled={cursors.length===1||history.isFetching} onClick={()=>setCursors(v=>v.slice(0,-1))}>上一页</Button><Button icon={<RightOutlined/>} disabled={!history.data?.hasMore||history.isFetching} onClick={()=>setCursors(v=>[...v,history.data!.nextBefore])}>下一页</Button></Space></div>
  </Card>
  <Modal open={!!selected} title={`配置差异 · 版本 ${selected?.id??''}`} width={850} onCancel={()=>setSelected(undefined)} footer={<Button onClick={()=>setSelected(undefined)}>关闭</Button>}>
   <div className="nt-settings-diff-toolbar"><span>对比基准</span><Segmented value={baseline} onChange={v=>setBaseline(String(v))} options={[{label:selected?.previousId?'上一个版本':'初始空配置',value:'previous'},{label:`当前版本 ${current.data?.id??''}`,value:'current'}]}/></div>
   {diff.error&&<Alert type="error" title={diff.error.message}/>}
   <Table<Change> size="small" rowKey="key" loading={diff.isFetching} dataSource={diff.data?.changes??[]} pagination={false} scroll={{x:550}} locale={{emptyText:<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="这两个版本的配置相同"/>}} columns={[
    {title:'配置项',dataIndex:'label'},
    {title:`基准版本 ${diff.data?.fromId??'—'}`,render:(_,v)=><Typography.Text type="secondary">{displayConfiguration(v.beforePresent?v.before:undefined,v)}</Typography.Text>},
    {title:`所选版本 ${selected?.id??'—'}`,render:(_,v)=><strong>{displayConfiguration(v.afterPresent?v.after:undefined,v)}</strong>},
   ]}/>
  </Modal>
  <Modal open={!!snapshotID} title={`只读配置快照 · 版本 ${snapshotID??''}`} onCancel={()=>setSnapshotID(undefined)} footer={<Button onClick={()=>setSnapshotID(undefined)}>关闭</Button>}>
   {snapshot.error&&<Alert type="error" title={snapshot.error.message}/>}
   <Descriptions size="small" column={1} items={[{key:'actor',label:'修改人',children:snapshot.data?.actor||'系统迁移'},{key:'time',label:'保存时间',children:snapshot.data?new Date(snapshot.data.createdAt).toLocaleString():'—'}]}/>
   <Table size="small" rowKey="key" loading={snapshot.isFetching} pagination={false} dataSource={schema.data?.properties??[]} columns={[{title:'配置项',dataIndex:'label'},{title:'保存值',render:(_,p)=>displayConfiguration(snapshot.data?.values[p.key],p)}]}/>
  </Modal>
 </div>;
}
