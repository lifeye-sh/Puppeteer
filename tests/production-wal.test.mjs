import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,readFile,rm,stat} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {addWAL} from '../scripts/production-wal.mjs';
test('WAL configuration preserves repository key and selected image on rerun',{skip:process.platform==='linux'&&process.getuid()!==0},async()=>{
 const dir=await mkdtemp(join(tmpdir(),'puppeteer-wal-'));
 try {
  await mkdir(join(dir,'secrets'));
  await writeFile(join(dir,'production.env'),'PUPPETEER_ORIGIN=https://example.com\n');
  assert.equal((await addWAL(dir)).addedSettings,1);
  const key=await readFile(join(dir,'secrets/wal_cipher_pass'),'utf8');
  assert.match(key.trim(),/^[0-9a-f]{64}$/);
  await writeFile(join(dir,'production.env'),'PUPPETEER_POSTGRES_WAL_IMAGE=private.example/pg:approved\n');
  assert.equal((await addWAL(dir)).addedSettings,0);
  assert.equal(await readFile(join(dir,'secrets/wal_cipher_pass'),'utf8'),key);
  if(process.platform==='linux') {
   const info=await stat(join(dir,'secrets/wal_cipher_pass'));
   assert.equal(info.uid,70);assert.equal(info.mode&0o777,0o400);
  }
 }finally{await rm(dir,{recursive:true,force:true})}
});
