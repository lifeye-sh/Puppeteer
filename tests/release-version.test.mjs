import test from 'node:test';
import assert from 'node:assert/strict';
import {releaseVersionPattern, compareReleaseVersions} from '../scripts/release-version.mjs';

test('explicit beta and historical RC versions only', () => {
    for (const value of ['beta0.9', 'beta0.10', 'beta1.0', 'r17-rc13']) assert.ok(releaseVersionPattern.test(value), value);
    for (const value of ['latest', 'beta01.9', 'beta0.09', 'beta0.9/../x', 'beta0.9;id', 'r17-rc0', 'beta0.9\n']) assert.ok(!releaseVersionPattern.test(value), value);
});
test('beta ordering is numeric and follows the historical RC series', () => {
    assert.deepEqual(['beta0.10', 'r17-rc13', 'beta0.9', 'beta1.0', 'r17-rc9'].sort(compareReleaseVersions),
        ['r17-rc9', 'r17-rc13', 'beta0.9', 'beta0.10', 'beta1.0']);
});
