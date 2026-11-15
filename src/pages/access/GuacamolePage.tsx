import {baseWebSocketUrl} from '@/api/core/requests';
import {useAccessSessionMutation} from '@/pages/access/hooks/use-access-session';
import {GuacamoleRuntime} from '@/pages/access/guacamole/guacamole-runtime';
import {type GuacamoleStatus} from '@/pages/access/guacamole/ErrorAlert';
import RenderState from '@/pages/access/guacamole/RenderState';
import useWindowFocus from '@/pages/access/hooks/use-window-focus';
import {maybe} from '@/utils/maybe';
import strings from '@/utils/strings';
import Guacamole from '@puppeteer/guacamole-common-js';
import qs from 'qs';
import {useEffect, useRef, useState} from 'react';
import {useSearchParams} from 'react-router-dom';

const GuacamolePage = () => {
    const [searchParams] = useSearchParams();
    const sharerToken = maybe(searchParams.get('sharerToken'), '');
    const sessionId = searchParams.get('sessionId') ?? '';
    const sessionMutation = useAccessSessionMutation({type: 'shared', sessionId, sharerToken});
    const containerRef = useRef<HTMLDivElement>(null);
    const displayRef = useRef<HTMLDivElement>(null);
    const runtimeRef = useRef<GuacamoleRuntime>(null);
    const [state, setState] = useState<number>();
    const [status, setStatus] = useState<GuacamoleStatus>();
    const [tunnelState, setTunnelState] = useState<number>();
    const windowFocused = useWindowFocus();

    useEffect(() => {
        if (windowFocused) {
            runtimeRef.current?.focus();
        } else {
            runtimeRef.current?.resetKeyboard();
        }
    }, [windowFocused]);

    useEffect(() => {
        const container = containerRef.current;
        const displayContainer = displayRef.current;
        if (!container || !displayContainer) {
            return;
        }
        let cancelled = false;
        const runtime = new GuacamoleRuntime({
            container,
            displayContainer,
            onStateChange: setState,
            onTunnelStateChange: setTunnelState,
            onError: setStatus,
        });
        runtimeRef.current = runtime;

        sessionMutation.mutateAsync().then((session) => {
            if (cancelled) {
                return;
            }
            document.title = session.assetName;
            runtime.connect({
                url: `${baseWebSocketUrl()}/access/graphics`,
                fixedSize: session.width > 0 && session.height > 0,
                remoteResize: true,
                params: (size) => {
                    const params: Record<string, string | number> = {
                        width: session.width > 0 ? session.width : size.width,
                        height: session.height > 0 ? session.height : size.height,
                        dpi: 96,
                        sessionId: session.id,
                    };
                    if (strings.hasText(sharerToken)) {
                        params.sharerToken = sharerToken;
                    }
                    return qs.stringify(params);
                },
            });
        }).catch((error) => {
            if (!cancelled) {
                setStatus({code: error?.code, message: error?.message});
            }
        });

        return () => {
            cancelled = true;
            runtimeRef.current = null;
            runtime.dispose();
        };
    }, [sessionId, sharerToken]);

    return (
        <div ref={containerRef} className="relative flex h-dvh w-screen items-center justify-center overflow-hidden bg-[#1b1b1b]">
            <RenderState
                state={state}
                status={status}
                tunnelState={tunnelState ?? Guacamole.Tunnel.State.CONNECTING}
                overlay
            />
            <div ref={displayRef} className="flex h-full w-full items-center justify-center"/>
        </div>
    );
};

export default GuacamolePage;
