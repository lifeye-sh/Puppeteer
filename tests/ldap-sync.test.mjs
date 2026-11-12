import test from 'node:test';
import assert from 'node:assert/strict';
import {ldapPreviewBody,ldapApplyBody} from '../src/pages/backend/ldap-sync-model.ts';

test('LDAP apply excludes retained preview fields rejected by the strict backend',()=>{
 const state={password:'synthetic-proof',totp:'123456',limit:1000,departmentId:'department',disableMissing:true};
 const apply=ldapApplyBody(state,'single-use-preview',['stable-subject']);
 assert.equal('limit' in apply,false);
 assert.deepEqual(Object.keys(apply).sort(),['password','totp','preview','subjects','departmentId','disableMissing'].sort());
 assert.equal(apply.disableMissing,true);
 assert.equal(apply.preview,'single-use-preview');
 const preview=ldapPreviewBody(state);
 assert.deepEqual(Object.keys(preview).sort(),['password','totp','limit'].sort());
 assert.equal(preview.limit,1000);
});
