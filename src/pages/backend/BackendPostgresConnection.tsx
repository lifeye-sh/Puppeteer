import {Alert,Button,Descriptions,Space,Tabs,Typography} from 'antd';
import {Link} from 'react-router-dom';

export default function BackendPostgresConnection({host,port,database,username,command}:{host:string;port:string;database:string;username:string;command:string}) {
 const address=host.includes(':')?`[${host}]`:host;
 const jdbc=`jdbc:postgresql://${address}:${Number(port)}/${encodeURIComponent(database)}?sslmode=verify-full&readOnly=true&readOnlyMode=always`;
 return <Tabs defaultActiveKey="desktop" items={[
  {key:'desktop',label:'DBeaver / JDBC',children:<Space orientation="vertical" size={12} style={{width:'100%'}}>
   <Descriptions size="small" column={2} items={[
    {key:'driver',label:'驱动',children:'PostgreSQL · JDBC 42.7.14'},
    {key:'port',label:'端口',children:port},
    {key:'host',label:'主机',children:<Typography.Text copyable>{host}</Typography.Text>},
    {key:'database',label:'数据库',children:<Typography.Text copyable>{database}</Typography.Text>},
    {key:'user',label:'用户名',span:2,children:<Typography.Text copyable>{username}</Typography.Text>},
   ]}/>
   <Typography.Paragraph>密码使用<Link to="/info">我的账号</Link>中生成的数据库专用密码。旧版 MySQL 密码需由本人重置以生成 SCRAM 材料；重置会使旧密码失效。</Typography.Paragraph>
   <Button href="/api/database-proxy/certificate" download="next-terminal-mysql.pem">下载代理证书</Button>
   <Typography.Paragraph>在 SSL 页启用 SSL，验证模式选择 <Typography.Text code>verify-full</Typography.Text>，根证书选择刚下载的 PEM 文件。驱动属性设置 <Typography.Text code>readOnly=true</Typography.Text> 和 <Typography.Text code>readOnlyMode=always</Typography.Text>；保留默认扩展查询模式，不填写自定义 currentSchema 或 options。</Typography.Paragraph>
   <Typography.Text type="secondary">可复制 JDBC URL；用户名、密码和根证书路径在连接属性中单独填写。</Typography.Text>
   <Typography.Paragraph copyable={{text:jdbc}} style={{overflowWrap:'anywhere',fontFamily:'monospace'}}>{jdbc}</Typography.Paragraph>
   <Alert type="info" title="JDBC 驱动自动化验收已通过；DBeaver 桌面操作仍待验收。" description="本机 backend.localhost 应解析到 127.0.0.1。内网测试资产可展开 public → 表 → items，或执行 SELECT id, label FROM items ORDER BY id; 验证。"/>
  </Space>},
  {key:'cli',label:'psql 命令',children:<>
   <Typography.Paragraph>将下载的代理证书放在命令执行目录。下列 Linux / macOS 命令会单独提示输入个人数据库密码。</Typography.Paragraph>
   <Typography.Paragraph copyable={{text:command}} style={{fontFamily:'monospace',whiteSpace:'pre-wrap',overflowWrap:'anywhere'}}>{command}</Typography.Paragraph>
  </>},
  {key:'scope',label:'支持范围',children:<Alert type="warning" title="只读预览" description="支持 SELECT、SHOW、只读事务、保存点、分页、取消和 COPY TO STDOUT。写入、COPY FROM、PG 工单及未批准的函数、语法暂未开放。执行超时、响应大小和结果行数由系统管理中的数据库运行配置控制，修改后新建连接生效。绑定参数只记录类型、长度和 NULL 状态；SQL 文本仍纳入审计。"/>},
 ]}/>;
}
