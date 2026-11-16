export type GrantChoice = {value:string;label:string};
export type GrantBatchForm = {
 users?:GrantChoice[];departments?:GrantChoice[];resources?:GrantChoice[];
 strategyId?:string;commandFilterId?:string;validFrom?:string;expiresAt?:string;
};
export const MAX_BATCH_GRANTS = 500;
const ids = (choices:GrantChoice[] = []) => [...new Set(choices.map(choice=>choice.value.toLowerCase()))];
export function grantBatchCount(values:GrantBatchForm) {
 return (ids(values.users).length+ids(values.departments).length)*ids(values.resources).length;
}
export function grantBatchRequest(values:GrantBatchForm) {
 const userIds=ids(values.users),departmentIds=ids(values.departments),resourceIds=ids(values.resources);
 const count=grantBatchCount(values);
 if(!count)throw Error('请至少选择一个用户或部门，以及一个资产或分组');
 if([userIds,departmentIds,resourceIds].some(list=>list.length>100))throw Error('每类最多选择 100 个对象');
 if(count>MAX_BATCH_GRANTS)throw Error(`本次包含 ${count} 条授权，单批最多 ${MAX_BATCH_GRANTS} 条`);
 const date = (value:string|undefined) => {
  if(!value)return undefined;
  const parsed=new Date(value);
  if(!Number.isFinite(parsed.getTime()))throw Error('授权时间格式无效');
  return parsed.toISOString();
 };
 const validFrom=date(values.validFrom),expiresAt=date(values.expiresAt);
 if(expiresAt&&new Date(expiresAt).getTime()<=(validFrom?new Date(validFrom).getTime():Date.now()))throw Error('到期时间必须晚于开始时间');
 return {userIds,departmentIds,resourceIds,strategyId:values.strategyId||'',commandFilterId:values.commandFilterId||'',validFrom,expiresAt:expiresAt??null};
}
