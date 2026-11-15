export type Dimension={state:string;scope:string;reason?:string};
export type Check={code:string;state:string;summary:string;remediation?:string;scope:string};
export type CheckTask={mode?:string;id:string;feature:string;node:string;revision:number;generation?:number;actor:string;state:string;stage:string;started:string;updated:string;expires:string;deadline:string;checks:Check[]};
export type NativeRDPCapability={supported:boolean;staticReady:boolean;profile:string;capacity:{limit:number;inUse:number;available:number};checks:Check[];observedAt:string};
export type FeatureStatus={capability?:NativeRDPCapability;id:string;label:string;category:string;section:string;desired:Dimension;applied:Dimension;dependencies:Dimension;service:Dimension;checkSupported:boolean;deployment?:{id:string;state:string;stage:string;actor:string;started:string;updated:string};lastCheck?:CheckTask};
export type ConfigurationStatus={version:number;revision:number;node:string;checkedAt:string;generation?:number;securityPending:boolean;nodes:{id:string;state:string;online:boolean;lastAppliedRevision:number}[];items:FeatureStatus[]};
export const operationStates:Record<string,string>={enabled:'期望开启',disabled:'期望关闭',configured:'已配置',pending:'等待应用',applying:'应用中',applied:'已应用',unknown:'未检测',checking:'检测中',ready:'预检通过',blocked:'未通过',stale:'证据过期',not_applicable:'不适用',running:'运行中',starting:'启动中',stopped:'已停止',degraded:'部分异常',failed:'失败',succeeded:'成功',cancelled:'已取消',interrupted:'已中断'};
export const operationStages:Record<string,string>={queued:'等待检测',backup_authorization:'复核操作权限',backup_write_authorization:'复核写入权限',backup_prepare:'登记清理信息',backup_prepare_file:'准备加密测试文件',backup_roundtrip:'远端写入与摘要回读',backup_cleanup:'清理测试对象',backup_version:'资源版本复核',application:'应用依赖检查',dependency:'外部依赖检查',capability:'运行能力检查',listener:'进程端口检查',host:'宿主预检',complete:'完成',cancelled:'已取消',interrupted:'超时或进程中断'};
// Polling failures must not continue to paint an old successful snapshot green.
export function displayedDimension(d:Dimension,fetchFailed:boolean):Dimension{
 return fetchFailed&&!['unknown','not_applicable'].includes(d.state)?{...d,state:'stale',reason:'状态刷新失败，以下为上次记录，请恢复连接后重新检查'}:d;
}
