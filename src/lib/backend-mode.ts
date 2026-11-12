// Production releases always use the Puppeteer backend. Legacy is a development profile.
export function usesPuppeteerBackend(mode: string, override?: string): boolean {
    return mode === 'production' || mode === 'backend' || override === 'new';
}
