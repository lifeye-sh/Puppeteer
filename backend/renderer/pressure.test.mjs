import test from 'node:test';
import assert from 'node:assert/strict';
import {requireMemory} from './pressure.mjs';

test('new renderer jobs wait under pressure and resume at the admission threshold', () => {
    for (const bytes of [0, 256 * 1024 * 1024 - 1]) assert.throws(() => requireMemory(bytes), /renderer_memory_pressure/);
    assert.doesNotThrow(() => requireMemory(256 * 1024 * 1024));
    assert.throws(() => requireMemory(NaN), /resource_probe_unavailable/);
});
