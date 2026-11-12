import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,rm,access} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {runMaintenance} from '../scripts/wal-maintenance.mjs';
test('maintenance rejects unsafe container arguments before accessing state',async()=>{
 await assert.rejects(runMaintenance({container:'--privileged',operation:'full'}),/Valid container/);
 await assert.rejects(runMaintenance({container:'test',operation:'restore'}),/Valid container/);
});
test('maintenance requires TLS and releases lock when configuration is rejected',async()=>{
 const directory=await mkdtemp(join(tmpdir(),'wal-worker-'));
 try{
  const webhookFile=join(directory,'url');await writeFile(webhookFile,'http://example.com/events');
  await assert.rejects(runMaintenance({container:'test',directory,webhookFile,operation:'check',allowLocalHTTP:true}),/HTTPS webhook/);
  await assert.rejects(access(join(directory,'running.lock')));
  await mkdir(join(directory,'running.lock'));
  await assert.rejects(runMaintenance({container:'test',directory,webhookFile,operation:'check'}),/already running/);
 }finally{await rm(directory,{recursive:true,force:true})}
});
test('maintenance refuses to reuse another database container state',async()=>{
 const directory=await mkdtemp(join(tmpdir(),'wal-worker-'));
 try{
  await writeFile(join(directory,'state.json'),JSON.stringify({container:'another',pending:[],lastSuccess:{}}));
  await assert.rejects(runMaintenance({container:'test',directory,operation:'check'}),/another container/);
 }finally{await rm(directory,{recursive:true,force:true})}
});
