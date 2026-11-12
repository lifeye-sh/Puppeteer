// Render URL ASTs instead of removing the first/last source characters: a
// concatenation does not end with the quote which begins its first operand.
export function requestPath(node,source,group){
 const slice=n=>source.slice(n.start,n.end);
 const render=n=>{
  if(!n)return '${missing}';
  if(n.type==='StringLiteral')return n.value;
  if(n.type==='TemplateLiteral')return n.quasis.map((q,i)=>(q.value.cooked??q.value.raw)+(i<n.expressions.length?'${'+slice(n.expressions[i])+'}':'')).join('');
  if(n.type==='BinaryExpression'&&n.operator==='+')return render(n.left)+render(n.right);
  if(n.type==='ParenthesizedExpression'||n.type==='TSAsExpression')return render(n.expression);
  return '${'+slice(n)+'}';
 };
 let value=render(node);
 if(group)value=value.replaceAll('${this.group}',group);
 value=value.replaceAll('${baseUrl()}','/api').replaceAll('${baseWebSocketUrl()}','/api').replaceAll('${fileSystemApi.group}','access/filesystem');
 value=value.replace(/^\/\//,'/');
 return /^\/api(?:\/|\$\{|$)/.test(value)?value:value.startsWith('/')?'/api'+value:value;
}
