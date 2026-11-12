// Render module task cards and validate the backend design ownership registry.
// Uses only the Node.js standard library. Does not generate server code.
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import {fileURLToPath} from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const base = path.join(root, 'docs/backend');
const catalog = JSON.parse(await fs.readFile(path.join(base, 'module-catalog.json'), 'utf8'));
const modules = catalog.modules;
const byId = new Map(modules.map(module => [module.id, module]));
const usage = JSON.parse(await fs.readFile(path.join(base, 'usage-requirements.json'), 'utf8'));
const sourceReview = JSON.parse(await fs.readFile(path.join(base, 'usage-docs-sources.json'), 'utf8'));
const propertyCatalog = JSON.parse(await fs.readFile(path.join(base, 'system-property-catalog.json'), 'utf8'));
const sources = new Map(sourceReview.sources.map(source => [source.id, source]));
assert.equal(sources.size, sourceReview.sources.length, 'Duplicate usage source ID');
assert.equal(new Set(usage.requirements.map(r => r.id)).size, usage.requirements.length, 'Duplicate usage requirement');
for (const requirement of usage.requirements) {
  assert(byId.has(requirement.owner), 'Unknown usage owner: '+requirement.id);
  assert(requirement.acceptance && requirement.requirement && requirement.phase, 'Incomplete usage requirement: '+requirement.id);
  assert(requirement.sources.length > 0, 'Missing usage evidence: '+requirement.id);
  for (const id of requirement.collaborators) assert(byId.has(id) && id !== requirement.owner, 'Invalid collaborator: '+requirement.id);
  for (const id of requirement.sources) assert(sources.has(id), 'Unknown usage source: '+id);
  assert(byId.get(requirement.owner).usageRequirements.includes(requirement.id), 'Usage requirement not registered by owner: '+requirement.id);
}
for (const module of modules) {
  for (const id of module.usageRequirements) assert(usage.requirements.some(r => r.id === id && r.owner === module.id), 'Invalid module usage requirement: '+id);
}
for (const source of sources.values()) {
  assert(source.textReviewed, 'Unread source: '+source.id);
  assert(usage.requirements.some(r => r.sources.includes(source.id)), 'Source without planned requirement: '+source.id);
}
assert.equal(new Set(propertyCatalog.properties.map(p => p.key)).size, propertyCatalog.properties.length, 'Duplicate property key');
const effects = new Set(['live','security-live','new-session','next-job','listener','readonly']);
for (const property of propertyCatalog.properties) {
  assert(byId.has(property.owner), 'Unknown property owner: '+property.key);
  assert(property.internalType && property.validation && property.defaultSource, 'Incomplete property design: '+property.key);
  assert(Object.hasOwn(property, 'proposedDefault'), 'Missing proposed default: '+property.key);
  assert(effects.has(property.effect), 'Invalid property effect: '+property.key);
  assert(['read-only','read-write'].includes(property.access), 'Invalid property access: '+property.key);
  assert(property.officialDefault, 'Missing official default provenance: '+property.key);
  if (property.internalType === 'bool') assert.equal(typeof property.proposedDefault, 'boolean', property.key);
  if (property.internalType === 'int') assert(Number.isSafeInteger(property.proposedDefault), property.key);
  if (property.internalType === 'float') assert(Number.isFinite(property.proposedDefault), property.key);
}
assert.equal(byId.size, modules.length, 'Duplicate module ID');
assert.equal(new Set(modules.map(module => module.key)).size, modules.length, 'Duplicate module key');
const owners = new Map();
const visiting = new Set(), visited = new Set();
function visit(id) {
  assert(byId.has(id), 'Unknown prerequisite: '+id);
  assert(!visiting.has(id), 'Implementation prerequisite cycle: '+id);
  if (visited.has(id)) return;
  visiting.add(id);
  for (const dependency of byId.get(id).buildAfter) visit(dependency);
  visiting.delete(id); visited.add(id);
}
for (const module of modules) {
  visit(module.id);
  for (const table of module.tables) {
    assert(!owners.has(table), 'Duplicate table owner: '+table);
    owners.set(table, module.id);
  }
  for (const file of module.apiEvidence) await fs.access(path.join(root,file));
  for (const guide of module.implementationGuides ?? []) await fs.access(path.join(base,guide));
}
const model = await fs.readFile(path.join(base, 'data-model.md'),'utf8');
const existingTables = [...model.matchAll(/^\| ([a-z][a-z0-9_]*) \|/gm)].map(match=>match[1]);
for (const table of existingTables) assert(owners.has(table),'Missing owner: '+table);
for (const table of owners.keys()) assert(existingTables.includes(table),'Unknown logical table: '+table);
const slices = {
  F00:['事务、迁移、outbox/jobs、存储与测试底座','按实际负载增加扩展适配器'],
  M01:['本地登录、用户部门、会话、基础 MFA 与 API Key','W3 完善 Passkey；W4 外部身份与 OIDC 提供方'],
  M02:['角色操作权限、直接资源授权、基本策略决策','W3 完善时段/地域/IP 集、命令策略和部门继承场景'],
  M03:['统一资源注册、主机、分组、标签与导入导出','W3/W4 接入网站/数据库资源投影与复杂批量操作'],
  M04:['秘密加密、凭据引用、短期租约与查看审计','W3 轮换状态机与逐资产补偿'],
  M05:['直连、SSH 跳板、节点登记与容量分配','W3 自有 Agent、网关组、多跳和网络代理'],
  M06:['SSH 会话、终止、授权复核和终端录屏接入','W3 图形/Telnet、分享、原生 SSH、自有客户端与 FreeRDP 基础链路；W4 RDP 策略/录像/RemoteApp 分 profile 验收'],
  M07:['会话 SFTP、上传下载、编辑与容量控制','W3 共享存储、图形驱动目录与批量文件能力'],
  M08:['管理/登录审计接口、事务写入及查询骨架','W2 终端录像；W3 图形录像/转码；W4 SQL 审计接入'],
  M09:['网站资源、登录认证反向代理与访问审计','W3 同期完善公开访问、临时授权、响应改写和证书联动'],
  M10:['数据库资产、原生协议访问与 SQL 审计','W4 工单执行、结果核对及客户端协议完整性验证'],
  M11:['临时访问申请、审批、取消和授权联动','W4 与 M10 集成 SQL 工单和 unknown 人工核对'],
  M12:['脚本片段、远程任务、cron、幂等调度','各业务就绪后注册轮换/续签/备份处理器'],
  M13:['TLS 导入/续签、DNS challenge 与证书发布','W3/W4 用户客户端证书和吊销联动'],
  M14:['渠道、绑定、事件规则和可靠投递','随审批、告警和其他模块上线增加事件类型'],
  M15:['基础仪表盘、健康指标、告警与受控诊断','随新协议注册聚合指标与告警规则'],
  M16:['可验证的手动备份与隔离恢复','W3 定时、异地复制；生产部署前完成恢复演练'],
  M17:['模型/对话、只读工具、去重和用量控制','W4 经审批的写工具、取消、故障恢复和预算控制'],
  M18:['初始化状态、设置注册、品牌、能力门控与 CLI 骨架','随业务增加配置 schema、节点 ACK 和领域 CLI；103 个文档键逐项接入']
};
const bullet = items => items.map(item=>'- '+item).join('\n');
const moduleLink = module => `[${module.id} ${module.name}](modules/${module.id}-${module.key}.md)`;
await fs.mkdir(path.join(base,'modules'),{recursive:true});
for (const module of modules) {
  const [first,later]=slices[module.id];
  const owned = usage.requirements.filter(r => r.owner === module.id);
  const collaborated = usage.requirements.filter(r => r.collaborators.includes(module.id));
  const usageSection = '\n## 使用文档补充需求\n\n来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。\n\n' +
    (owned.length ? owned.map(r => `### ${r.id} ${r.title}\n\n- 阶段：${r.phase}；协作：${r.collaborators.join('、')}。\n- 功能要求：${r.requirement}\n- 验收条件：${r.acceptance}\n- 来源：${r.sources.map(id => `[${sources.get(id).title}](${sources.get(id).url})`).join('、')}。`).join('\n\n') : '本模块提供跨需求共用的基础机制，不拥有使用场景规则。') +
    '\n\n协作项：' + (collaborated.length ? collaborated.map(r => `${r.id}（主责 ${r.owner}）`).join('、') : '无独立登记项') + '。\n';
  const text = `# ${module.id} ${module.name}\n\n${module.goal}\n\n状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：${module.group}。首次交付批次：${module.wave}。目录：\`backend/internal/${module.id==='F00'?'platform':'modules/'+module.key}\`。\n\n## 开发范围\n\n${bullet(module.features)}\n\n## 本模块不负责\n\n${bullet(module.excludes)}\n\n## 交付切片\n\n- 首次交付：${first}。\n- 后续交付：${later}。\n- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。\n\n## 数据所有权\n\n${bullet(module.tables.map(table=>'\`'+table+'\`'))}\n\n${module.physicalTableAliases ? '阶段物理表名映射（同一数据所有权，不另计逻辑表）：'+Object.entries(module.physicalTableAliases).map(([physical,logical])=>'`'+physical+'` → `'+logical+'`').join('；')+'。\n\n' : ''}仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。\n\n## 对外契约\n\n${bullet(module.ports)}\n\n这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。\n\n## 事件\n\n${bullet(module.events)}\n\n事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。\n\n## 开发前置与集成\n\n${module.buildAfter.length ? module.buildAfter.map(id=>`- [${id} ${byId.get(id).name}](${id}-${byId.get(id).key}.md)`).join('\n') : '- 无业务模块前置。'}\n\n前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。\n\n## 前端契约证据\n\n${module.apiEvidence.length ? module.apiEvidence.map(file=>`- [${file}](../../../${file})`).join('\n') : '- 基础设施无独立业务页面，参考任务、事务与部署设计。'}\n\n同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。\n${usageSection}\n## 已确定的实施规范\n\n先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：\n\n${(module.implementationGuides ?? []).map(file=>`- [${file}](../${file})`).join("\n")}\n\n## 验收条件\n\n${bullet(module.acceptance)}\n\n同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。\n\n---\n\n由 \`node scripts/backend-module-docs.mjs\` 根据 \`module-catalog.json\` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。\n`;
  await fs.writeFile(path.join(base,'modules',module.id+'-'+module.key+'.md'),text);
}
const lines=['# 模块开发任务索引','','本索引按功能模块组织独立交付。W0..W4 是首次交付顺序，完整范围及后续扩展见各任务卡。所有状态均为“待开发”，没有后端实现进度被隐含标记为完成。','','| 模块 | 首次批次 | 首次交付 | 后续范围 |','|---|---|---|---|'];
for(const module of modules) lines.push(`| ${moduleLink(module)} | ${module.wave} | ${slices[module.id][0]} | ${slices[module.id][1]} |`);
lines.push('','## 数据归属索引','','下列每张逻辑表只有一个写入所有者。原数据模型按主题分组，不能代替本归属表。','','| 逻辑表 | 写入所有者 |','|---|---|');
for(const [table,id] of [...owners].sort((a,b)=>a[0].localeCompare(b[0]))) lines.push(`| \`${table}\` | ${moduleLink(byId.get(id))} |`);
await fs.writeFile(path.join(base,'module-backlog.md'),lines.join('\n')+'\n');
const coverage = ['# 官方使用文档覆盖矩阵','','这是阅读与规划覆盖，不是功能实现状态。正文和图示已核对，嵌入视频缺口见[核对说明](usage-docs-review.md)。来源元数据见[JSON](usage-docs-sources.json)，每个页面至少对应一项有主责模块的开发需求。','','| 页面 | 范围 | 图示引用 | 对应需求及主责模块 |','|---|---|---|---|'];
for (const source of sources.values()) {
  const matched = usage.requirements.filter(r => r.sources.includes(source.id));
  coverage.push(`| [${source.title}](${source.url}) | ${source.category === 'usage' ? '使用文档' : '关联资料'} | ${source.imagesReviewed} | ${matched.map(r => `${r.id} / ${r.owner}`).join('、')} |`);
}
coverage.push('','## 需求分配','','| 需求 | 主责 | 协作 | 阶段 | 验收要点 |','|---|---|---|---|---|');
for (const r of usage.requirements) coverage.push(`| ${r.id} ${r.title} | ${moduleLink(byId.get(r.owner))} | ${r.collaborators.join('、')} | ${r.phase} | ${r.acceptance} |`);
coverage.push('','由 `node scripts/backend-module-docs.mjs` 生成；修改来源 JSON 或需求 JSON 后重新生成。','');
await fs.writeFile(path.join(base,'usage-coverage.md'),coverage.join('\n'));
const cell = value => String(typeof value === 'object' ? JSON.stringify(value) : value).replaceAll('|','\\|').replaceAll('\n',' ');
const properties = ['# 系统属性兼容目录','','以下103键来自[官方系统属性文档](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。官方 wireType 原样保留为 string；**本表默认值是本项目设计，不代表官方默认值**。运行实现尚未开始。默认空值表示未配置，生成型私钥由相应启用流程生成；版本取构建元数据。JSON中 officialDefault 保留来源缺口。跨字段规则及effect语义见[配置实施规范](implementation/settings-entitlements.md)。当前前端还有文档页未列出的扩展键，见该规范。','','## 类型与默认值','','| 键 | 内部类型 | 本项目默认 | 单位 | owner |','|---|---|---|---|---|'];
for (const p of propertyCatalog.properties) properties.push(`| \`${p.key}\` | ${p.internalType} | ${cell(p.proposedDefault)} | ${p.internalUnit ?? (p.unit === '未在本页明确' ? '不适用/见校验' : p.unit)} | ${moduleLink(byId.get(p.owner))} |`);
properties.push('','## 校验与应用方式','','M18拥有写入入口，各owner校验；所有秘密普通读取掩码。只读投影不形成第二份业务权威数据。','','| 键 | 校验 | 生效方式 | 访问 | 秘密 |','|---|---|---|---|---|');
for (const p of propertyCatalog.properties) properties.push(`| \`${p.key}\` | ${cell(p.validation)} | ${p.effect} | ${p.access} | ${p.secret ? '是' : '否'} |`);
properties.push('','机器可读源：[system-property-catalog.json](system-property-catalog.json)。由模块文档脚本生成；检查覆盖和类型，不表示后端已实现这些规则。','');
await fs.writeFile(path.join(base,'system-property-catalog.md'),properties.join('\n'));
console.log(JSON.stringify({modules:modules.length,businessModules:modules.filter(m=>m.id!=='F00').length,tables:owners.size,evidenceReferences:modules.reduce((n,m)=>n+m.apiEvidence.length,0),usagePages:sourceReview.sources.filter(s=>s.category==='usage').length,supportPages:sourceReview.sources.filter(s=>s.category==='support').length,usageRequirements:usage.requirements.length,propertyKeys:propertyCatalog.properties.length,validation:'ownership, sources, requirements, property implementation schemas, guide links and prerequisite DAG passed'}));
