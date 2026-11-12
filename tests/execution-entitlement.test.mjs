import test from 'node:test';
import assert from 'node:assert/strict';
import {executionEntitlement} from '../src/pages/backend/execution-entitlement.ts';
test('execution gates fail closed independently of retained workspaces',()=>{
 for(const [workspace,key] of [['automation.jobs','automation.execute'],['notification.delivery','notification.send'],['storage.spaces','files.write']]){
  assert.equal(executionEntitlement({[workspace]:{state:'available'}},key,'操作').allowed,false);
  assert.equal(executionEntitlement({[key]:{state:'available'}},key,'操作').allowed,true);
  for(const reason of ['expired','invalid_license','not_entitled']){
   const result=executionEntitlement({[key]:{state:'not_entitled',reason}},key,'操作');
   assert.equal(result.allowed,false);assert.match(result.message,/历史、配置与取消/);
  }
 }
});
