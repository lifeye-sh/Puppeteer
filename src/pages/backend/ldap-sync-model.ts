export type LDAPSyncForm={password:string;totp?:string;limit?:number;departmentId?:string;disableMissing?:boolean};

// Preview and apply have distinct strict API bodies. Hidden/disabled preview
// fields remain in Ant Form state and must never be spread into apply requests.
export function ldapPreviewBody(form:LDAPSyncForm){
 return {password:form.password,totp:form.totp,limit:form.limit};
}
export function ldapApplyBody(form:LDAPSyncForm,preview:string,subjects:string[]){
 return {password:form.password,totp:form.totp,preview,subjects,departmentId:form.departmentId,disableMissing:form.disableMissing};
}
