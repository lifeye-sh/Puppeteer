// Design-time inventory. Requires the repository's npm dependencies (@babel/parser).
// It describes frontend expectations; it does not assert that server routes exist.
import {parse} from '@babel/parser';
import {readdir, readFile, mkdir, writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {requestMethods} from './backend-inventory-methods.mjs';
import {requestPath} from './backend-inventory-paths.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const output = path.join(root, 'docs/backend');
const calls = [], nativeFetchCalls = [], inherited = [], directUrls = [], routeReferences = [], models = [], parseErrors = [];
const crud = {getById: ['GET','/{id}'], getPaging: ['GET','/paging?{params}'], getAll: ['GET',''], create: ['POST',''], updateById: ['PUT','/{id}'], deleteById: ['DELETE','/{id}']};
async function files(dir) {
  const result = [];
  for (const item of await readdir(dir, {withFileTypes: true})) {
    const full = path.join(dir, item.name);
    if (item.isDirectory()) result.push(...await files(full));
    else if (/\.tsx?$/.test(item.name)) result.push(full);
  }
  return result.sort();
}
function walk(node, visit, ancestors = []) {
  if (!node || typeof node !== 'object') return;
  if (node.type) visit(node, ancestors);
  for (const [key, value] of Object.entries(node)) {
    if (['loc','start','end','extra','comments','tokens'].includes(key)) continue;
    if (Array.isArray(value)) for (const child of value) walk(child, visit, [...ancestors, node]);
    else if (value && typeof value === 'object') walk(value, visit, [...ancestors, node]);
  }
}
for (const full of await files(path.join(root, 'src'))) {
  const file = path.relative(root, full).replaceAll('\\','/');
  const source = await readFile(full, 'utf8');
  let ast;
  try { ast = parse(source, {sourceType: 'module', plugins: ['typescript', 'jsx']}); }
  catch (error) { parseErrors.push({file, message: error.message}); continue; }
  const slice = node => node ? source.slice(node.start, node.end) : '';
  const classGroups = new Map();
  walk(ast, node => {
    if (node.type !== 'ClassDeclaration') return;
    let group;
    for (const member of node.body.body) if (member.key?.name === 'group' && member.value?.type === 'StringLiteral') group = member.value.value;
    walk(node.body, child => {
      if (child.type === 'CallExpression' && child.callee.type === 'Super' && child.arguments[0]?.type === 'StringLiteral') group ??= child.arguments[0].value;
    });
    classGroups.set(node, group);
    if (node.superClass?.name === 'Api' && group) {
      const overrides = new Set(node.body.body.map(member => member.key?.name));
      for (const [method, [verb, suffix]] of Object.entries(crud)) if (!overrides.has(method)) inherited.push({file, line: node.loc.start.line, class: node.id.name, member: method, method: verb, pathTemplate: '/api/'+group+suffix, evidence: 'inherited-api-method', note: 'Base class exposes this method; frontend usage and server exposure must be verified.'});
    }
  });
  const normalize=(node,group)=>requestPath(node,source,group);
  walk(ast, (node, ancestors) => {
    const owner = [...ancestors].reverse().find(parent => parent.type === 'ClassDeclaration');
    const group = classGroups.get(owner);
    const member = [...ancestors].reverse().find(parent => ['ClassProperty','ClassMethod','ObjectProperty','ObjectMethod','VariableDeclarator'].includes(parent.type));
    if (file.startsWith('src/api/') && ['TSInterfaceDeclaration','TSTypeAliasDeclaration','TSEnumDeclaration'].includes(node.type)) models.push({file, line: node.loc.start.line, name: node.id.name, kind: node.type});
    if (node.type === 'CallExpression' && node.callee.type === 'MemberExpression' && node.callee.object.name === 'requests') {
      const expression = slice(node.arguments[0]);
      for(const {method,encoding} of requestMethods(node.callee)) calls.push({file, line: node.loc.start.line, class: owner?.id?.name, member: member?.key?.name ?? member?.id?.name, method, encoding, methodExpression:node.callee.computed?slice(node.callee.property):undefined, pathTemplate: normalize(node.arguments[0], group), expression, argumentExpressions: node.arguments.slice(1).map(slice), typeArguments: slice(node.typeParameters ?? node.typeArguments), evidence: 'explicit-request-call'});
    }
    if (node.type === 'CallExpression' && node.callee.type === 'Identifier' && node.callee.name === 'fetch') {
      const methodProperty = node.arguments[1]?.properties?.find(property => property.key?.name === 'method');
      nativeFetchCalls.push({file, line: node.loc.start.line, method: methodProperty?.value?.value ?? (node.arguments[1] ? 'DYNAMIC' : 'GET'), pathTemplate: normalize(node.arguments[0], group), expression: slice(node.arguments[0]), evidence: 'native-fetch-call'});
    }
    if (['StringLiteral','JSXText'].includes(node.type) && /^\/(api|swagger|agent)\//.test(node.value.trim())) routeReferences.push({file,line:node.loc.start.line,pathTemplate:node.value.trim(),evidence:'literal-route-reference',note:'May be a displayed documentation URL or permission constant, not a request.'});
    if (node.type === 'TemplateLiteral' && /\$\{base(?:WebSocket)?Url\(\)\}/.test(slice(node))) directUrls.push({file, line: node.loc.start.line, transport: slice(node).includes('baseWebSocketUrl') ? 'websocket' : 'http-url', pathTemplate: normalize(node,group), expression: slice(node), evidence: 'url-expression', note:'Inspect caller for HTTP verb, query parameters, response type and authentication.'});
  });
}
let commit = 'unknown';
try {commit = execFileSync('git',['rev-parse','HEAD'],{cwd:root,encoding:'utf8'}).trim();} catch {}
const result = {schemaVersion:1, sourceCommit:commit, scope:'Frontend static inventory, NOT a server OpenAPI specification. Templates retain dynamic expressions. Inherited CRUD entries are candidates, not permission grants. Direct URLs may duplicate call sites. Non-API auth/discovery/native-listener routes require separate design.', counts:{explicitRequestCalls:calls.length,nativeFetchCalls:nativeFetchCalls.length,inheritedCrudCandidates:inherited.length,directUrlExpressions:directUrls.length,literalRouteReferences:routeReferences.length,apiModels:models.length,parseErrors:parseErrors.length}, calls,nativeFetchCalls,inherited,directUrls,routeReferences,models,parseErrors};
await mkdir(output,{recursive:true});
await writeFile(path.join(output,'frontend-contract-inventory.json'),JSON.stringify(result,null,2)+'\n');
const escape = value => String(value ?? '').replaceAll('|','\\|').replaceAll('\n',' ');
const lines = ['# 前端接口证据清单','',`源码提交：\`${commit}\`。由 \`node scripts/backend-contract-inventory.mjs\` 生成。`,'','本清单记录调用点，不代表已实现的服务端路由，也不等于去重后的接口总数。动态表达式保留原样；query、body、类型与鉴权需要结合源文件确认。继承 CRUD 仅作为候选，不应自动开放删除、更新等权限。','',`请求封装调用 ${calls.length} 处；原生 fetch ${nativeFetchCalls.length} 处；继承 CRUD 候选 ${inherited.length} 条；直接 URL ${directUrls.length} 处；API 模型 ${models.length} 个；解析失败 ${parseErrors.length} 个。`,''];
for (const [title,entries] of [['显式 HTTP 调用',calls],['原生 fetch 调用',nativeFetchCalls],['继承 CRUD 候选',inherited],['WebSocket 和下载等直接 URL',directUrls],['页面中硬编码的路由引用',routeReferences]]) {
  lines.push('## '+title,'','| 方法或传输 | 路径模板 | 源码位置 |','|---|---|---|');
  for (const item of entries) lines.push(`| ${escape(item.method ?? item.transport)} | \`${escape(item.pathTemplate)}\` | [${escape(item.file)}:${item.line}](../../${item.file}#L${item.line}) |`);
  lines.push('');
}
await writeFile(path.join(output,'frontend-contract-inventory.md'),lines.join('\n')+'\n');
console.log(JSON.stringify(result.counts));
if (parseErrors.length) process.exitCode = 1;
