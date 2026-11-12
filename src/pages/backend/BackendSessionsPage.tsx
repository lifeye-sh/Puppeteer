import BackendForwardAudit from './BackendForwardAudit';
import Modal from './BackendFormModal';
import {useState} from 'react';
import BackendRecordingExport from './BackendRecordingExport';
import {Link, useSearchParams} from 'react-router-dom';
import {useQuery} from '@tanstack/react-query';
import {Alert, Button, Card, Checkbox, Popconfirm, Space, Table, Tag, Typography} from 'antd';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';
import BackendSessionShares from './BackendSessionShares';
import BackendRecordingPlayer from './BackendRecordingPlayer';

type Row = {session:{forwarding?:boolean;protocol:string;id:string;actorId:string;assetId:string;readonly:boolean;state:string;reason:string;createdAt:string;connectedAt:string|null;closedAt:string|null};
    access:{username:string;assetName:string;target:string;recordingState:string;recordingBytes:number;recordingEvents:number;recordingFormat?:string}};
type Command = {id:string;command:string;source:string;note:string;state:string;exitCode:number|null;createdAt:string};
const commandStates:Record<string,string>={observed:'已识别提交',unrecognized:'未识别输入',running:'执行中',succeeded:'成功',failed:'失败',timeout:'超时',interrupted:'已中断'};
const labels:Record<string,string> = {reserved:'待连接',connecting:'连接中',connected:'已连接',closed:'已关闭',failed:'失败',interrupted:'已中断'};
function operationDuration(s:Row['session']){if(!s.connectedAt)return '未连接';const seconds=Math.max(0,Math.floor(((s.closedAt?new Date(s.closedAt).getTime():Date.now())-new Date(s.connectedAt).getTime())/1000));return `${Math.floor(seconds/3600)}小时 ${Math.floor(seconds%3600/60)}分 ${seconds%60}秒`}
function formatBytes(bytes:number){if(bytes<1024)return `${bytes} B`;if(bytes<1024*1024)return `${(bytes/1024).toFixed(1)} KiB`;return `${(bytes/1024/1024).toFixed(2)} MiB`}
export default function BackendSessionsPage({online=false}:{online?:boolean}) {
    const {account,capabilities} = useBackendRuntime();
    const observeAvailable=capabilities.features['session.observe']?.state==='available';
    const [sharing,setSharing]=useState('');
    const canAll = account.actions?.includes('session.read');
    const [all,setAll] = useState(!!canAll), [page,setPage] = useState(1), [error,setError] = useState('');
    const [params] = useSearchParams();
    const [selected,setSelected] = useState(params.get('sessionId') ?? '');
    const [commandPage,setCommandPage] = useState(1);
    const [replay,setReplay] = useState('');
    const mayReplay=(r:Row)=>!['disabled','expired'].includes(r.access.recordingState)&&!r.session.forwarding&&(r.session.actorId===account.id||account.actions?.includes('audit.read'));
    const list = useQuery({queryKey:['r04-sessions',online,all,page], queryFn:()=>requests.get<{items:Row[];total:number}>(`/sessions?online=${online}&all=${!!canAll&&all}&current=${page}`,{errorMode:'local'}),refetchInterval:2000,retry:false});
    const detail = useQuery({queryKey:['r04-session',selected], enabled:!!selected, queryFn:()=>requests.get<Row>(`/sessions/${selected}`,{errorMode:'local'}),refetchInterval:2000,retry:false});
    const commands = useQuery({queryKey:['r04-commands',selected,commandPage], enabled:!!selected&&!detail.data?.session.forwarding&&!['rdp','vnc'].includes(detail.data?.session.protocol??'')&&(detail.data?.session.actorId===account.id||account.actions?.includes('audit.read')),queryFn:()=>requests.get<{items:Command[];total:number}>(`/sessions/${selected}/commands?current=${commandPage}&pageSize=20`,{errorMode:'local'}),refetchInterval:2000,retry:false});
    const terminate = async (id:string) => {setError('');try{await requests.post(`/sessions/${id}/terminate`,undefined,{errorMode:'local'});await list.refetch()}catch(e){setError((e as Error).message)}};
    return <section className="nt-page">
        <Typography.Title level={2}>{online?'在线会话':'访问记录与命令记录'}</Typography.Title>
        <Space><Link to={online?'/access-log':'/online-session'}>{online?'访问历史':'在线会话'}</Link>{canAll&&<Checkbox checked={all} onChange={e=>{setAll(e.target.checked);setPage(1)}}>查看全部用户</Checkbox>}<Button onClick={()=>void list.refetch()}>刷新</Button></Space>
        {(error||list.isError)&&<Alert type="error" title={error||list.error?.message}/>}
        <Alert type="info" title="MP4 由服务器后台生成，关闭页面也会继续。完成后可在此下载；转换缓存保留 7 天，失效后可重新生成。" style={{marginBlock:12}}/><Card><Table<Row> rowKey={r=>r.session.id} dataSource={list.data?.items??[]} loading={list.isPending} scroll={{x:950}} pagination={{current:page,pageSize:20,total:list.data?.total??0,onChange:setPage,showSizeChanger:false}} columns={[
            {title:'用户',render:(_,r)=>r.access.username},{title:'资产 / 目标',render:(_,r)=><>{r.access.assetName}<div>{r.access.target}</div></>},
            {title:'状态',render:(_,r)=><Tag color={r.session.state==='connected'?'green':r.session.state==='failed'?'red':'default'}>{labels[r.session.state]??r.session.state}</Tag>},
            {title:'模式',render:(_,r)=>r.session.forwarding?'端口转发':r.session.readonly?'只读':'交互'}, {title:'开始时间',render:(_,r)=>new Date(r.session.createdAt).toLocaleString()},
            {title:'操作时长',render:(_,r)=>operationDuration(r.session)},
            {title:'录屏大小',render:(_,r)=><span title="原始录屏数据大小；转换后的 MP4 大小显示在下载按钮上">{r.access.recordingEvents?formatBytes(r.access.recordingBytes):'—'}</span>},
            {title:'录屏',render:(_,r)=>r.session.forwarding?'不适用':r.access.recordingState==='expired'?'已过期清理':r.access.recordingState==='disabled'?'未启用':r.access.recordingState==='writing'?'录制中':r.access.recordingEvents===0?'无内容':r.access.recordingState==='interrupted'?'部分录屏':'可回放'},
            {title:'操作',render:(_,r)=><Space wrap>{!['disabled','expired'].includes(r.access.recordingState)&&!r.session.forwarding&&observeAvailable&&r.session.state==='connected'&&(r.session.actorId===account.id||account.type==='admin')&&<><Button href={`/session-observer?sessionId=${r.session.id}`} target="_blank" rel="noopener noreferrer">只读观察</Button><Button onClick={()=>setSharing(r.session.id)}>分享</Button></>}<Button onClick={()=>{setCommandPage(1);setSelected(r.session.id)}}>详情与命令</Button>{mayReplay(r)&&<Button disabled={r.access.recordingState==='writing'||r.access.recordingEvents===0} onClick={()=>setReplay(r.session.id)}>录屏回放</Button>}{mayReplay(r)&&r.access.recordingState!=='writing'&&r.access.recordingEvents>0&&(r.access.recordingFormat==='native-rdp-mp4-v1'&&r.access.recordingEvents===1?<Button href={`/api/sessions/${r.session.id}/recording/segments/1?download=1`} download>下载 MP4</Button>:<BackendRecordingExport sessionId={r.session.id}/>)}{!r.session.closedAt&&(r.session.actorId===account.id||account.actions?.includes('identity.manage'))&&<Popconfirm title="终止此远程会话？" onConfirm={()=>terminate(r.session.id)}><Button danger>终止</Button></Popconfirm>}</Space>},
        ]}/></Card>
        <Modal open={!!sharing} title="会话只读分享" footer={null} width={800} onCancel={()=>setSharing('')} destroyOnHidden>{sharing&&<BackendSessionShares key={sharing} sessionId={sharing}/>}</Modal>
        <Modal open={!!selected} title="会话审计详情" footer={null} width={850} onCancel={()=>setSelected('')}>
            {detail.isError&&<Alert type="error" title={detail.error.message}/>}
            {detail.data&&<><Typography.Paragraph copyable>{detail.data.session.id}</Typography.Paragraph><p>{detail.data.access.assetName} · {labels[detail.data.session.state]}</p>
                <p>操作时长：{operationDuration(detail.data.session)}</p>{!detail.data.session.forwarding&&<p>录屏：{detail.data.access.recordingState==='expired'?'已按保留策略清理':detail.data.access.recordingState==='disabled'?'未启用':detail.data.access.recordingState}，{detail.data.access.recordingBytes} 字节，{detail.data.access.recordingEvents} 个事件。</p>}{mayReplay(detail.data)&&<Button style={{marginBottom:12}} disabled={detail.data.access.recordingState==='writing'||!detail.data.access.recordingEvents} onClick={()=>setReplay(selected)}>录屏回放</Button>}</>}
            {detail.data?.session.forwarding?<BackendForwardAudit key={selected} sessionId={selected}/>:['rdp','vnc'].includes(detail.data?.session.protocol??'')?<Alert type="info" title="图形会话审计" description="RDP/VNC 保存桌面输出录屏，不将图形键盘输入识别为命令。可通过录屏回放审查操作。"/>:<><Alert type="info" title="记录交互终端提交和显式执行命令" description="交互记录根据远端回显识别，不代表远端执行确认；密码等未回显输入、复杂多行编辑或全屏应用标记为未识别，不保存原始按键。脚本内部命令不在免安装审计范围，交互退出码未知。旧会话不会补生成命令记录。"/>
            {commands.isError&&<Alert type="error" title={commands.error.message}/>}
            <Table<Command> rowKey="id" dataSource={commands.data?.items??[]} loading={commands.isPending&&!!selected} scroll={{x:750}} pagination={{current:commandPage,pageSize:20,total:commands.data?.total??0,onChange:setCommandPage,showSizeChanger:false,showTotal:total=>`共 ${total} 条`}} columns={[
                {title:'来源',dataIndex:'source',width:100,render:v=><Tag color={v==='explicit-exec'?'blue':'default'}>{v==='explicit-exec'?'显式执行':'终端提交'}</Tag>},
                {title:'命令 / 说明',render:(_,v)=><><pre style={{whiteSpace:'pre-wrap',overflowWrap:'anywhere'}}>{v.command||'（未保存输入内容）'}</pre>{v.note&&<Typography.Text type="secondary">{v.note}</Typography.Text>}</>},
                {title:'状态',dataIndex:'state',width:110,render:v=>commandStates[v]??v},
                {title:'退出码',dataIndex:'exitCode',width:80,render:v=>v??'未知'},
                {title:'时间',dataIndex:'createdAt',width:160,render:v=>new Date(v).toLocaleString()},
            ]}/></>}
        </Modal>
        <Modal open={!!replay} title="会话录屏回放" footer={null} width={1100} onCancel={()=>setReplay('')} destroyOnHidden>
            {replay&&<><Button href={`/graphics-playback?sessionId=${replay}`} target="_blank" rel="noopener noreferrer">在独立页面回放</Button><BackendRecordingPlayer key={replay} sessionId={replay}/></>}
        </Modal>
    </section>;
}
