import {Modal, Tabs, theme, type ModalProps} from 'antd';
import {useEffect, useRef, useState, type CSSProperties, type ReactNode} from 'react';
import './backend-forms.css';

/** Shared editor shell. The body scrolls while the title and actions stay visible. */
function BackendFormModal(props:ModalProps){
 const {token}=theme.useToken();
 const style={...props.style,'--editor-bg':token.colorBgContainer,'--editor-soft':token.colorFillAlter,'--editor-line':token.colorBorderSecondary,'--editor-muted':token.colorTextSecondary} as CSSProperties;
 return <Modal centered width={800} okText="保存" cancelText="取消" {...props} style={style} className={`nt-form-modal ${props.className||''}`} styles={{header:{marginBottom:0,padding:'22px 28px 18px'},body:{padding:'22px 28px',maxHeight:'calc(100dvh - 210px)',overflowY:'auto'},footer:{marginTop:0,padding:'16px 28px'},...props.styles}} onOk={async event=>{try{await props.onOk?.(event)}catch(error){if(!error||typeof error!=='object'||!('errorFields' in error))throw error}}}/>;
}

type Section={key:string;label:string;description?:string;children:ReactNode};
export function FormTabs({sections}:{sections:Section[]}){
 const [active,setActive]=useState(sections[0].key);const root=useRef<HTMLDivElement>(null);
 useEffect(()=>{
  const element=root.current;if(!element)return;
  // All panels remain mounted, so switching tabs preserves values and validation.
  const observer=new MutationObserver(records=>{
   if(!records.some(r=>r.type==='attributes'&&(r.target as Element).classList.contains('ant-form-item-has-error')))return;
   const field=element.querySelector('.ant-form-item-has-error');
   const panel=field?.closest<HTMLElement>('[data-editor-section]');
   if(panel?.dataset.editorSection){setActive(panel.dataset.editorSection);requestAnimationFrame(()=>field?.scrollIntoView({block:'nearest',behavior:'smooth'}))}
  });
  observer.observe(element,{subtree:true,attributes:true,attributeFilter:['class']});
  return ()=>observer.disconnect();
 },[]);
 return <div ref={root} className="nt-form-tabs"><Tabs activeKey={active} onChange={setActive} items={sections.map(s=>({key:s.key,label:s.label,forceRender:true,children:<div data-editor-section={s.key}><p className="nt-form-section-note">{s.description}</p><div className="nt-form-grid">{s.children}</div></div>}))}/></div>;
}

export default Object.assign(BackendFormModal,{info:Modal.info,confirm:Modal.confirm,warning:Modal.warning,error:Modal.error,success:Modal.success});
