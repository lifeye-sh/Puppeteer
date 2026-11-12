import {useState} from 'react';
import {Alert,Button,Space,Typography} from 'antd';

export default function BackendLogoInput({value,onChange}:{value?:string;onChange?:(value:string)=>void}){
 const [busy,setBusy]=useState(false),[error,setError]=useState('');
 const upload=async(file:File)=>{
  setError('');if(file.size>1024*1024){setError('文件不能超过 1 MiB');return}
  setBusy(true);try{
   const response=await fetch('/api/admin/branding/logo',{method:'POST',headers:{'Content-Type':file.type||'application/octet-stream'},body:file});
   const data=await response.json() as {id?:string;message?:string};
   if(!response.ok||!data.id)throw Error(data.message??'上传失败');onChange?.(data.id);
  }catch(e){setError((e as Error).message)}finally{setBusy(false)}
 };
 return <Space orientation="vertical" size={12}>
  {value?<img src={`/api/admin/branding/logo/${encodeURIComponent(value)}`} alt="待保存的系统 Logo" style={{width:64,height:64,objectFit:'contain'}}/>:<Typography.Text type="secondary">使用默认 Puppeteer 卡通 Logo</Typography.Text>}
  <Space wrap><label><Typography.Text>上传 Logo：</Typography.Text><input aria-label="上传系统 Logo" type="file" accept="image/png,image/jpeg" disabled={busy} onChange={e=>{const file=e.target.files?.[0];e.target.value='';if(file)void upload(file)}}/></label><Button disabled={!value||busy} onClick={()=>onChange?.('')}>恢复默认</Button></Space>
  {busy&&<Typography.Text>正在处理图片…</Typography.Text>}
  {error&&<Alert type="error" title={error}/>}
  <Typography.Text type="secondary">PNG / JPEG，不超过 1 MiB、1024×1024。上传后点击保存配置才会生效。</Typography.Text>
 </Space>;
}
