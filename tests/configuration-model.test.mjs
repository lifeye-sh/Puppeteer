import test from 'node:test';
import assert from 'node:assert/strict';
import {configurationPatch,displayConfiguration,propertyGroup} from '../src/pages/backend/configuration-model.ts';
test('grouped edits exclude hidden, unknown and inherited settings while preserving false and zero',()=>{
 const definitions=['system-name','system-event-saved-limit-days','login-lock-enabled','login-lock-ip-duration','postgres-result-max-rows'].map(key=>({key}));
 const values=Object.assign(Object.create({'system-name':'inherited'}),{'system-event-saved-limit-days':31,'login-lock-enabled':false,'login-lock-ip-duration':0,'postgres-result-max-rows':3,'unknown':'not submitted'});
 assert.deepEqual(configurationPatch(definitions,'locks',values),{'login-lock-enabled':false,'login-lock-ip-duration':0});
 assert.deepEqual(configurationPatch(definitions,'branding',values),{});
 assert.deepEqual(configurationPatch(definitions,'retention',values),{'system-event-saved-limit-days':31});
 assert.equal(propertyGroup('system-event-saved-limit-days'),'retention');
});
test('history display distinguishes missing, empty, false, zero and forever',()=>{
 const p={key:'login-lock-ip-duration',unit:'分钟'};
 assert.equal(displayConfiguration(0,p),'0 分钟');assert.equal(displayConfiguration(false,p),'关闭');
 assert.equal(displayConfiguration(undefined,p),'此版本未包含');assert.equal(displayConfiguration('',p),'未设置');
 assert.equal(displayConfiguration(-1,{key:'access-log-saved-limit-days',unit:'天'}),'永久保留');
 assert.equal(displayConfiguration('image-id',{key:'system-logo',unit:''}),'自定义 Logo');
 assert.equal(displayConfiguration(false,{key:'disable-password-login',unit:''}),'允许密码登录');
 assert.equal(displayConfiguration(true,{key:'disable-password-login',unit:''}),'已禁用密码登录');
});
test('resource defaults have a separate group and keep byte precision',()=>{
 const definitions=['user-default-storage-size','user-client-cert-valid-days','postgres-result-max-rows'].map(key=>({key}));
 assert.deepEqual(configurationPatch(definitions,'resources',{'user-default-storage-size':0,'user-client-cert-valid-days':90,'postgres-result-max-rows':3}),{'user-default-storage-size':0,'user-client-cert-valid-days':90});
 assert.equal(displayConfiguration(5368709120,{key:'user-default-storage-size',unit:'Byte'}),'5368709120 Byte（5 GiB）');
 assert.equal(displayConfiguration(1,{key:'user-default-storage-size',unit:'Byte'}),'1 Byte（0 GiB）');
});

test('DML policy edits do not resubmit listener endpoints',()=>{
 const definitions=['db-proxy-block-dml','db-proxy-enabled','db-proxy-addr'].map(key=>({key}));
 assert.deepEqual(configurationPatch(definitions,'databasePolicy',{'db-proxy-block-dml':false,'db-proxy-enabled':true,'db-proxy-addr':':3307'}),{'db-proxy-block-dml':false});
});

test('captcha proof group preserves false without changing password or lock policy',()=>{
 const definitions=['login-captcha-enabled','disable-password-login','login-lock-enabled','http-server-addr','native-rdp-addr'].map(key=>({key}));
 assert.deepEqual(configurationPatch(definitions,'captcha',{'login-captcha-enabled':false,'disable-password-login':true,'login-lock-enabled':false}),{'login-captcha-enabled':false});
 assert.equal(propertyGroup('http-server-addr'),'listeners');
 assert.equal(propertyGroup('website-server-enabled'),'listeners');
 assert.equal(propertyGroup('native-rdp-public-address'),'listeners');
});

test('agent address belongs to resource defaults and read-only groups show current counts',()=>{
 assert.equal(propertyGroup('agent-gateway-endpoint'),'resources');
 assert.equal(propertyGroup('asset-group-tree'),undefined);
 assert.equal(displayConfiguration('[{"id":"a","children":[{"id":"b","children":[]}]}]',{key:'asset-group-tree',unit:''}),'2 个分组（当前共享资源目录）');
 assert.equal(displayConfiguration('[]',{key:'website-group-tree',unit:''}),'0 个分组（当前共享资源目录）');
 assert.equal(displayConfiguration('{}',{key:'website-group-tree',unit:''}),'分组数据不可用');
});
