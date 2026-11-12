import {readFile,writeFile,readdir,access} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {parse} from '@babel/parser';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const inventory=JSON.parse(await readFile(path.join(root,'docs/backend/frontend-contract-inventory.json'),'utf8'));
const routesFile=process.argv[2];
if(!routesFile)throw Error('Usage: node scripts/backend-ui-contract-audit.mjs <nt-routes.json>');
const registered=JSON.parse((await readFile(path.resolve(root,routesFile),'utf8')).replace(/^\uFEFF/,''));
const reviewed=JSON.parse(await readFile(path.join(root,'docs/backend/acceptance/R16-ui-dynamic-routes.json'),'utf8')).entries;
const routeKeys=new Set(registered.routes.filter(r=>!r.fallback).map(r=>r.method+' '+r.path));
for(const mapping of reviewed)for(const key of mapping.routes)if(!routeKeys.has(key))throw Error('Reviewed mapping refers to unregistered route: '+key);
function walk(n,fn){if(!n||typeof n!=='object')return;if(n.type)fn(n);for(const [k,v]of Object.entries(n)){if(['loc','extra','comments','tokens'].includes(k))continue;if(Array.isArray(v))v.forEach(x=>walk(x,fn));else if(v&&typeof v==='object')walk(v,fn);}}
const unwrap=n=>n?.type==='TSAsExpression'||n?.type==='ParenthesizedExpression'?unwrap(n.expression):n;
function value(n,env={}){n=unwrap(n);if(n?.type==='StringLiteral')return n.value;if(n?.type==='Identifier')return env[n.name];if(n?.type==='TemplateLiteral'){let s='';for(let i=0;i<n.quasis.length;i++){s+=n.quasis[i].value.cooked??n.quasis[i].value.raw;if(i<n.expressions.length){const v=value(n.expressions[i],env);if(v===undefined)return;s+=v;}}return s;}}
function attr(n,key,source){const a=n.attributes?.find(x=>x.type==='JSXAttribute'&&x.name.name===key);if(!a)return;return a.value?.type==='StringLiteral'?a.value.value:a.value?.type==='JSXExpressionContainer'?source.slice(a.value.expression.start,a.value.expression.end):true;}
const appSource=await readFile(path.join(root,'src/App.tsx'),'utf8');const app=parse(appSource,{sourceType:'module',plugins:['typescript','jsx']});
const imports=new Map();let array;
walk(app,n=>{if(n.type==='ImportDeclaration')for(const s of n.specifiers)imports.set(s.local.name,n.source.value);if(n.type==='VariableDeclarator'&&n.id?.name==='backendRoutes')array=n.init;if(n.type==='VariableDeclarator'&&n.id?.name){walk(n.init,c=>{if(c.type==='CallExpression'&&c.callee.type==='Import'&&c.arguments[0]?.type==='StringLiteral')imports.set(n.id.name,c.arguments[0].value);});}});
const pages=[];const unresolvedRoutes=[];
function page(n,env={}){n=unwrap(n);if(n?.type==='ObjectExpression'){const p=n.properties.find(x=>x.key?.name==='path'),e=n.properties.find(x=>x.key?.name==='element');const url=value(p?.value,env);if(url===undefined){unresolvedRoutes.push(appSource.slice(n.start,n.end));return;}const entry={path:url,line:n.loc.start.line,components:[],permission:[],capabilities:[],props:[]};walk(e?.value,c=>{if(c.type!=='JSXOpeningElement')return;const name=c.name?.name;if(name==='PermissionGate')entry.permission.push(attr(c,'action',appSource));if(name==='CapabilityGate')entry.capabilities.push(attr(c,'feature',appSource));if(name?.startsWith('Backend')||name==='SetupPage'){entry.components.push({name,import:imports.get(name)});entry.props.push(...c.attributes.filter(x=>x.type==='JSXAttribute').map(x=>({component:name,name:x.name.name,value:attr(c,x.name.name,appSource)})));}});pages.push(entry);return;}
 if(n?.type==='SpreadElement'){const call=unwrap(n.argument);const values=unwrap(call?.callee?.object);const fn=call?.arguments?.[0];if(call?.type==='CallExpression'&&call.callee.property?.name==='map'&&values?.type==='ArrayExpression'&&fn?.type==='ArrowFunctionExpression'){for(const element of values.elements){const v=value(element);if(v!==undefined)page(fn.body,{...env,[fn.params[0].name]:v});}return;}}
 unresolvedRoutes.push(appSource.slice(n?.start??0,n?.end??0));}
for(const n of array.elements)page(n);
const files=[];for(const entry of await readdir(path.join(root,'src/pages/backend'))){if(/\.tsx?$/.test(entry))files.push('src/pages/backend/'+entry);}files.push('src/hook/BackendRuntimeContext.tsx');
const fileRecords=[];
for(const file of files){const source=await readFile(path.join(root,file),'utf8');const ast=parse(source,{sourceType:'module',plugins:['typescript','jsx']});const buttons=[],fields=[],dependencies=[];
 walk(ast,n=>{
  if(n.type==='ImportDeclaration'&&(n.source.value.startsWith('.')||n.source.value.startsWith('@/pages/backend/')))dependencies.push(n.source.value);
  if(n.type!=='JSXElement')return;const o=n.openingElement;const tag=o.name.type==='JSXIdentifier'?o.name.name:o.name.type==='JSXMemberExpression'?o.name.object.name+'.'+o.name.property.name:'';
  if(['Button','button','Popconfirm'].includes(tag))buttons.push({line:n.loc.start.line,tag,label:n.children.filter(c=>c.type==='JSXText').map(c=>c.value.trim()).filter(Boolean).join(' ')||attr(o,'aria-label',source)||attr(o,'title',source)||'(dynamic/icon)',disabled:attr(o,'disabled',source),onClick:attr(o,'onClick',source),onConfirm:attr(o,'onConfirm',source),href:attr(o,'href',source)});
  if(tag==='Form.Item')fields.push({line:n.loc.start.line,name:attr(o,'name',source),label:attr(o,'label',source),rules:attr(o,'rules',source),hidden:attr(o,'hidden',source)});
 });
 const requests=[...inventory.calls,...inventory.nativeFetchCalls].filter(x=>x.file===file).map(x=>{
  // Replace expressions before removing a query; '?' inside an expression is
  // not a URL query delimiter. Dynamic roots remain candidates, never proof.
  const expressions=[];const template=x.pathTemplate.replace(/\$\{([^}]*)\}/g,(_,e)=>{expressions.push(e);return '__PARAM__';}).split('?')[0];
  const regex=new RegExp('^'+template.split('__PARAM__').map(x=>x.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('.*')+'$');
  const mapping=reviewed.find(m=>m.file===file&&m.expression===x.expression);
  const candidates=mapping?.routes??registered.routes.filter(r=>!r.fallback&&(x.method==='DYNAMIC'||x.method===r.method)&&regex.test(r.path.replace(/\{[^}]+\}/g,'__VALUE__'))).map(r=>r.method+' '+r.path);
  return {line:x.line,method:x.method,pathTemplate:x.pathTemplate,expression:x.expression,arguments:x.argumentExpressions,match:mapping?'source-reviewed':candidates.length===0?'unresolved':expressions.length||x.method==='DYNAMIC'?'dynamic-candidate':'literal-match',review:mapping?.review,candidates};
 });
 fileRecords.push({file,dependencies,buttons,fields,requests});
}
const report={release:registered.release,scope:'Static page/component/button/form/request inventory only. Dynamic candidate matches and UI visibility are NOT runtime authorization or business acceptance.',completeProductAcceptance:false,counts:{pages:pages.length,files:fileRecords.length,buttons:fileRecords.reduce((s,x)=>s+x.buttons.length,0),formItems:fileRecords.reduce((s,x)=>s+x.fields.length,0),requests:fileRecords.reduce((s,x)=>s+x.requests.length,0)},unresolvedRoutes,pages,files:fileRecords};
const out=path.join(root,'docs/backend/acceptance/R16-ui-contract-audit.json');await writeFile(out,JSON.stringify(report,null,2)+'\n');
const unresolved=fileRecords.flatMap(x=>x.requests.filter(r=>r.match==='unresolved').map(r=>({file:x.file,...r})));
console.log(JSON.stringify({...report.counts,unresolvedRoutes:unresolvedRoutes.length,unresolvedRequests:unresolved.length}));
await writeFile(path.join(root,'backend/.local/r16-ui-unresolved.json'),JSON.stringify(unresolved,null,2)+'\n');
