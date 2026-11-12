import test from 'node:test';
import assert from 'node:assert/strict';
import {safeReturn, rememberReturn, consumeReturn} from '../src/pages/backend/login-return.ts';
const origin = 'https://backend.localhost:5174';
function storage(){const map=new Map();return {getItem:k=>map.get(k)??null,setItem:(k,v)=>map.set(k,v),removeItem:k=>map.delete(k)};}
test('native authorization survives federation redirect once within TTL',()=>{
 const store=storage();const path='/api/oidc/server/authorize?client_id=nt-native-v1&state=public-state';
 rememberReturn('?return='+encodeURIComponent(path),origin,store,1000);
 assert.equal(consumeReturn('',origin,store,2000),path);
 assert.equal(consumeReturn('',origin,store,2001),'/');
 rememberReturn('?return='+encodeURIComponent(path),origin,store,1000);
 assert.equal(consumeReturn('',origin,store,301001),'/');
});
test('return rejects other origins, credentials, paths and malformed storage',()=>{
 for(const value of ['//evil.example/api/oidc/server/authorize','https://user:secret@backend.localhost:5174/api/oidc/server/authorize','/admin/users','javascript:alert(1)'])assert.equal(safeReturn(value,origin),'/');
 const store=storage();store.setItem('nt-login-return','null');assert.equal(consumeReturn('',origin,store),'/');
 store.setItem('nt-login-return',JSON.stringify({path:'//evil.example',expires:2000}));assert.equal(consumeReturn('',origin,store,1000),'/');
});
