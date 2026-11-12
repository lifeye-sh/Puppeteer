import test from 'node:test';
import assert from 'node:assert/strict';
import {parseExpression} from '@babel/parser';
import {requestMethods} from '../scripts/backend-inventory-methods.mjs';
const methods=code=>requestMethods(parseExpression(code).callee);
test('inventory retains computed and conditional request methods',()=>{
 assert.deepEqual(methods('requests.get("/x")').map(x=>x.method),['GET']);
 assert.deepEqual(methods('requests["delete"]("/x")').map(x=>x.method),['DELETE']);
 assert.deepEqual(methods('requests[editing ? "put" : "post"]("/x")').map(x=>x.method),['PUT','POST']);
 assert.equal(methods('requests[verb]("/x")')[0].method,'DYNAMIC');
 assert.deepEqual(methods('requests.postStream("/x")')[0],{name:'postStream',method:'POST',encoding:'event-stream'});
});
