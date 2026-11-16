export const graphicsQualities = {
    economy: {label: '省资源', width: 1280, height: 720, detail: '最高 1280 × 720，16 位色，关闭桌面特效'},
    balanced: {label: '均衡', width: 1920, height: 1080, detail: '最高 1920 × 1080，24 位色，关闭桌面特效'},
    sharp: {label: '清晰', width: 3840, height: 2160, detail: '最高 3840 × 2160，24 位色，启用字体平滑，资源占用较高'},
} as const;
export type GraphicsQuality = keyof typeof graphicsQualities;
export function graphicsSize(name: GraphicsQuality, width: number, height: number) {
    const q = graphicsQualities[name];
    const scale = Math.min(1, q.width / width, q.height / height);
    return {width: Math.max(4, Math.floor(width * scale / 4) * 4), height: Math.max(2, Math.floor(height * scale / 2) * 2)};
}
