import test from 'node:test';
import assert from 'node:assert/strict';
import {aiExecutionState} from '../src/pages/backend/ai-entitlement.ts';
test('AI execution is distinct from retained workspace access and fails closed',()=>{
 assert.equal(aiExecutionState({'ai.chat':{state:'available'}}).allowed,false);
 assert.equal(aiExecutionState({'ai.inference':{state:'available'}}).allowed,true);
 for(const reason of ['expired','invalid_license','not_entitled']){
  const result=aiExecutionState({'ai.chat':{state:'available'},'ai.inference':{state:'not_entitled',reason}});
  assert.equal(result.allowed,false);assert.match(result.message,/历史/);assert.match(result.message,/停止/);
 }
});
