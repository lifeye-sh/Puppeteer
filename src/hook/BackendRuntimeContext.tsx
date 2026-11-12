import {useBackendBranding} from './useBackendBranding';
import {createContext, useContext, useEffect, useRef, type ReactNode} from 'react';
import {useQuery, useQueryClient} from '@tanstack/react-query';
import {Navigate, useLocation} from 'react-router-dom';
import {Button, Result, Spin} from 'antd';
import requests from '@/api/core/requests';
import accountApi, {type AccountInfo} from '@/api/account-api';
import {clearCurrentUser} from '@/utils/permission';

import {usesPuppeteerBackend} from '@/lib/backend-mode';
export const isNewBackend = usesPuppeteerBackend(import.meta.env.MODE, import.meta.env.VITE_BACKEND_MODE);
type Feature = {state: 'available' | 'not_implemented' | 'not_configured' | 'not_entitled' | 'unavailable'; reason: string | null};
export interface BackendRuntime {
    backendId: string; instanceId: string; release: string; contractVersion: number;
    schemaVersion: number; capabilitiesRevision: number; round: string; environment: string;
}
export interface BackendCapabilities {protocols?:Record<string,Record<string,boolean>>;schemaVersion: number; revision: number; backendId: string; features: Record<string, Feature>}
interface BackendContext {runtime: BackendRuntime; capabilities: BackendCapabilities; account: AccountInfo}
const Context = createContext<BackendContext | null>(null);

export function BackendRuntimeProvider({children}: {children: ReactNode}) {
    const {pathname,search} = useLocation();
    const branding=useBackendBranding();
    const publicPage = ['/login','/setup','/oidc/callback','/wechat-work/callback'].includes(pathname);
    const queryClient = useQueryClient();
    const previousInstance = useRef(sessionStorage.getItem('nt-new-instance'));
    const previousPermissions = useRef('');
    const query = useQuery({
        // Runtime and identity belong to the login session, not to each menu page.
        queryKey: ['backend-runtime'],
        enabled: !publicPage,
        retry: false,
        staleTime: 0,
        refetchOnMount: 'always',
        refetchInterval: 5_000,
        queryFn: async () => {
            // Authenticate first so a setup/login redirect does not race several requests.
            const runtime = await requests.get<BackendRuntime>('/system/runtime', {errorMode: 'local'});
            if (runtime.backendId !== 'nt-go' || runtime.contractVersion !== 1) {
                throw new Error('后端身份或契约版本不匹配，请检查 BACKEND_API_TARGET。');
            }
            const capabilities = await requests.get<BackendCapabilities>('/system/capabilities', {errorMode: 'local'});
            if (capabilities.backendId !== runtime.backendId || capabilities.schemaVersion !== 1
                || capabilities.revision !== runtime.capabilitiesRevision) {
                throw new Error('后端能力版本不一致，请刷新后重试。');
            }
            const account = await accountApi.getUserInfo();
            return {runtime, capabilities, account};
        },
    });
    useEffect(() => {
        document.title = `${branding.data?.name??'Puppeteer'} · ${query.data?.runtime.round??''}`;
        if (publicPage) clearCurrentUser();
        const instance = query.data?.runtime.instanceId;
        if (!instance) return;
        if (previousInstance.current && previousInstance.current !== instance) {
            clearCurrentUser();
            queryClient.clear();
            sessionStorage.setItem('nt-new-instance', instance);
            // A new instance may reuse a database snapshot. Revoke this browser's session first.
            void accountApi.logout().finally(() => window.location.replace('/login'));
            return;
        }
        previousInstance.current = instance;
        sessionStorage.setItem('nt-new-instance', instance);
    }, [publicPage, query.data, queryClient, branding.data?.name]);
    useEffect(() => {
        if (!query.data) return;
        const signature = `${query.data.account.id}:${query.data.account.actions?.join(',')}`;
        if (previousPermissions.current && previousPermissions.current !== signature) {
            queryClient.removeQueries({predicate:q=>q.queryKey[0]!=='backend-runtime'});
        }
        previousPermissions.current = signature;
    }, [query.data, queryClient]);
    if (publicPage) return <>{children}</>;
    if (query.isPending) return <Spin fullscreen description="正在验证新后端身份…"/>;
    if (query.isError) {
        const status = (query.error as {status?: number}).status;
        if (status === 418) return <Navigate to="/setup" replace/>;
        if (status === 401) return <Navigate to={pathname==='/website-login'?`/login?return=${encodeURIComponent(pathname+search)}`:'/login'} replace/>;
        return <Result status="error" title="新后端暂不可用" subTitle={query.error.message}
            extra={<Button onClick={() => void query.refetch()}>重试连接</Button>}/>;
    }
    return <Context.Provider value={query.data}>{children}</Context.Provider>;
}

export function useBackendRuntime() {
    const value = useContext(Context);
    if (!value) throw new Error('Backend runtime context is unavailable');
    return value;
}

export function CapabilityGate({feature, children}: {feature: string; children: ReactNode}) {
    const {capabilities} = useBackendRuntime();
    if (capabilities.features[feature]?.state !== 'available') return <UnavailableBackendPage/>;
    return <>{children}</>;
}
export function PermissionGate({action, children}: {action:string;children:ReactNode}) {
    const {account}=useBackendRuntime();
    if (!account.actions?.includes(action)) return <Result status="403" title="没有此页面的访问权限" subTitle="菜单、页面和后端 API 使用同一权限规则。" extra={<Button href="/">返回首页</Button>}/>;
    return <>{children}</>;
}

export function UnavailableBackendPage() {
    return <Result status="info" title="此功能本轮尚未开放"
        subTitle="当前后端尚未开放此能力，或所需服务尚未配置。请从首页使用已开放的功能。"
        extra={<Button type="primary" href="/">返回可用首页</Button>}/>;
}
