import test from 'node:test';
import assert from 'node:assert/strict';
import {grantBatchCount,grantBatchRequest} from '../src/pages/backend/grant-batch-model.ts';
const choice=value=>({value,label:'name '+value});
test('both directions and mixed users/departments create the full deduplicated product',()=>{
 const values={users:['A','a','b'].map(choice),departments:['c','d'].map(choice),resources:['X','x','y'].map(choice)};
 assert.equal(grantBatchCount(values),8);
 assert.deepEqual(grantBatchRequest(values).userIds,['a','b']);
 assert.equal(grantBatchCount({users:[choice('a')],resources:['x','y','z'].map(choice)}),3);
 assert.equal(grantBatchCount({users:['a','b'].map(choice),departments:[choice('c')],resources:[choice('x')]}),3);
});
test('empty, excessive and invalid validity selections are blocked before submission',()=>{
 for(const values of [{},{users:[choice('a')]},{resources:[choice('x')]},{users:[choice('a')],resources:[choice('x')],validFrom:'invalid'},
 {users:[choice('a')],resources:[choice('x')],validFrom:'2030-01-01T00:00:00Z',expiresAt:'2029-01-01T00:00:00Z'},
 {users:Array.from({length:100},(_,i)=>choice('u'+i)),resources:Array.from({length:6},(_,i)=>choice('r'+i))}])assert.throws(()=>grantBatchRequest(values));
});
test('labels survive remote search but only IDs and supported fields reach the API',()=>{
 const payload=grantBatchRequest({users:[choice('a')],resources:[choice('b')],strategyId:'s',validFrom:'2030-01-01T08:00:00+08:00',approvalId:'forged'});
 assert.equal(payload.validFrom,'2030-01-01T00:00:00.000Z');assert.equal(payload.expiresAt,null);
 assert.equal(payload.strategyId,'s');assert.equal('approvalId' in payload,false);assert.equal(JSON.stringify(payload).includes('name '),false);
});
