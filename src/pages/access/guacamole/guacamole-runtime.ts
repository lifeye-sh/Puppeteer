import {duplicateKeys} from '@/pages/access/guacamole/keys';
import Guacamole from '@puppeteer/guacamole-common-js';

export interface GuacamoleRuntimeStatusHandlers {
    onStateChange?: (state: number) => void;
    onTunnelStateChange?: (state: number) => void;
    onError?: (status: Guacamole.Status) => void;
    onRequired?: (parameters: string[]) => void;
    onClipboard?: (stream: Guacamole.InputStream, mimetype: string) => void;
}

interface GuacamoleRuntimeOptions extends GuacamoleRuntimeStatusHandlers {
    container: HTMLDivElement;
    displayContainer: HTMLDivElement;
    interactive?: boolean;
    active?: boolean;
    remoteResizeDelay?: number;
}

interface GuacamoleConnectOptions {
    url: string;
    params: (size: {width: number; height: number}) => string;
    fixedSize?: boolean;
    remoteResize?: boolean;
}

/**
 * Guacamole 唯一的底层运行时。
 *
 * 负责 Tunnel/Client、Display、键鼠触控、InputSink、本地缩放、远端尺寸合并和完整销毁。
 */
export class GuacamoleRuntime {
    private readonly container: HTMLDivElement;
    private readonly displayContainer: HTMLDivElement;
    private readonly interactive: boolean;
    private readonly remoteResizeDelay: number;
    private handlers: GuacamoleRuntimeStatusHandlers;
    private resizeObserver: ResizeObserver | null = null;
    private resizeAnimationFrame = 0;
    private remoteResizeTimer = 0;
    private client: Guacamole.Client | null = null;
    private tunnel: Guacamole.WebSocketTunnel | null = null;
    private sink: Guacamole.InputSink | null = null;
    private sinkElement: HTMLElement | null = null;
    private keyboard: Guacamole.Keyboard | null = null;
    private mouse: Guacamole.Mouse | null = null;
    private touch: Guacamole.Mouse.Touchpad | null = null;
    private active: boolean;
    private fixedSize = false;
    private remoteResize = false;
    private lastSentSize = {width: 0, height: 0};
    private disposed = false;

    constructor(options: GuacamoleRuntimeOptions) {
        this.container = options.container;
        this.displayContainer = options.displayContainer;
        this.interactive = options.interactive !== false;
        this.active = options.active !== false;
        this.remoteResizeDelay = options.remoteResizeDelay ?? 120;
        this.handlers = options;

        if (typeof ResizeObserver !== 'undefined') {
            this.resizeObserver = new ResizeObserver(() => this.scheduleResize());
            this.resizeObserver.observe(this.container);
        }
        document.addEventListener('fullscreenchange', this.handleViewportChange);
        window.addEventListener('resize', this.handleViewportChange);
    }

    setHandlers(handlers: GuacamoleRuntimeStatusHandlers) {
        this.handlers = handlers;
    }

    setActive(active: boolean) {
        this.active = active;
        if (active) {
            this.focus();
            this.scheduleResize();
        } else {
            this.keyboard?.reset();
        }
    }

    getSize() {
        const fullscreenElement = document.fullscreenElement;
        const sizeElement = fullscreenElement?.contains(this.displayContainer)
            ? fullscreenElement
            : this.container;
        return {
            width: Math.max(0, sizeElement.clientWidth),
            height: Math.max(0, sizeElement.clientHeight),
        };
    }

    connect(options: GuacamoleConnectOptions) {
        if (this.disposed) {
            return null;
        }
        this.disconnect();
        this.fixedSize = options.fixedSize === true;
        this.remoteResize = options.remoteResize === true;
        this.lastSentSize = {width: 0, height: 0};

        const tunnel = new Guacamole.WebSocketTunnel(options.url);
        const client = new Guacamole.Client(tunnel);
        this.tunnel = tunnel;
        this.client = client;

        tunnel.onstatechange = (state: number) => this.handlers.onTunnelStateChange?.(state);
        // WebSocket 连接失败和超时由 Tunnel 报错，不会触发 Client 的协议错误回调。
        tunnel.onerror = (status: Guacamole.Status) => this.handlers.onError?.(status);
        client.onstatechange = (state: number) => this.handlers.onStateChange?.(state);
        client.onerror = (status: Guacamole.Status) => this.handlers.onError?.(status);
        client.onrequired = (parameters: string[]) => this.handlers.onRequired?.([...parameters]);
        client.onclipboard = (stream: Guacamole.InputStream, mimetype: string) => {
            this.handlers.onClipboard?.(stream, mimetype);
        };

        this.displayContainer.replaceChildren();
        const display = client.getDisplay();
        const element = display.getElement();
        this.displayContainer.appendChild(element);
        display.onresize = () => this.scheduleResize();

        if (this.interactive) {
            this.bindInput(client, element);
        }

        client.connect(options.params(this.getSize()));
        this.scheduleResize();
        return client;
    }

    resize() {
        if (!this.active || !this.client) {
            return;
        }
        const size = this.getSize();
        if (size.width === 0 || size.height === 0) {
            return;
        }

        const display = this.client.getDisplay();
        const displayWidth = display.getWidth();
        const displayHeight = display.getHeight();
        if (displayWidth > 0 && displayHeight > 0) {
            display.scale(Math.min(size.width / displayWidth, size.height / displayHeight));
        }

        clearTimeout(this.remoteResizeTimer);
        const remoteSizeDiffers = displayWidth !== size.width || displayHeight !== size.height;
        if (this.remoteResize && !this.fixedSize && remoteSizeDiffers) {
            this.remoteResizeTimer = window.setTimeout(() => {
                const latestSize = this.getSize();
                const sizeChanged = latestSize.width !== this.lastSentSize.width
                    || latestSize.height !== this.lastSentSize.height;
                if (this.active && sizeChanged && latestSize.width > 0 && latestSize.height > 0) {
                    this.client?.sendSize(latestSize.width, latestSize.height);
                    this.lastSentSize = latestSize;
                }
            }, this.remoteResizeDelay);
        }
    }

    focus() {
        this.sink?.focus();
    }

    resetKeyboard() {
        this.keyboard?.reset();
    }

    sendClipboard(data: string | Blob, mimetype: string) {
        if (!this.client) {
            return;
        }
        const stream = this.client.createClipboardStream(mimetype);
        if (typeof data === 'string') {
            const writer = new Guacamole.StringWriter(stream);
            writer.sendText(data);
            writer.sendEnd();
            return;
        }
        const writer = new Guacamole.BlobWriter(stream);
        writer.oncomplete = () => writer.sendEnd();
        writer.sendBlob(data);
    }

    sendKeys(keys: string[]) {
        for (const key of keys) {
            this.client?.sendKeyEvent(1, Number(key));
        }
        for (const key of keys) {
            this.client?.sendKeyEvent(0, Number(key));
        }
    }

    sendArgumentValues(values: Record<string, unknown>) {
        for (const [name, rawValue] of Object.entries(values)) {
            const stream = this.client?.createArgumentValueStream('text/plain', name);
            if (!stream) {
                continue;
            }
            const writer = new Guacamole.StringWriter(stream);
            writer.sendText(rawValue == null ? '' : String(rawValue));
            writer.sendEnd();
        }
    }

    disconnect() {
        clearTimeout(this.remoteResizeTimer);
        cancelAnimationFrame(this.resizeAnimationFrame);
        this.keyboard?.reset();
        if (this.keyboard) {
            this.keyboard.onkeydown = null;
            this.keyboard.onkeyup = null;
        }
        if (this.mouse) {
            this.mouse.onmousedown = null;
            this.mouse.onmouseup = null;
            this.mouse.onmousemove = null;
        }
        if (this.touch) {
            this.touch.onmousedown = null;
            this.touch.onmousemove = null;
            this.touch.onmouseup = null;
        }
        if (this.client) {
            this.client.getDisplay().onresize = null;
            this.client.onstatechange = null;
            this.client.onerror = null;
            this.client.onrequired = null;
            this.client.onclipboard = null;
        }
        this.sinkElement?.removeEventListener('paste', this.preventPasteDefault);
        if (this.tunnel) {
            this.tunnel.onstatechange = null;
            this.tunnel.onerror = null;
        }
        this.client?.disconnect();
        this.client = null;
        this.tunnel = null;
        this.sink = null;
        this.sinkElement = null;
        this.keyboard = null;
        this.mouse = null;
        this.touch = null;
        this.displayContainer.replaceChildren();
    }

    dispose() {
        if (this.disposed) {
            return;
        }
        this.disposed = true;
        this.disconnect();
        this.resizeObserver?.disconnect();
        document.removeEventListener('fullscreenchange', this.handleViewportChange);
        window.removeEventListener('resize', this.handleViewportChange);
    }

    private bindInput(client: Guacamole.Client, element: HTMLElement) {
        const sink = new Guacamole.InputSink();
        const sinkElement = sink.getElement();
        sinkElement.addEventListener('paste', this.preventPasteDefault);
        element.appendChild(sinkElement);

        const keyboard = new Guacamole.Keyboard(sinkElement);
        const handleKeyEvent = (pressed: boolean, keysym: number) => {
            const twin = duplicateKeys.get(keysym);
            if (twin !== undefined && keyboard.pressed[twin]) {
                return false;
            }
            client.sendKeyEvent(pressed ? 1 : 0, keysym);
            // 按键已交给远端处理；返回 false 阻止 F5、Ctrl+S 等触发浏览器默认行为。
            return false;
        };
        keyboard.onkeydown = (keysym: number) => handleKeyEvent(true, keysym);
        keyboard.onkeyup = (keysym: number) => handleKeyEvent(false, keysym);

        const display = client.getDisplay();
        const mouse = new Guacamole.Mouse(element);
        mouse.onmousedown = mouse.onmouseup = (mouseState: Guacamole.Mouse.State) => {
            client.sendMouseState(mouseState);
            sink.focus();
        };
        mouse.onmousemove = (mouseState: Guacamole.Mouse.State) => {
            mouseState.x /= display.getScale();
            mouseState.y /= display.getScale();
            client.sendMouseState(mouseState);
        };

        const touch = new Guacamole.Mouse.Touchpad(element);
        touch.onmousedown = touch.onmousemove = touch.onmouseup = (state: Guacamole.Mouse.State) => {
            client.sendMouseState(state);
        };

        this.sink = sink;
        this.sinkElement = sinkElement;
        this.keyboard = keyboard;
        this.mouse = mouse;
        this.touch = touch;
        if (this.active) {
            this.focus();
        }
    }

    private scheduleResize() {
        cancelAnimationFrame(this.resizeAnimationFrame);
        this.resizeAnimationFrame = requestAnimationFrame(() => this.resize());
    }

    private handleViewportChange = () => this.scheduleResize();

    private preventPasteDefault = (event: ClipboardEvent) => event.preventDefault();
}
