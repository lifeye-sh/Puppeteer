import {Input} from 'antd';

export default function BackendLDAPMappingInput({value,onChange}:{value?:string;onChange?:(value:string)=>void}){
 let mapping:Record<string,string>={};try{mapping=JSON.parse(value??'{}')}catch{/* Form validation reports invalid JSON. */}
 return <div className="nt-mail-fields">{[['username','登录账号属性','uid 或 sAMAccountName'],['nickname','昵称属性（可选）','cn 或 displayName'],['mail','邮箱属性（可选）','mail']].map(([key,label,placeholder])=><label key={key}><span>{label}</span><Input aria-label={label} value={mapping[key]??''} maxLength={64} placeholder={placeholder} onChange={e=>onChange?.(JSON.stringify({...mapping,[key]:e.target.value}))}/></label>)}</div>;
}
