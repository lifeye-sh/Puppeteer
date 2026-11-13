import {defineConfig, loadEnv, PluginOption} from 'vite'
import {backendProfile} from './scripts/frontend-backend-profile.ts';
import {resolve} from 'path';
import {VitePWA} from 'vite-plugin-pwa';
import {visualizer} from "rollup-plugin-visualizer";
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vitejs.dev/config/
export default defineConfig(({mode, command}) => {
    const isProd = mode === 'production';
    const env = {...loadEnv(mode, process.cwd(), ''), ...process.env};
    if (isProd && env.VITE_BACKEND_MODE && env.VITE_BACKEND_MODE !== 'new') {
        throw new Error('Production releases require the Puppeteer backend; remove VITE_BACKEND_MODE or set it to new.');
    }
    const analyze = process.env.ANALYZE === 'true';
    const profile = command === 'serve'
        ? backendProfile(mode, env)
        : undefined;
    return {
        plugins: [
            ...(profile ? [profile.plugin] : []),
            react(),
            tailwindcss(),
            ...(isProd ? [
                VitePWA({
                    registerType: 'autoUpdate',
                    // 发布注销脚本，替换已安装的旧 Service Worker，避免预缓存持续提供旧页面。
                    // 保留 manifest，用于应用名称、图标和独立窗口显示。
                    selfDestroying: true,
                    // 新页面不再注册；旧注册仍会通过原来的 /sw.js 地址获取注销脚本。
                    injectRegister: false,
                    manifest: {
                        name: 'Puppeteer',
                        description: '',
                        background_color: '#313131',
                        icons: [
                            {
                                src: '/api/logo',
                                sizes: '512x512',
                                type: 'image/png',
                                purpose: "any"
                            },
                        ]
                    },
                })
            ] : []),
            ...(analyze ? [
                visualizer({
                    filename: 'stats.html',
                }) as unknown as PluginOption
            ] : []),
        ],
        resolve: {
            alias: {'@': resolve(import.meta.dirname, './src')},
        },
        server: profile?.server,
        build: {
            sourcemap: false,
            minify: 'oxc',

        }
    }
});
