import { baseWebSocketUrl } from "@/api/core/requests";
import qs from "qs";
import { useEffect,useRef,useState } from 'react';
import portalApi,{ ExportSession } from "@/api/portal-api";
import FileSystemPage from "@/pages/access/FileSystemPage";
import ControlButtons from "@/pages/access/guacamole/ControlButtons";
import { GuacamoleStatus } from "@/pages/access/guacamole/ErrorAlert";
import { GuacamoleRuntime } from "@/pages/access/guacamole/guacamole-runtime";
import { useAccessSessionMutation } from "@/pages/access/hooks/use-access-session";
import RenderState,{ GuacamoleState } from "@/pages/access/guacamole/RenderState";
import GuacClipboard from "@/pages/access/GuacClipboard";
import GuacdRequiredParameters from "@/pages/access/GuacdRequiredParameters";
import useWindowFocus from "@/pages/access/hooks/use-window-focus";
import SessionSharerModal from "@/pages/access/SessionSharerModal";
import SessionWatermark from "@/pages/access/SessionWatermark";
import MultiFactorAuthentication from "@/pages/account/MultiFactorAuthentication";
import { requestFullScreen } from "@/utils/utils";
import Guacamole from '@puppeteer/guacamole-common-js';
import { useMutation } from "@tanstack/react-query";
import { App } from "antd";
import copy from "copy-to-clipboard";
import { useTranslation } from "react-i18next";

interface Props {
    assetId: string;
    standalone?: boolean;
    active?: boolean;
}

const AccessGuacamole = ({assetId, standalone = false, active: activeProp}: Props) => {

    let [requiredOpen, setRequiredOpen] = useState<boolean>(false);
    let [requiredParameters, setRequiredParameters] = useState<string[]>([]);

    let {t} = useTranslation();
    let {message} = App.useApp();

    let [tiger, setTiger] = useState(0);
    const terminalRef = useRef<HTMLDivElement>(null);
    const containerRef = useRef<HTMLDivElement>(null);
    const runtimeRef = useRef<GuacamoleRuntime>(null);
    const mfaCheckingRef = useRef(false);

    let [state, setState] = useState<number>();
    let [status, setStatus] = useState<GuacamoleStatus>();
    let [tunnelState, setTunnelState] = useState<number>();

    let [session, setSession] = useState<ExportSession>();
    const sessionMutation = useAccessSessionMutation({type: 'asset', assetId});
    const accessRequireMFAMutation = useMutation({mutationFn: () => portalApi.getAccessRequireMFA()});
    const [modals, setModals] = useState({sharer: false, fs: false, clipboard: false});
    let [clipboardText, setClipboardText] = useState('');

    const active = activeProp ?? standalone;

    let [mfaOpen, setMfaOpen] = useState(false);

    let windowFocus = useWindowFocus();

    useEffect(() => {
        if (windowFocus && active) {
            handleWindowFocus(); // 你处理剪贴板的函数
            runtimeRef.current?.setActive(true);
            runtimeRef.current?.focus();
            return;
        }

        if (!windowFocus || !active) {
            runtimeRef.current?.setActive(false);
        }
    }, [windowFocus, active]);

    let sendRequiredMutation = useMutation({
        mutationFn: async (values: Record<string, unknown>) => {
            runtimeRef.current?.sendArgumentValues(values);
        },
        onSuccess: () => setRequiredOpen(false)
    });

    const handleClipboardReceived = (stream: Guacamole.InputStream, mimetype: string) => {
        if (/^text\//.test(mimetype)) {
            const reader = new Guacamole.StringReader(stream);
            let data = '';
            reader.ontext = (text: string) => data += text;
            reader.onend = () => {
                setClipboardText(data);
                copy(data);
                message.success(t('general.copy_success'));
            };
        } else {
            const reader = new Guacamole.BlobReader(stream, mimetype);
            reader.onend = () => reader.getBlob().text().then((text: string) => {
                setClipboardText(text);
                copy(text);
                message.success(t('general.copy_success'));
            });
        }
    };

    const handleWindowFocus = () => {
        if (navigator.clipboard) {
            navigator.clipboard.readText().then(text => {
                sendClipboard({data: text, type: 'text/plain'});
            }).catch(console.error);
        }
    };

    const connect = async (securityToken?: string, expectedRuntime = runtimeRef.current) => {
        if (!expectedRuntime) {
            return;
        }
        let session: ExportSession;
        try {
            session = await sessionMutation.mutateAsync(securityToken);
            if (runtimeRef.current !== expectedRuntime) {
                return;
            }
            setSession(session);
            if (standalone && session.assetName) {
                document.title = session.assetName;
            }
        } catch (e) {
            console.error('create session err', e);
            return
        }

        const runtime = expectedRuntime;
        runtime.setHandlers({
            onStateChange: setState,
            onTunnelStateChange: setTunnelState,
            onError: setStatus,
            onRequired: (parameters) => {
                setRequiredParameters(parameters);
                setRequiredOpen(true);
            },
            onClipboard: (stream, mimetype) => {
                if (!session.strategy?.copy) {
                    message.info(t('general.clipboard_disabled'));
                    return;
                }
                handleClipboardReceived(stream, mimetype);
            },
        });
        runtime.connect({
            url: `${baseWebSocketUrl()}/access/graphics`,
            fixedSize: session.width > 0 && session.height > 0,
            remoteResize: true,
            params: (size) => qs.stringify({
                width: session.width > 0 ? session.width : size.width,
                height: session.height > 0 ? session.height : size.height,
                dpi: 96,
                sessionId: session.id,
            }),
        });
    }

    const connectWrap = async (expectedRuntime: GuacamoleRuntime) => {
        if (mfaCheckingRef.current) {
            return;
        }
        mfaCheckingRef.current = true;
        try {
            const required = await accessRequireMFAMutation.mutateAsync();
            if (runtimeRef.current !== expectedRuntime) {
                return;
            }
            if (required) {
                setMfaOpen(true);
            } else {
                connect(undefined, expectedRuntime);
            }
        } finally {
            mfaCheckingRef.current = false;
        }
    }

    useEffect(() => {
        const container = containerRef.current;
        const displayContainer = terminalRef.current;
        if (!container || !displayContainer) {
            return
        }
        const runtime = new GuacamoleRuntime({
            container,
            displayContainer,
            active,
            onStateChange: setState,
            onTunnelStateChange: setTunnelState,
            onError: setStatus,
        });
        runtimeRef.current = runtime;
        void connectWrap(runtime);
        return () => {
            runtimeRef.current = null;
            runtime.dispose();
        }
    }, [assetId, tiger]);

    const sendClipboard = (data: {data: string | Blob; type: string}) => {
        runtimeRef.current?.sendClipboard(data.data, data.type);
    }

    const sendCombinationKey = (keys: string[]) => {
        runtimeRef.current?.sendKeys(keys);
    }

    const fullScreen = () => {
        if (terminalRef.current) {
            requestFullScreen(terminalRef.current);
        }
        runtimeRef.current?.focus();
    }

    return (
        <div className={'h-full min-h-0 w-full'}
             ref={containerRef}
        >
            <RenderState
                state={state}
                status={status}
                tunnelState={tunnelState ?? Guacamole.Tunnel.State.CONNECTING}
                onReconnect={() => {
                    setStatus({});
                    setState(GuacamoleState.IDLE);
                    setTunnelState(Guacamole.Tunnel.State.CONNECTING);
                    setTiger(prevState => prevState + 1);
                }}
                overlay={true}
            />
            <div className={'relative flex items-center justify-center h-full w-full'}>
                <div className={'w-full flex items-center justify-center'}
                     ref={terminalRef}
                />
                <SessionWatermark watermark={session?.watermark}/>
            </div>

            <ControlButtons
                sessionId={session?.id}
                hasFileSystem={session?.fileSystem}
                onOpenFS={() => {
                    setModals({
                        ...modals,
                        fs: true
                    })
                }}
                onShare={() => {
                    setModals({
                        ...modals,
                        sharer: true
                    })
                }}
                onClipboard={() => {
                    setModals({
                        ...modals,
                        clipboard: true
                    })
                }}
                onFull={() => {
                    fullScreen();
                }}
                onSendKeys={(keys) => {
                    sendCombinationKey(keys);
                }}
            />

            <FileSystemPage fsId={session?.id ?? ''}
                            strategy={session?.strategy}
                            open={modals.fs}
                            mask={false}
                            maskClosable={false}
                            onClose={() => {
                                setModals({
                                    ...modals,
                                    fs: false
                                })
                            }}/>

            <SessionSharerModal sessionId={session?.id ?? ''} open={modals.sharer}
                                onClose={() => setModals({...modals, sharer: false})}/>
            <GuacClipboard clipboardText={clipboardText}
                           open={modals.clipboard}
                           handleOk={(text) => {
                               sendClipboard({
                                   'data': text,
                                   'type': 'text/plain'
                               });
                               setClipboardText(text);
                               runtimeRef.current?.focus();
                               setModals({
                                   ...modals,
                                   clipboard: false
                               })
                           }}
                           handleCancel={() => {
                               runtimeRef.current?.focus();
                               setModals({
                                   ...modals,
                                   clipboard: false
                               })
                           }}
            />

            <GuacdRequiredParameters
                open={requiredOpen}
                parameters={requiredParameters}
                confirmLoading={sendRequiredMutation.isPending}
                handleOk={sendRequiredMutation.mutate}
                handleCancel={() => {
                    setRequiredOpen(false);
                    runtimeRef.current?.disconnect();
                }}
            />

            <MultiFactorAuthentication
                open={mfaOpen}
                handleOk={async (securityToken) => {
                    setMfaOpen(false);
                    connect(securityToken);
                }}
                handleCancel={() => setMfaOpen(false)}
            />
        </div>
    );
};

export default AccessGuacamole;
