import {InputNumber,Switch} from 'antd';

const defaults={minLength:12,minCharacterType:2,mustNotContainUsername:true,mustNotBePalindrome:false,mustNotWeak:true};
export default function BackendPasswordPolicyInput({value,onChange,disabled}:{value?:string;onChange?:(value:string)=>void;disabled?:boolean}){
 let policy=defaults;try{policy={...defaults,...JSON.parse(value||'{}')}}catch{/* Server rejects invalid policy values. */}
 const set=(key:keyof typeof defaults,next:number|boolean|null)=>onChange?.(JSON.stringify({...policy,[key]:next}));
 return <div className="nt-password-policy">
  <div className="nt-password-policy-numbers">
   <label><span>最少字符数</span><InputNumber aria-label="最少字符数" min={8} max={128} precision={0} value={policy.minLength} disabled={disabled} onChange={v=>set('minLength',v)}/></label>
   <label><span>最少字符种类</span><InputNumber aria-label="最少字符种类" min={1} max={3} precision={0} value={policy.minCharacterType} disabled={disabled} onChange={v=>set('minCharacterType',v)}/></label>
  </div>
  <p>字母、数字、其他字符，共三类；大小写字母属于同一类。{disabled?'当前使用推荐规则，自定义规则未启用。':''}</p>
  <div className="nt-password-policy-checks">
   <label><span>禁止包含账号</span><Switch aria-label="禁止包含账号" checked={policy.mustNotContainUsername} disabled={disabled} onChange={v=>set('mustNotContainUsername',v)}/></label>
   <label><span>禁止回文</span><Switch aria-label="禁止回文" checked={policy.mustNotBePalindrome} disabled={disabled} onChange={v=>set('mustNotBePalindrome',v)}/></label>
   <label><span>检查常见弱密码</span><Switch aria-label="检查常见弱密码" checked={policy.mustNotWeak} disabled={disabled} onChange={v=>set('mustNotWeak',v)}/></label>
  </div>
 </div>
}
