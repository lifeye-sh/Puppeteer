/** Incremental SSE decoding; incomplete EOF is an error, never a successful turn. */
export type EventStreamOptions = {completionEvent?: string; disconnectMessage?: string};
export async function readEventStream(response: Response, onEvent: (event: string, value: unknown) => void, options: EventStreamOptions = {}) {
    if (!response.body || !response.headers.get('Content-Type')?.includes('text/event-stream')) throw new Error('服务未返回流式响应');
    const reader = response.body.getReader();
    const decoder = new TextDecoder('utf-8', {fatal: true});
    let buffer = '', complete = false;
    try {
        while (!complete) {
            const {value, done} = await reader.read();
            buffer += decoder.decode(value, {stream: !done});
            if (buffer.length > 1024 * 1024) throw new Error('流式响应超出限制');
            let end: number;
            while ((end = buffer.indexOf('\n\n')) >= 0) {
                const frame = buffer.slice(0, end); buffer = buffer.slice(end + 2);
                let event = 'message'; const data: string[] = [];
                for (const line of frame.split('\n')) {
                    if (line.startsWith('event:')) event = line.slice(6).trim();
                    if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
                }
                if (!data.length) continue;
                const parsed: unknown = JSON.parse(data.join('\n'));
                if (event === 'error') throw new Error((parsed as {message?: string}).message || '流式响应失败');
                onEvent(event, parsed);
                if (event === (options.completionEvent ?? 'done')) { complete = true; break; }
            }
            if (done) break;
        }
        if (!complete) throw new Error(options.disconnectMessage ?? '连接已断开，正在从历史对话恢复进度。服务端不会重复调用模型。');
    } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
    }
}
