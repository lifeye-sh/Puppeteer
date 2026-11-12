import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,writeFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {rendererToken} from './secrets.mjs';
test('renderer file secret loading rejects ambiguous, short and oversized credentials',()=>{
 const dir=mkdtempSync(join(tmpdir(),'renderer-secret-'));
 try {
  const file=join(dir,'token'),token='test-only-'.repeat(8);
  writeFileSync(file,token+'\n',{mode:0o600});
  assert.equal(rendererToken({NT_MP4_TOKEN_FILE:file}),token);
  assert.equal(rendererToken({NT_MP4_TOKEN:token}),token);
  assert.throws(()=>rendererToken({NT_MP4_TOKEN_FILE:file,NT_MP4_TOKEN:token}));
  assert.throws(()=>rendererToken({NT_MP4_TOKEN:'short'}));
  writeFileSync(file,'x'.repeat(4097));assert.throws(()=>rendererToken({NT_MP4_TOKEN_FILE:file}));
 }finally{rmSync(dir,{recursive:true,force:true})}
});
