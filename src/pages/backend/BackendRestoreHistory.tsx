import {Alert,Card,Table,Tag,Timeline,Typography} from 'antd';
import {useQuery} from '@tanstack/react-query';
import requests from '@/api/core/requests';
type Event={restoreId:string;state:string;at:string};
type History={restoreId:string;events:Event[]};
const labels:Record<string,string>={preflight:'开始检查',validated:'归档完整性通过',safety_backup:'备份切换前数据',staging:'恢复隔离数据库',verifying:'验证密钥与文件',prepared:'恢复验证完成',switching:'已切换，等待开放',writes_opened:'已开放业务',completed:'恢复完成',failed:'中断或失败',rolled_back:'已回退原数据库'};
export default function BackendRestoreHistory(){
 const q=useQuery({queryKey:['r11-restore-history'],queryFn:()=>requests.get<{activeRestoreId:string;items:History[]}>('/admin/backup/restores',{errorMode:'local'}),refetchInterval:5000});
 return <Card title="独立恢复记录"><Alert type="info" showIcon title="离线恢复，检查通过后开放业务" description="管理员通过运维脚本选择备份和离线私钥。恢复前自动备份当前环境；新数据库通过验证后先保持冻结，开放前可以回退。恢复后需重新登录，旧会话和旧任务不会继续运行。" style={{marginBottom:16}}/>
 {q.data?.activeRestoreId&&<Typography.Paragraph>当前环境恢复编号：<Typography.Text copyable>{q.data.activeRestoreId}</Typography.Text></Typography.Paragraph>}
 {q.isError&&<Alert type="error" title="恢复记录读取失败，请重试或检查运维日志"/>}
 <Table<History> rowKey="restoreId" loading={q.isLoading} dataSource={q.data?.items} pagination={{pageSize:8}} expandable={{expandedRowRender:row=><Timeline items={row.events.map(e=>({children:`${new Date(e.at).toLocaleString()} · ${labels[e.state]??e.state}`}))}/>}} columns={[
 {title:'恢复编号',dataIndex:'restoreId',render:v=><Typography.Text copyable>{v}</Typography.Text>},
 {title:'开始时间',render:(_,r)=>r.events[0]?new Date(r.events[0].at).toLocaleString():'—'},
 {title:'当前阶段',render:(_,r)=>{const state=r.events[r.events.length-1]?.state??'';return <Tag color={state==='completed'?'success':state==='failed'?'error':'processing'}>{labels[state]??state}</Tag>}}
 ]}/></Card>
}
