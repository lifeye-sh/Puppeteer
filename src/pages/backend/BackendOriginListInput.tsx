import {Input} from 'antd';
import {useEffect,useRef,useState} from 'react';
function display(value:string){try{const list=JSON.parse(value);if(Array.isArray(list)&&list.every(v=>typeof v==='string'))return list.join('\n')}catch{}return value}
export default function BackendOriginListInput({value='[]',onChange,id}:{value?:string;onChange?:(v:string)=>void;id?:string}){
 const [text,setText]=useState(()=>display(value));const emitted=useRef(value);
 useEffect(()=>{if(value!==emitted.current){emitted.current=value;setText(display(value))}},[value]);
 return <Input.TextArea id={id} rows={3} value={text} maxLength={32768} placeholder="https://backend.localhost:5174" onChange={e=>{setText(e.target.value);emitted.current=JSON.stringify(e.target.value.split(/\r?\n/).map(v=>v.trim()).filter(Boolean));onChange?.(emitted.current)}}/>;
}
