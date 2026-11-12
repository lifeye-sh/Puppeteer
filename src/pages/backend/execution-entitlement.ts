type Capability={state:string;reason?:string|null};
export function executionEntitlement(features:Record<string,Capability>,key:string,label:string){
 const feature=features[key],allowed=feature?.state==='available';
 const reason=feature?.reason==='expired'?'许可已过期':feature?.reason==='invalid_license'?'许可无效或尚未生效':'当前许可未开放此功能';
 return {allowed,message:allowed?'':`${label}已暂停：${reason}。历史、配置与取消仍可使用。`};
}
