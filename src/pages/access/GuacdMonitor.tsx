import {baseWebSocketUrl} from '@/api/core/requests';
import {type GuacamoleStatus} from '@/pages/access/guacamole/ErrorAlert';
import {GuacamoleRuntime} from '@/pages/access/guacamole/guacamole-runtime';
import RenderState from '@/pages/access/guacamole/RenderState';
import Guacamole from '@puppeteer/guacamole-common-js';
import {useEffect, useRef, useState} from 'react';
import {useSearchParams} from 'react-router-dom';

const GuacdMonitor = () => {
    const [searchParams] = useSearchParams();
    const sessionId = searchParams.get('sessionId') ?? '';
    const containerRef = useRef<HTMLDivElement>(null);
    const displayRef = useRef<HTMLDivElement>(null);
    const [state, setState] = useState<number>();
    const [status, setStatus] = useState<GuacamoleStatus>();
    const [tunnelState, setTunnelState] = useState<number>();

    useEffect(() => {
        const container = containerRef.current;
        const displayContainer = displayRef.current;
        if (!container || !displayContainer) {
            return;
        }
        const runtime = new GuacamoleRuntime({
            container,
            displayContainer,
            interactive: false,
            onStateChange: setState,
            onTunnelStateChange: setTunnelState,
            onError: setStatus,
        });
        runtime.connect({
            url: `${baseWebSocketUrl()}/admin/sessions/${sessionId}/graphics-monitor`,
            remoteResize: false,
            params: () => '',
        });
        return () => runtime.dispose();
    }, [sessionId]);

    return (
        <div ref={containerRef} className="relative flex h-dvh w-screen items-center justify-center overflow-hidden bg-[#1b1b1b]">
            <RenderState
                state={state}
                status={status}
                tunnelState={tunnelState ?? Guacamole.Tunnel.State.CONNECTING}
                overlay
            />
            <div ref={displayRef}/>
        </div>
    );
};

export default GuacdMonitor;
