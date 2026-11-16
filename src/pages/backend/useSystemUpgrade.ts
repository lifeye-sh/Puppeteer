import {useCallback, useEffect, useRef, useState} from 'react';

export type UpgradeTask = {
    id: string; source: string; target: string; stage: string; reason?: string;
    started: string; updated: string; terminal: boolean; progressToken?: string;
    events: {stage: string; at: string}[];
};
type Ticket = {id: string; token: string};
const storageKey = 'puppeteer-upgrade-progress';

export function useSystemUpgrade(enabled: boolean) {
    const [task, setTask] = useState<UpgradeTask>();
    const [connection, setConnection] = useState('');
    const ticket = useRef<Ticket | undefined>(undefined);
    const done = useRef(false);
    const accept = useCallback((value: UpgradeTask) => {
        setTask(value);
        done.current = value.terminal;
        if (value.progressToken && /^[a-f0-9]{32}$/.test(value.id) && /^[a-f0-9]{64}$/.test(value.progressToken)) {
            ticket.current = {id: value.id, token: value.progressToken};
            try {sessionStorage.setItem(storageKey, JSON.stringify(ticket.current))} catch { /* progress still works in this tab */ }
        }
        if (value.terminal) {
            ticket.current = undefined;
            try {sessionStorage.removeItem(storageKey)} catch { /* private browser mode */ }
        }
        setConnection('');
    }, []);
    const refresh = useCallback(() => {done.current = false}, []);
    useEffect(() => {
        if (!enabled) return;
        try {
            const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null') as Ticket | null;
            if (saved && /^[a-f0-9]{32}$/.test(saved.id) && /^[a-f0-9]{64}$/.test(saved.token)) ticket.current = saved;
        } catch { /* invalid saved ticket is ignored */ }
        let cancelled = false;
        let timer: ReturnType<typeof setTimeout>;
        let abort: AbortController | undefined;
        const read = async (url: string) => {
            abort = new AbortController();
            const timeout = setTimeout(() => abort?.abort(), 8000);
            try {
                const response = await fetch(url, {cache: 'no-store', signal: abort.signal, referrerPolicy: 'no-referrer'});
                if (!response.ok) throw new Error('暂不可达');
                return await response.json();
            } finally {clearTimeout(timeout)}
        };
        const poll = async () => {
            try {
                const value = await read('/api/admin/updates/task') as {task?: UpgradeTask};
                if (!cancelled) {if (value.task) accept(value.task); else setConnection('')}
            } catch {
                const saved = ticket.current;
                if (saved) {
                    try {
                        const value = await read(`/upgrade-progress/${saved.id}?ticket=${saved.token}`) as UpgradeTask;
                        if (!cancelled && value.id === saved.id) accept(value);
                    } catch {
                        if (!cancelled) setConnection('入口正在重启或进度暂不可达，将自动重连。请勿重复提交；超过 4 小时需重新登录查询。');
                    }
                } else if (!cancelled) setConnection('升级任务查询暂不可用，执行器可能尚未升级。');
            }
            if (!cancelled) timer = setTimeout(poll, done.current ? 15000 : 3000);
        };
        void poll();
        return () => {cancelled = true; clearTimeout(timer); abort?.abort()};
    }, [enabled, accept]);
    return {task, connection, accept, refresh};
}
