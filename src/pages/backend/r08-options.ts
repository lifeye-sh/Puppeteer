import requests from '@/api/core/requests';
export async function pagedOptions<T>(path:string):Promise<T[]>{
 const out:T[]=[];
 for(let current=1;current<=1000;current++){
  const page=await requests.get<{items:T[];total:number}>(`${path}?current=${current}&pageSize=100`,{errorMode:'local'});
  out.push(...page.items);if(out.length>=page.total)return out;
  if(!page.items.length)throw new Error('选项列表分页发生变化，请刷新后重试');
 }
 throw new Error('选项过多，请缩小资源或用户范围');
}
