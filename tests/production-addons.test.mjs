import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,readFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {addServices} from '../scripts/production-addons.mjs';

test('deployment addons preserve existing keys and operator-selected image settings', {skip:process.platform==='linux'&&process.getuid()!==0},async()=>{
 const dir=await mkdtemp(join(tmpdir(),'puppeteer-addons-'));
 try {
  await mkdir(join(dir,'secrets'));
  await writeFile(join(dir,'production.env'),'PUPPETEER_ORIGIN=https://example.com\nPUPPETEER_GRAPHICS_IMAGE=private.example/graphics:approved\n');
  await writeFile(join(dir,'secrets','mfa_key'),'must-not-change');
  const first=await addServices(dir),secret=await readFile(join(dir,'secrets','mp4_token'),'utf8');
  assert.equal(first.addedSettings,3);assert.match(secret.trim(),/^[0-9a-f]{64}$/);
  assert.equal((await addServices(dir)).addedSettings,0);
  assert.equal(await readFile(join(dir,'secrets','mp4_token'),'utf8'),secret);
  assert.equal(await readFile(join(dir,'secrets','mfa_key'),'utf8'),'must-not-change');
  const env=await readFile(join(dir,'production.env'),'utf8');
  assert.equal((env.match(/PUPPETEER_GRAPHICS_IMAGE=/g)||[]).length,1);
  assert.match(env,/private.example\/graphics:approved/);assert.match(env,/PUPPETEER_PROTOCOL_HOST=example.com/);
 }finally {await rm(dir,{recursive:true,force:true})}
});
