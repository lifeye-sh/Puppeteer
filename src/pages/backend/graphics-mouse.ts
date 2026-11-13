// Keep only the latest motion within 33 ms. Button/key boundaries flush it first.
export function coalesceMouse<T>(send:(value:T)=>void, schedule:(flush:()=>void)=>()=>void=flush=>{
 const timer=setTimeout(flush,33);return ()=>clearTimeout(timer);
}) {
 let pending:T|undefined,cancel:(()=>void)|undefined;
 const flush=()=>{cancel?.();cancel=undefined;const value=pending;pending=undefined;if(value!==undefined)send(value)};
 return {
  move(value:T){pending=value;if(!cancel)cancel=schedule(flush)},
  flush,
  clear(){cancel?.();cancel=undefined;pending=undefined},
 };
}
