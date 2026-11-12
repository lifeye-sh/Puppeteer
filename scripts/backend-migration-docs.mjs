// Validate migration planning coverage and render user-facing execution cards.
// Coverage is not implementation progress or user acceptance.
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import {fileURLToPath} from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const base = path.join(root, 'docs/backend');
const read = async name => JSON.parse(await fs.readFile(path.join(base, name), 'utf8'));
const {rounds} = await read('migration-rounds.json');
const {modules} = await read('module-catalog.json');
const {requirements} = await read('usage-requirements.json');
const moduleMap = new Map(modules.map(m => [m.id, m]));
const requirementMap = new Map(requirements.map(r => [r.id, r]));
const byId = new Map(rounds.map(r => [r.id, r]));
assert.equal(rounds.length, byId.size, 'Duplicate round ID');
const app = await fs.readFile(path.join(root, 'src/App.tsx'), 'utf8');
const routes = new Set([...app.matchAll(/path:\s*(['"])(.*?)\1/g)].map(m => m[2]));
const visited = new Set(), active = new Set();
function visit(id) {
    assert(byId.has(id), 'Unknown prerequisite '+id);
    assert(!active.has(id), 'Round dependency cycle '+id);
    if (visited.has(id)) return;
    active.add(id);
    for (const dep of byId.get(id).dependsOn) visit(dep);
    active.delete(id); visited.add(id);
}
for (const round of rounds) {
    visit(round.id);
    for (const field of ['backendTasks','frontendTasks','userSteps','modules','pages','evidence']) {
        assert(round[field]?.length, `Missing ${field} for ${round.id}`);
    }
    assert(round.exitGate && round.negativeCase && round.status, `Missing acceptance for ${round.id}`);
    assert.equal(typeof round.userAccepted, 'boolean');
    assert.equal(typeof round.backendVerified, 'boolean');
    if (round.status === 'accepted') assert(round.backendVerified && round.userAccepted, 'Cannot accept unverified round '+round.id);
    for (const id of round.modules) assert(moduleMap.has(id), 'Unknown module '+id);
    for (const id of round.requirements) assert(requirementMap.has(id), 'Unknown requirement '+id);
    for (const page of round.pages) assert(routes.has(page.split('?')[0]), 'Unknown page '+page);
    for (const file of round.evidence) await fs.access(path.join(root, file));
}
for (const {id} of modules) assert(rounds.some(r => r.modules.includes(id)), 'Module missing from plan '+id);
for (const {id} of requirements) assert(rounds.some(r => r.requirements.includes(id)), 'Requirement missing from plan '+id);
const lines = ['# 前端逐轮迁移执行卡', '',
    `由 migration-rounds.json 生成。配套[迁移主计划](frontend-migration-plan.md)和[单轮验收记录模板](migration-acceptance-template.md)。当前后端自测完成 ${rounds.filter(r=>r.backendVerified).length} 轮，用户验收完成 ${rounds.filter(r=>r.userAccepted).length} 轮；未执行用例不能标通过。`, '',
    '| 轮次 | 交付目标 | 模块批次 | 用户验收状态 |', '|---|---|---|---|'];
for (const r of rounds) lines.push(`| [${r.id}](#${r.id.toLowerCase()}) | ${r.title} | ${r.wave} | ${r.status} / ${r.userAccepted ? '已验收' : '未验收'} |`);
for (const r of rounds) {
    lines.push('', `<a id="${r.id.toLowerCase()}"></a>`, '', `## ${r.id} ${r.title}`, '',
        `前置：${r.dependsOn.join('、') || '无'}。模块：${r.modules.join('、')}。使用需求：${r.requirements.join('、') || '验证环境，不独占业务需求'}。`, '',
        '**后端交付**', '', ...r.backendTasks.map(t=>'- '+t), '',
        '**前端交付**', '', ...r.frontendTasks.map(t=>'- '+t), '',
        `**验证入口**：${r.pages.map(p=>'`'+p+'`').join('、')}（均在新环境域名内，R00旧站点对照除外）。`, '',
        '**用户操作**', '', ...r.userSteps.map((t,i)=>`${i+1}. ${t}`), '',
        `**反向用例**：${r.negativeCase}。`, '', `**通过门槛**：${r.exitGate}。`, '',
        `**当前状态**：${r.status}；后端自测 ${r.backendVerified ? '已通过' : '未通过/未执行'}；用户验收 ${r.userAccepted ? '已通过' : '待确认'}。`, '',
        '每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。', '',
        '**源码入口**', '', ...r.evidence.map(f=>`- [${f}](../../${f})`));
}
lines.push('', '## 使用需求覆盖（仅规划）', '', '| 需求 | 负责开发/验证轮次 |', '|---|---|');
for (const req of requirements) lines.push(`| ${req.id} ${req.title} | ${rounds.filter(r=>r.requirements.includes(req.id)).map(r=>`[${r.id}](#${r.id.toLowerCase()})`).join('、')} |`);
lines.push('', '## 模块覆盖（仅规划）', '', '| 模块 | 涉及轮次 |', '|---|---|');
for (const mod of modules) lines.push(`| [${mod.id} ${mod.name}](modules/${mod.id}-${mod.key}.md) | ${rounds.filter(r=>r.modules.includes(mod.id)).map(r=>`[${r.id}](#${r.id.toLowerCase()})`).join('、')} |`);
await fs.writeFile(path.join(base, 'migration-rounds.md'), lines.join('\n')+'\n');
console.log(JSON.stringify({rounds:rounds.length,modules:modules.length,requirements:requirements.length,
    backendVerified:rounds.filter(r=>r.backendVerified).length,userAccepted:rounds.filter(r=>r.userAccepted).length,
    validation:'DAG, route/source references, module and usage coverage passed'}));
