import {createHash} from 'node:crypto';
import {readdir, readFile, writeFile} from 'node:fs/promises';
import {resolve, relative, dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const output = resolve(root, 'dist/production');
const manifestName = 'release-manifest.json';

export async function inventory(directory) {
    const entries = [];
    async function walk(dir) {
        for (const item of await readdir(dir, {withFileTypes:true})) {
            const path = resolve(dir, item.name);
            const name = relative(directory, path).replaceAll('\\', '/');
            if (item.isSymbolicLink()) throw new Error(`Symbolic link in release: ${name}`);
            if (item.isDirectory()) {await walk(path); continue;}
            if (!item.isFile()) throw new Error(`Unsupported release entry: ${name}`);
            if (name === manifestName) continue;
            const bytes = await readFile(path);
            entries.push({path:name, bytes:bytes.length, sha256:createHash('sha256').update(bytes).digest('hex')});
        }
    }
    await walk(directory);
    return entries.sort((a,b)=>a.path < b.path ? -1 : a.path > b.path ? 1 : 0);
}

export async function verify(directory) {
    const manifest = JSON.parse(await readFile(resolve(directory, manifestName), 'utf8'));
    if (manifest.product !== 'Puppeteer' || manifest.profile !== 'production' || manifest.backend !== 'new' || manifest.format !== 1) {
        throw new Error('Invalid production release identity');
    }
    const actual = await inventory(directory);
    if (JSON.stringify(actual) !== JSON.stringify(manifest.files)) throw new Error('Release files missing, added or modified');
    const html = await readFile(resolve(directory, 'index.html'), 'utf8');
    if (/backend-validation-profile|configured target|\/@vite\/client/.test(html)) throw new Error('Development markup in production release');
    return {product:manifest.product, files:actual.length, bytes:actual.reduce((n,f)=>n+f.bytes,0)};
}

async function main() {
    const action = process.argv[2];
    if (!['build','verify'].includes(action)) throw new Error('Usage: frontend-release.mjs build|verify');
    if (action === 'build') {
        for (const [script,args] of [
            ['node_modules/typescript/bin/tsc',['--noEmit']],
            ['node_modules/vite/bin/vite.js',['build','--mode','production','--outDir','dist/production']],
        ]) {
            const child = spawnSync(process.execPath, [resolve(root,script), ...args], {cwd:root, stdio:'inherit'});
            if (child.error) throw child.error;
            if (child.status !== 0) throw new Error(`${script} failed (${child.status})`);
        }
        const manifest = {format:1,product:'Puppeteer',profile:'production',backend:'new',createdAt:new Date().toISOString(),files:await inventory(output)};
        await writeFile(resolve(output,manifestName), JSON.stringify(manifest,null,2)+'\n');
    }
    console.log(JSON.stringify(await verify(output)));
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
    main().catch(error=>{console.error(error.message);process.exitCode=1;});
}
