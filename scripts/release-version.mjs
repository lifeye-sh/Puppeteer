// Keep the historical RC series readable while supporting the public beta series.
export const releaseVersionPattern = /^(?:r17-rc[1-9]\d*|beta(?:0|[1-9]\d*)\.(?:0|[1-9]\d*))(?![\s\S])/;
export function compareReleaseVersions(a, b) {
    const parts = value => value.startsWith('beta')
        ? [1, ...value.slice(4).split('.').map(Number)]
        : [0, 17, Number(value.slice(6))];
    const left = parts(a), right = parts(b);
    for (let i = 0; i < left.length; i++) {
        if (left[i] !== right[i]) return left[i] - right[i];
    }
    return 0;
}
