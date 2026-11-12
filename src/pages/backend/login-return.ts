const key = 'nt-login-return';
type Store = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
export function safeReturn(value: string, origin: string): string {
    try {
        if (value.length > 9000) return '/';
        const u = new URL(value, origin);
        return u.origin === origin && !u.username && !u.password &&
            ['/website-login', '/api/oidc/server/authorize'].includes(u.pathname)
            ? u.pathname + u.search : '/';
    } catch { return '/'; }
}
// Keep a same-tab return across an external identity provider redirect. No
// credential is stored, and every read revalidates the path and five-minute TTL.
export function rememberReturn(search: string, origin: string, store: Store, now = Date.now()) {
    const query = new URLSearchParams(search);
    if (!query.has('return')) return;
    const path = safeReturn(query.get('return') ?? '', origin);
    try {
        if (path === '/') store.removeItem(key);
        else store.setItem(key, JSON.stringify({path, expires: now + 300000}));
    } catch { /* Login still works when browser storage is unavailable. */ }
}
export function consumeReturn(search: string, origin: string, store: Store, now = Date.now()): string {
    let saved: {path?: string; expires?: number} = {};
    try { saved = JSON.parse(store.getItem(key) ?? '{}'); store.removeItem(key); } catch { /* Ignore corrupt storage. */ }
    const query = new URLSearchParams(search);
    if (query.has('return')) return safeReturn(query.get('return') ?? '', origin);
    return typeof saved?.path === 'string' && typeof saved?.expires === 'number' && saved.expires > now && saved.expires <= now + 300000
        ? safeReturn(saved.path, origin) : '/';
}
