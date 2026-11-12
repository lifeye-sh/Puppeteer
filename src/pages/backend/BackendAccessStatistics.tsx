import {useState} from 'react';
import {Alert, Button, Card, DatePicker, Select, Space, Statistic, Table, Typography} from 'antd';
import {useQuery} from '@tanstack/react-query';
import dayjs from 'dayjs';
import requests from '@/api/core/requests';
import {useBackendRuntime} from '@/hook/BackendRuntimeContext';

export type StatisticsTotals = {sessions:number;users:number;assets:number;failed:number;seconds:number};
export type StatisticsDay = StatisticsTotals & {date:string};
export type SessionStatistics = {from:string;to:string;timezone:string;scope:string;totals:StatisticsTotals;days:StatisticsDay[];protocols:{protocol:string;count:number}[]};
export const statisticsTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

export default function BackendAccessStatistics(){
 const {account}=useBackendRuntime();
 const canAll=!!account.actions?.includes('session.read');
 const [scope,setScope]=useState(canAll?'all':'own');
 const [range,setRange]=useState<[string,string]>([dayjs().subtract(13,'day').format('YYYY-MM-DD'),dayjs().format('YYYY-MM-DD')]);
 const all=canAll&&scope==='all';
 const params=new URLSearchParams({all:String(all),from:range[0],to:range[1],timezone:statisticsTimezone});
 const query=useQuery({queryKey:['r16-statistics',params.toString()],queryFn:()=>requests.get<SessionStatistics>(`/statistics/sessions?${params}`,{errorMode:'local'}),refetchInterval:30000});
 const totals=query.data?.totals;
 return <Space orientation="vertical" size="large" style={{width:'100%'}}>
  <Typography.Title level={3}>访问统计</Typography.Title>
  <Space wrap><DatePicker.RangePicker value={[dayjs(range[0]),dayjs(range[1])]} allowClear={false} disabledDate={d=>d.isAfter(dayjs(),'day')} onChange={v=>{if(v?.[0]&&v?.[1])setRange([v[0].format('YYYY-MM-DD'),v[1].format('YYYY-MM-DD')])}}/>
   {canAll&&<Select aria-label="统计范围" value={scope} onChange={setScope} options={[{value:'all',label:'全部会话'},{value:'own',label:'仅本人'}]}/>}
   <Button onClick={()=>void query.refetch()} loading={query.isFetching}>刷新</Button>
   <Button href={`/api/statistics/sessions?${params}&format=csv`} disabled={!query.data||query.isError} download>导出 CSV</Button>
  </Space>
  <Typography.Text type="secondary">时区：{statisticsTimezone}。统计所选日期内建立且仍在保留期内的主机会话，包含 SSH、Telnet、RDP、VNC；数据库与 Web 审计不计入。操作时长从实际连接成功开始计算。最多选择 366 天。</Typography.Text>
  {query.isError&&<Alert type="error" title="统计加载失败" description={query.error.message}/>}
  <div style={{display:'grid',gridTemplateColumns:'repeat(auto-fit,minmax(150px,1fr))',gap:16}}>{[
   ['访问次数',totals?.sessions],['访问用户',totals?.users],['访问资产',totals?.assets],['失败会话',totals?.failed],['操作时长（分钟）',totals?Math.round(totals.seconds/60):undefined],
  ].map(([label,value])=><Card key={String(label)}><Statistic title={label} value={value??'—'}/></Card>)}</div>
  <Space wrap>{query.data?.protocols.map(p=><Card size="small" key={p.protocol}>{p.protocol.toUpperCase()}：{p.count} 次</Card>)}</Space>
  <Table<StatisticsDay> rowKey="date" loading={query.isFetching} dataSource={query.data?.days} scroll={{x:680}} pagination={{pageSize:31,showSizeChanger:false}} columns={[
   {title:'日期',dataIndex:'date'},{title:'访问次数',dataIndex:'sessions'},{title:'访问用户',dataIndex:'users'},{title:'访问资产',dataIndex:'assets'},{title:'失败会话',dataIndex:'failed'},{title:'操作时长（秒）',dataIndex:'seconds'},
  ]}/>
 </Space>;
}
