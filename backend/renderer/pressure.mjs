import {availableMemory} from 'node:process';
import {admitScratch} from './scratch.mjs';

// libuv accounts for the process/container memory constraint on supported Linux.
// Reserve headroom for starting Chromium/FFmpeg; this is not a capacity guarantee.
export function requireMemory(available = availableMemory()) {
    if (!Number.isFinite(available) || available < 0) throw Error('resource_probe_unavailable');
    if (available < 256 * 1024 * 1024) throw Error('renderer_memory_pressure');
}

export async function admitResources() {
    requireMemory();
    await admitScratch();
}
