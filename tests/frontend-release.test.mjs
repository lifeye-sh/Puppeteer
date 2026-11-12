import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,dirname} from 'node:path';
import {inventory,verify} from '../scripts/frontend-release.mjs';
import {usesPuppeteerBackend} from '../src/lib/backend-mode.ts';

test('production cannot select legacy even with a stale environment override',()=>{
    for(const override of [undefined,'','new','legacy']) assert.equal(usesPuppeteerBackend('production',override),true);
    assert.equal(usesPuppeteerBackend('backend'),true);
    assert.equal(usesPuppeteerBackend('legacy'),false);
});

test('release verification detects tampering, missing and unexpected files',async()=>{
    const dir=await mkdtemp(join(tmpdir(),'puppeteer-release-'));
    try {
        const html='<html><title>Puppeteer</title></html>';
        await writeFile(join(dir,'index.html'),html);
        const manifest={format:1,product:'Puppeteer',profile:'production',backend:'new',files:await inventory(dir)};
        await writeFile(join(dir,'release-manifest.json'),JSON.stringify(manifest));
        assert.equal((await verify(dir)).files,1);
        await writeFile(join(dir,'index.html'),html+'modified');
        await assert.rejects(verify(dir),/modified/);
        await writeFile(join(dir,'index.html'),html);
        await writeFile(join(dir,'unexpected.key'),'fixture');
        await assert.rejects(verify(dir),/modified/);
        await rm(join(dir,'unexpected.key'));
        await rm(join(dir,'index.html'));
        await assert.rejects(verify(dir),/modified/);
        await writeFile(join(dir,'index.html'),'<div id="backend-validation-profile"></div>');
        manifest.files=await inventory(dir);
        await writeFile(join(dir,'release-manifest.json'),JSON.stringify(manifest));
        await assert.rejects(verify(dir),/Development markup/);
    } finally {
        assert.equal(dirname(dir),tmpdir());
        await rm(dir,{recursive:true,force:true});
    }
});
