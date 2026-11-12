const BackendTasks = lazy(() => import('@/pages/backend/BackendTasks'));
const BackendRotation = lazy(() => import('@/pages/backend/BackendRotation'));
const BackendSnippets = lazy(() => import('@/pages/backend/BackendSnippets'));
const BackendPolicies = lazy(() => import('@/pages/backend/BackendPolicies'));
const BackendApprovals = lazy(() => import('@/pages/backend/BackendApprovals'));

const BackendWebsites = lazy(() => import('@/pages/backend/BackendWebsites'));
const BackendCertificates = lazy(() => import('@/pages/backend/BackendCertificates'));
const BackendWebsiteLogin = lazy(() => import('@/pages/backend/BackendWebsiteLogin'));
const BackendWebsiteSettings = lazy(() => import('@/pages/backend/BackendWebsiteSettings'));
import BackendConnectivity, {BackendMonitoring,BackendDiagnostics} from '@/pages/backend/BackendConnectivity';
import BackendSSHJumps from '@/pages/backend/BackendSSHJumps';
import BackendAgentGateways from '@/pages/backend/BackendAgentGateways';
import '@/App.css';
import {lazy, Suspense, useEffect} from "react";
import {createBrowserRouter, Outlet, RouterProvider} from "react-router-dom";
import '@/react-i18next/i18n'
import ManagerLayout from '@/layout/ManagerLayout';
import AccessPage from "@/pages/access/AccessPage";
import eventEmitter from "@/api/core/event-emitter";
import {baseUrl} from "@/api/core/requests";
import {useTranslation} from "react-i18next";
import {App as AntdApp, Spin} from "antd";
import {LicenseProvider} from "@/hook/LicenseContext";
import {BackendRuntimeProvider, CapabilityGate, PermissionGate, isNewBackend, UnavailableBackendPage} from '@/hook/BackendRuntimeContext';
const BackendPlaybackPage = lazy(() => import('@/pages/backend/BackendPlaybackPage'));
const BackendAIPage = lazy(() => import('@/pages/backend/BackendAIPage'));
const BackendOAuthClients = lazy(() => import('@/pages/backend/BackendOAuthClients'));
const BackendFederationCallback = lazy(()=>import('@/pages/backend/BackendFederationCallback'));
const BackendOAuthConsent = lazy(() => import('@/pages/backend/BackendOAuthConsent'));
const BackendSettings = lazy(() => import('@/pages/backend/BackendSettings'));
const BackendBackupPage = lazy(() => import('@/pages/backend/BackendBackupPage'));
const BackendStoragePage = lazy(() => import('@/pages/backend/BackendStoragePage'));
const BackendHomePage = lazy(() => import('@/pages/backend/BackendHomePage'));
const BackendWorkbench = lazy(() => import('@/pages/backend/BackendHomePage').then(module=>({default:module.BackendWorkbench})));
const BackendLayout = lazy(() => import('@/pages/backend/BackendLayout'));
const BackendLoginPage = lazy(() => import('@/pages/backend/BackendLoginPage'));
const BackendObserverPage = lazy(() => import('@/pages/backend/BackendObserverPage'));
const BackendTerminalPage = lazy(() => import('@/pages/backend/BackendTerminalPage'));
const BackendSessionsPage = lazy(() => import('@/pages/backend/BackendSessionsPage'));
const BackendFileLogs = lazy(() => import('@/pages/backend/BackendFileLogs'));
const BackendResourcesPage = lazy(() => import('@/pages/backend/BackendResourcesPage'));
const BackendDatabaseAssets = lazy(() => import('@/pages/backend/BackendDatabaseAssets'));
const BackendDatabaseWorkOrders = lazy(() => import('@/pages/backend/BackendDatabaseWorkOrders'));
const BackendDatabaseLogs = lazy(() => import('@/pages/backend/BackendDatabaseLogs'));
const BackendIdentityPage = lazy(() => import('@/pages/backend/BackendIdentityPage'));

import TerminalPage from "@/pages/access/TerminalPage";
import GuacamolePage from "@/pages/access/GuacamolePage";

const AuthorisedAssetPage = lazy(() => import("@/pages/authorised/AuthorisedAssetPage"));
const AuthorisedWebsitePage = lazy(() => import("@/pages/authorised/AuthorisedWebsitePage"));
const AuthorisedAssetPost = lazy(() => import("@/pages/authorised/AuthorisedAssetPost"));
const AuthorisedWebsitePost = lazy(() => import("@/pages/authorised/AuthorisedWebsitePost"));
const LoginPage = lazy(() => import("@/pages/account/LoginPage"));
const WechatWorkCallback = lazy(() => import("@/pages/account/WechatWorkCallback"));
const OidcCallback = lazy(() => import("@/pages/account/OidcCallback"));
const OidcServerConsent = lazy(() => import("@/pages/account/OidcServerConsent"));
const OAuthConsent = lazy(() => import("@/pages/account/OAuthConsent"));
const AssetAuthorizationPage = lazy(() => import("@/pages/access/AssetAuthorizationPage"));
const StandaloneAssetAccessPage = lazy(() => import("@/pages/access/StandaloneAssetAccessPage"));
const UserPage = lazy(() => import("@/pages/identity/UserPage"));
const UserDetailPage = lazy(() => import("@/pages/identity/UserDetailPage"));
const SettingPage = lazy(() => import("@/pages/sysconf/SettingPage"));
const InfoPage = lazy(() => import("@/pages/account/InfoPage"));
const DepartmentPage = lazy(() => import("@/pages/identity/DepartmentPage"));
const DepartmentDetail = lazy(() => import("@/pages/identity/DepartmentDetail"));
const RolePage = lazy(() => import("@/pages/identity/RolePage"));
const RoleDetail = lazy(() => import("@/pages/identity/RoleDetail"));
const LoginLockedPage = lazy(() => import("@/pages/identity/LoginLockedPage"));
const AccessPolicyPage = lazy(() => import("@/pages/identity/AccessPolicyPage"));
const AccessPolicyPostPage = lazy(() => import("@/pages/identity/AccessPolicyPostPage"));
const AccessPolicyDetailPage = lazy(() => import("@/pages/identity/AccessPolicyDetailPage"));
const AccessPolicyRulePostPage = lazy(() => import("@/pages/identity/AccessPolicyRulePostPage"));
const OidcClientPage = lazy(() => import("@/pages/identity/OidcClientPage"));
const AssetsPage = lazy(() => import("@/pages/assets/AssetPage"));
const DatabaseAssetPage = lazy(() => import("@/pages/assets/DatabaseAssetPage"));
const CredentialPage = lazy(() => import("@/pages/assets/CredentialPage"));
const CredentialRotationPage = lazy(() => import("@/pages/assets/CredentialRotationPage"));
const CertificatePage = lazy(() => import("@/pages/assets/CertificatePage"));
const SnippetPage = lazy(() => import("@/pages/assets/SnippetPage"));
const StrategyPage = lazy(() => import("@/pages/authorised/StrategyPage"));
const CommandFilterPage = lazy(() => import("@/pages/authorised/CommandFilterPage"));
const CommandFilterDetail = lazy(() => import("@/pages/authorised/CommandFilterDetail"));
const AuthorisedDatabaseAssetPage = lazy(() => import("@/pages/authorised/AuthorisedDatabaseAssetPage"));
const AuthorisedDatabaseAssetPost = lazy(() => import("@/pages/authorised/AuthorisedDatabaseAssetPost"));
const ScheduledTaskPage = lazy(() => import("@/pages/sysops/ScheduledTaskPage"));
const ToolsPage = lazy(() => import("@/pages/sysops/ToolsPage"));
const LoginLogPage = lazy(() => import("@/pages/audit/LoginLogPage"));
const OperationLogPage = lazy(() => import("@/pages/audit/OperationLogPage"));
const OfflineSessionPage = lazy(() => import("@/pages/audit/OfflineSessionPage"));
const ExecCommandLogPage = lazy(() => import("@/pages/audit/ExecCommandLogPage"));
const OnlineSessionPage = lazy(() => import("@/pages/audit/OnlineSessionPage"));
const TerminalPlayback = lazy(() => import("@/pages/access/TerminalPlayback"));
const TerminalMonitor = lazy(() => import("@/pages/access/TerminalMonitor"));
const GuacdPlayback = lazy(() => import("@/pages/access/GuacdPlayback"));
const GuacdMonitor = lazy(() => import("@/pages/access/GuacdMonitor"));
const VideoPlayback = lazy(() => import("@/pages/access/VideoPlayback"));
const FileSystemLogPage = lazy(() => import("@/pages/audit/FileSystemLogPage"));
const AccessLogPage = lazy(() => import("@/pages/audit/AccessLogPage"));
const AccessLogStatsPage = lazy(() => import("@/pages/audit/AccessLogStatsPage"));
const DatabaseSQLLogPage = lazy(() => import("@/pages/dbproxy/DatabaseSQLLogPage"));
const SshGatewayPage = lazy(() => import("@/pages/gateway/SshGatewayPage"));
const AgentGatewayPage = lazy(() => import("@/pages/gateway/AgentGatewayPage"));
const GatewayGroupPage = lazy(() => import("@/pages/gateway/GatewayGroupPage"));
const NetworkProxyPage = lazy(() => import("@/pages/gateway/NetworkProxyPage"));
const ErrorPage = lazy(() => import("@/components/ErrorPage"));
const StoragePage = lazy(() => import("@/pages/assets/StoragePage"));
const WebsitePage = lazy(() => import("@/pages/assets/WebsitePage"));
const IPSetPage = lazy(() => import("@/pages/networkpolicy/IPSetPage"));
const BrowserPage = lazy(() => import("@/pages/access/BrowserPage"));
const FacadePage = lazy(() => import("@/pages/facade/AssetFacadePage.tsx"));
const WebsiteFacadePage = lazy(() => import("@/pages/facade/WebsiteFacadePage"));
const RedirectPage = lazy(() => import("@/layout/RedirectPage"));
const UserLayout = lazy(() => import("@/layout/UserLayout"));
const DashboardPage = lazy(() => import("@/pages/dashboard/DashboardPage"));
const AgentAccessPage = lazy(() => import("@/pages/agent/AgentAccessPage"));
const UserInfoPage = lazy(() => import("@/pages/facade/UserInfoPage"));
const SnippetUserPage = lazy(() => import("@/pages/facade/SnippetUserPage"));
const DatabaseAssetFacadePage = lazy(() => import("@/pages/facade/DatabaseAssetFacadePage"));
const SystemMonitorPage = lazy(() => import("@/pages/sysops/SystemMonitorPage"));
const SystemEventPage = lazy(() => import("@/pages/sysops/SystemEventPage"));
const SetupPage = lazy(() => import("@/pages/identity/SetupPage"));
const DatabaseWorkOrderPage = lazy(() => import("@/pages/dbproxy/DatabaseWorkOrderPage"));
const DatabaseWorkOrderUserPage = lazy(() => import("@/pages/facade/DatabaseWorkOrderUserPage"));
const AccessRequestPage = lazy(() => import("@/pages/accessrequest/AccessRequestPage"));
const AccessRequestUserPage = lazy(() => import("@/pages/facade/AccessRequestUserPage"));
const GlobalAIPage = lazy(() => import("@/pages/ai/GlobalAIPage"));
const AccessDeniedPage = lazy(() => import("@/pages/identity/AccessDeniedPage"));

const routes = [
    {path: "/setup", element: <SetupPage/>},
    {path: "/access", element: <AccessPage/>},
    {path: "/login", element: <LoginPage/>},
    {path: "/access-denied", element: <AccessDeniedPage/>},
    {path: "/wechat-work/callback", element: <WechatWorkCallback/>},
    {path: "/oidc/callback", element: <OidcCallback/>},
    {path: "/oidc/server/consent", element: <OidcServerConsent/>},
    {path: "/oauth/consent", element: <OAuthConsent/>},
    {path: "/asset-authorization", element: <AssetAuthorizationPage/>},
    {path: "/terminal-playback", element: <TerminalPlayback/>},
    {path: "/terminal-monitor", element: <TerminalMonitor/>},
    {path: "/graphics-playback", element: <GuacdPlayback/>},
    {path: "/graphics-monitor", element: <GuacdMonitor/>},
    {path: "/video-playback", element: <VideoPlayback/>},
    {path: "/terminal", element: <TerminalPage/>,},
    {path: "/standalone-access", element: <StandaloneAssetAccessPage/>,},
    {path: "/graphics", element: <GuacamolePage/>,},
    {path: "/browser", element: <BrowserPage/>,},
    {path: "/", element: <RedirectPage/>, errorElement: <ErrorPage/>,},
    {
        element: <UserLayout/>,
        children: [
            {path: "/x-asset", element: <FacadePage/>,},
            {path: "/x-database-asset", element: <DatabaseAssetFacadePage/>,},
            {path: "/x-website", element: <WebsiteFacadePage/>,},
            {path: "/x-snippet", element: <SnippetUserPage/>,},
            {path: "/x-info", element: <UserInfoPage/>,},
            {path: "/x-agent-access", element: <AgentAccessPage portal/>},
            {path: "/x-db-work-order", element: <DatabaseWorkOrderUserPage/>},
            {path: "/x-access-request", element: <AccessRequestUserPage/>},
        ]
    },
    {
        element: <ManagerLayout/>,
        children: [
            {path: "/dashboard", element: <DashboardPage/>},
            {path: "/ai", element: <GlobalAIPage/>},
            {path: "/user", element: <UserPage/>},
            {path: "/user/:userId", element: <UserDetailPage/>},
            {path: "/department", element: <DepartmentPage/>},
            {path: "/department/:departmentId", element: <DepartmentDetail/>},
            {path: "/login-locked", element: <LoginLockedPage/>},
            {path: "/access-policy", element: <AccessPolicyPage/>},
            {path: "/access-policy/new", element: <AccessPolicyPostPage/>},
            {path: "/access-policy/:groupId/rules/new", element: <AccessPolicyRulePostPage/>},
            {path: "/access-policy/:groupId", element: <AccessPolicyDetailPage/>},
            {path: "/role", element: <RolePage/>},
            {path: "/role/:roleId", element: <RoleDetail/>},
            {path: "/oidc-client", element: <OidcClientPage/>},
            {path: "/login-log", element: <LoginLogPage/>},
            {path: "/operation-log", element: <OperationLogPage/>},

            {path: "/asset", element: <AssetsPage/>},
            {path: "/database-asset", element: <DatabaseAssetPage/>},
            {path: "/credential", element: <CredentialPage/>},
            {path: "/credential-rotation", element: <CredentialRotationPage/>},
            {path: "/snippet", element: <SnippetPage/>},
            {path: "/storage", element: <StoragePage/>},
            {path: "/website", element: <WebsitePage/>},
            {path: "/ip-set", element: <IPSetPage/>},
            {path: "/certificate", element: <CertificatePage/>},
            {path: "/db-work-order", element: <DatabaseWorkOrderPage/>},
            {path: "/access-request", element: <AccessRequestPage/>},

            {path: "/strategy", element: <StrategyPage/>},
            {path: "/command-filter", element: <CommandFilterPage/>},
            {path: "/command-filter/:commandFilterId", element: <CommandFilterDetail/>},
            {path: "/authorised-asset", element: <AuthorisedAssetPage/>},
            {path: "/authorised-asset/post", element: <AuthorisedAssetPost/>},
            {path: "/authorised-website", element: <AuthorisedWebsitePage/>},
            {path: "/authorised-website/post", element: <AuthorisedWebsitePost/>},
            {path: "/authorised-database-asset", element: <AuthorisedDatabaseAssetPage/>},
            {path: "/authorised-database-asset/post", element: <AuthorisedDatabaseAssetPost/>},

            {path: "/scheduled-task", element: <ScheduledTaskPage/>},
            {path: "/tools", element: <ToolsPage/>},
            {path: "/monitoring", element: <SystemMonitorPage/>},
            {path: "/system-event", element: <SystemEventPage/>},

            {path: "/offline-session", element: <OfflineSessionPage/>},
            {path: "/exec-command-log", element: <ExecCommandLogPage/>},
            {path: "/online-session", element: <OnlineSessionPage/>},
            {path: "/filesystem-log", element: <FileSystemLogPage/>},
            {path: "/access-log", element: <AccessLogPage/>},
            {path: "/access-log-stats", element: <AccessLogStatsPage/>},
            {path: "/database-sql-log", element: <DatabaseSQLLogPage/>},

            {path: "/ssh-gateway", element: <SshGatewayPage/>},
            {path: "/agent-gateway", element: <AgentGatewayPage/>},
            {path: "/gateway-group", element: <GatewayGroupPage/>},
            {path: "/network-proxy", element: <NetworkProxyPage/>},

            {path: "/setting", element: <SettingPage/>},
            {path: "/info", element: <InfoPage/>},
            {path: "/agent-access", element: <AgentAccessPage/>},
        ],
    }
];

const BackendAccessStatistics = lazy(() => import('./pages/backend/BackendAccessStatistics'));
const backendRoutes = [
 {path:'/access-log-stats',element:<BackendAccessStatistics/>},
 {path:'/ai',element:<CapabilityGate feature="ai.chat"><BackendAIPage/></CapabilityGate>},
 ...['/oidc/callback','/wechat-work/callback'].map(path=>({path,element:<BackendFederationCallback/>})),
 {path:'/oidc-client',element:<CapabilityGate feature="identity.oidc-server"><PermissionGate action="identity.manage"><BackendOAuthClients/></PermissionGate></CapabilityGate>},
 {path:'/oidc/server/consent',element:<CapabilityGate feature="identity.oidc-server"><BackendOAuthConsent/></CapabilityGate>},
 {path:'/db-work-order',element:<PermissionGate action="database.approve"><BackendDatabaseWorkOrders/></PermissionGate>},
 {path:'/x-db-work-order',element:<BackendDatabaseWorkOrders portal/>},
 {path:'/database-sql-log',element:<PermissionGate action="database.audit"><BackendDatabaseLogs/></PermissionGate>},
 {path:'/authorised-database-asset',element:<PermissionGate action="grant.read"><BackendResourcesPage kind="grants"/></PermissionGate>},
 {path:'/database-asset',element:<PermissionGate action="database.read"><BackendDatabaseAssets/></PermissionGate>},
 {path:'/x-database-asset',element:<BackendDatabaseAssets portal/>},
 {path:'/credential-rotation',element:<PermissionGate action="credential.rotate"><BackendRotation/></PermissionGate>},
 {path:"/scheduled-task",element:<PermissionGate action="task.manage"><BackendTasks/></PermissionGate>},
 {path:'/snippet',element:<BackendSnippets system/>},
 {path:'/x-snippet',element:<BackendSnippets/>},
 ...(['access-policy','strategy','command-filter','ip-set'] as const).map(kind=>({path:`/${kind}`,element:<PermissionGate action="policy.read"><BackendPolicies kind={kind}/></PermissionGate>})),
 {path:'/access-request',element:<PermissionGate action="approval.read"><BackendApprovals/></PermissionGate>},
 {path:'/x-access-request',element:<BackendApprovals portal/>},
 {path:'/setting',element:<BackendSettings/>},
 {path:'/notification-preferences',element:<BackendSettings section="preferences"/>},
 {path:'/identity-settings',element:<CapabilityGate feature="identity.oidc-config"><PermissionGate action="identity.manage"><BackendSettings section="identity"/></PermissionGate></CapabilityGate>},
 {path:'/system-event',element:<PermissionGate action="backup.manage"><BackendBackupPage/></PermissionGate>},
 {path:'/website',element:<PermissionGate action="website.read"><BackendWebsites/></PermissionGate>},
 {path:'/x-website',element:<BackendWebsites portal/>},
 {path:'/certificate',element:<PermissionGate action="certificate.read"><BackendCertificates/></PermissionGate>},
 {path:'/website-setting',element:<PermissionGate action="identity.manage"><BackendWebsiteSettings/></PermissionGate>},
 {path:'/website-login',element:<BackendWebsiteLogin/>},
 {path:'/authorised-website',element:<PermissionGate action="grant.read"><BackendResourcesPage kind="grants"/></PermissionGate>},
 {path:'/storage',element:<CapabilityGate feature="storage.spaces"><BackendStoragePage/></CapabilityGate>},
 {path:'/graphics-playback',element:<BackendPlaybackPage/>},
    {path: '/gateway-group', element: <PermissionGate action="identity.manage"><BackendConnectivity kind="group"/></PermissionGate>},
    {path: '/network-proxy', element: <PermissionGate action="identity.manage"><BackendConnectivity kind="proxy"/></PermissionGate>},
    {path: '/monitoring', element: <PermissionGate action="identity.manage"><BackendMonitoring/></PermissionGate>},
    {path: '/tools', element: <PermissionGate action="identity.manage"><BackendDiagnostics/></PermissionGate>},
    {path: '/ssh-gateway', element: <PermissionGate action="identity.manage"><BackendSSHJumps/></PermissionGate>},
    {path: '/agent-gateway', element: <PermissionGate action="identity.manage"><BackendAgentGateways/></PermissionGate>},
    {path: '/setup', element: <SetupPage/>},
    {path: '/login', element: <BackendLoginPage/>},
    {path: '/user', element: <PermissionGate action="user.read"><BackendIdentityPage kind="users"/></PermissionGate>},
    {path: '/department', element: <PermissionGate action="department.read"><BackendIdentityPage kind="departments"/></PermissionGate>},
    {path: '/role', element: <PermissionGate action="role.read"><BackendIdentityPage kind="roles"/></PermissionGate>},
    {path: '/login-locked', element: <PermissionGate action="lock.read"><BackendIdentityPage kind="login-locked"/></PermissionGate>},
    {path: '/asset', element: <PermissionGate action="asset.read"><BackendResourcesPage kind="assets"/></PermissionGate>},
    {path: '/credential', element: <PermissionGate action="credential.read"><BackendResourcesPage kind="credentials"/></PermissionGate>},
    {path: '/authorised-asset', element: <PermissionGate action="grant.read"><BackendResourcesPage kind="grants"/></PermissionGate>},
    ...['/access','/terminal'].map(path=>({path,element:<CapabilityGate feature="session.ssh"><BackendTerminalPage/></CapabilityGate>})),
    {path:'/session-observer',element:<CapabilityGate feature="session.observe"><BackendObserverPage/></CapabilityGate>},
    {path:'/graphics',element:<CapabilityGate feature="session.graphics"><BackendTerminalPage/></CapabilityGate>},
    ...['/online-session','/access-log','/exec-command-log'].map(path=>({path,element:<CapabilityGate feature="session.ssh"><BackendSessionsPage online={path==='/online-session'}/></CapabilityGate>})),
    {path: '/x-asset', element: <CapabilityGate feature="assets.hosts"><BackendResourcesPage kind="portal"/></CapabilityGate>},
    {path: '/', element: <CapabilityGate feature="system.runtime"><BackendHomePage/></CapabilityGate>},
    ...['/info','/x-info'].map(path=>({path,element:<CapabilityGate feature="identity.self"><BackendHomePage accountOnly/></CapabilityGate>})),
    {path: '/system-status', element: <BackendWorkbench/>},
    {path: '/filesystem-log', element: <CapabilityGate feature="files.sftp"><BackendFileLogs/></CapabilityGate>},
    {path: '*', element: <UnavailableBackendPage/>},
];
const router = createBrowserRouter([
    {
        element: (
            isNewBackend ? <BackendRuntimeProvider><Suspense fallback={<Spin fullscreen/>}><BackendLayout><Suspense fallback={<div role="status" style={{padding:32,textAlign:'center'}}><Spin/> 正在加载页面…</div>}><Outlet/></Suspense></BackendLayout></Suspense></BackendRuntimeProvider> :
                <LicenseProvider><Outlet/></LicenseProvider>
        ),
        children: isNewBackend ? backendRoutes : routes,
    },
]);


function App() {
    let {t} = useTranslation();
    const {message} = AntdApp.useApp();

    useEffect(() => {
        let link = document.querySelector("link[rel~='icon']") as HTMLLinkElement;
        if (!link) {
            link = document.createElement('link');
            link.rel = 'icon';
            document.getElementsByTagName('head')[0].appendChild(link);
        }
        link.href = `${baseUrl()}/logo`;
    }, []);

    const un_auth = () => {
        window.location.href = '/login';
    }

    const redirect = (url: string) => {
        window.location.href = url;
    }

    const validate_error = (errorCode: number, error: string) => {
        let msg = t(`errors.${errorCode}`);
        if (!msg || msg === `errors.${errorCode}`) {
            msg = error;
        }

        message.error(error && msg !== error ? <div>
            <div>{msg}</div>
            <details style={{textAlign: 'left', marginTop: 4}}>
                <summary>{t('actions.detail')}</summary>
                <div style={{whiteSpace: 'pre-wrap', overflowWrap: 'anywhere'}}>{error}</div>
            </details>
        </div> : msg);
    }

    useEffect(() => {
        eventEmitter.on("API:UN_AUTH", un_auth)
        eventEmitter.on("API:REDIRECT", redirect)
        eventEmitter.on("API:VALIDATE_ERROR", validate_error)

        return () => {
            eventEmitter.off("API:UN_AUTH", un_auth)
            eventEmitter.off("API:REDIRECT", redirect)
            eventEmitter.off("API:VALIDATE_ERROR", validate_error)
        }
    }, [t]);

    return <RouterProvider router={router}/>
}

export default App
