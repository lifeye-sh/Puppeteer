import {Alert} from 'antd';
import {useSearchParams} from 'react-router-dom';
import BackendRecordingPlayer from './BackendRecordingPlayer';
export default function BackendPlaybackPage(){
 const [params]=useSearchParams(),id=params.get('sessionId')??'';
 return <section className="nt-page"><h2>录屏回放</h2>{/^[0-9a-f-]{36}$/i.test(id)?<BackendRecordingPlayer key={id} sessionId={id}/>:<Alert type="info" title="请从访问记录选择会话录屏"/>}</section>;
}
