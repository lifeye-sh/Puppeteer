import type {Plugin, ServerOptions} from 'vite';
import {readFileSync} from 'node:fs';

export function backendProfile(mode: string, env: Record<string, string | undefined>) {
    const isolated = mode === 'legacy' || mode === 'backend';
    const targetKey = mode === 'backend' ? 'BACKEND_API_TARGET' : 'LEGACY_API_TARGET';
    const rawTarget = isolated
        ? env[targetKey] || (mode === 'backend' ? 'http://127.0.0.1:8089' : 'http://localhost:8088')
        : env.DEV_API_TARGET || 'http://localhost:8088';
    const target = new URL(rawTarget);
    if (!['http:', 'https:'].includes(target.protocol) || target.username || target.password
        || target.pathname !== '/' || target.search || target.hash) {
        throw new Error('API target must be an HTTP(S) origin without credentials, path, query or fragment.');
    }
    const port = mode === 'backend' ? 5174 : 5173;
    const hostname = `${mode}.localhost`;
    const certPath = env.BACKEND_TLS_CERT;
    const keyPath = env.BACKEND_TLS_KEY;
    if (mode === 'backend' && (!!certPath !== !!keyPath)) throw new Error('Both BACKEND_TLS_CERT and BACKEND_TLS_KEY are required.');
    const tls = mode === 'backend' && certPath && keyPath ? {cert: readFileSync(certPath), key: readFileSync(keyPath)} : undefined;
    const origin = `${tls ? 'https' : 'http'}://${hostname}:${port}`;
    const proxy = {
        target: target.origin,
        changeOrigin: true,
        // Keep the browser Origin for backend CSRF/WS validation.
        ...(isolated ? {cookieDomainRewrite: ''} : {}),
    };
    const server: ServerOptions = {
        ...(tls ? {https: tls} : {}),
        fs: {
            // Keep Vite 8 defaults, and protect backend credentials / browser test logs.
            deny: ['.env', '.env.*', '*.{crt,pem,key,p12,pfx,cer,der}', '.npmrc', '.yarnrc.yml', '**/.git/**',
                '**/backend/.local/**', '**/.playwright-cli/**'],
        },
        ...(isolated ? {host: '127.0.0.1', port, strictPort: true, open: origin} : {}),
        proxy: {
            '/.well-known/openid-configuration': {...proxy},
            '/swagger/': {...proxy},
            '/api/': {...proxy, ws: true},
        },
    };
    const plugin: Plugin = {
        name: 'backend-validation-profile',
        apply: 'serve',
        configureServer(vite) {
            if (!isolated) return;
            vite.middlewares.use((req, res, next) => {
                const authority = req.headers.host ?? req.headers[':authority'];
                if (typeof authority !== 'string' || authority.split(':')[0] !== hostname) {
                    res.statusCode = 421;
                    res.setHeader('Content-Type', 'text/plain; charset=utf-8');
                    res.end(`Use ${origin} to isolate this validation environment's cookies.`);
                    return;
                }
                if (req.url === '/__migration/profile' && req.method === 'GET') {
                    res.setHeader('Content-Type', 'application/json');
                    res.setHeader('Cache-Control', 'no-store');
                    res.end(JSON.stringify({profile: mode, configuredTarget: target.origin, frontendOrigin: origin}));
                    return;
                }
                next();
            });
        },
        transformIndexHtml() {
            if (!isolated) return;
            // This identifies configuration only; runtime identity comes from the backend in R01.
            return [{
                tag: 'div',
                attrs: {
                    id: 'backend-validation-profile',
                    style: 'position:fixed;bottom:0;right:0;z-index:2147483647;background:#172554;color:#fff;padding:4px 10px;font:12px monospace;pointer-events:none',
                },
                children: `${mode === 'backend' ? 'NEW BACKEND' : 'LEGACY'} · ${hostname} · configured target (not health check)`,
                injectTo: 'body',
            }];
        },
    };
    return {server, plugin};
}
