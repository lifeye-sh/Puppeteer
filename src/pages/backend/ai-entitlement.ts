type Feature={state:string;reason?:string|null};
// ai.chat is the historical workspace; inference has an independent live gate.
export function aiExecutionState(features:Record<string,Feature>){
 const v=features['ai.inference'];
 const allowed=v?.state==='available';
 const reason=v?.reason==='expired'?'AI 许可已过期':v?.reason==='invalid_license'?'AI 许可无效或尚未生效':'当前许可未开放 AI 执行';
 return {allowed,message:allowed?'':reason+'。可继续查看历史、停止请求或清理对话。'};
}
