import test from 'node:test';
import assert from 'node:assert/strict';
import {navigationGroups,visibleNavigation,navigationLocation} from '../src/pages/backend/backend-navigation.ts';

const features=Object.fromEntries(navigationGroups.flatMap(g=>g.pages).filter(p=>p.feature).map(p=>[p.feature,{state:'available'}]));
const actions=[...new Set(navigationGroups.flatMap(g=>g.pages).map(p=>p.permission).filter(Boolean))];
test('navigation is two levels with unique destinations and seven groups',()=>{
 const all=navigationGroups.flatMap(g=>g.pages);
 assert.equal(navigationGroups.length,7);
 assert.equal(new Set(all.map(p=>p.path)).size,all.length);
 assert.equal(new Set(navigationGroups.map(g=>g.key)).size,7);
 for(const group of navigationGroups)assert.ok(group.pages.length>0&&group.pages.length<=9);
 const visible=visibleNavigation(actions,features);
 assert.equal(visible.flatMap(g=>g.pages).length,all.length);
});
test('ordinary users do not see management destinations or unavailable capabilities',()=>{
 const visible=visibleNavigation([],{}),pages=visible.flatMap(g=>g.pages);
 assert.ok(pages.some(p=>p.path==='/x-asset'));
 assert.ok(pages.some(p=>p.path==='/notification-preferences'));
 for(const page of pages){assert.equal(page.permission,undefined);assert.equal(page.feature,undefined);}
 assert.ok(!visible.some(g=>['identity','access','connectivity','resources'].includes(g.key)));
});
test('partial permissions do not reveal sibling management pages',()=>{
 const visible=visibleNavigation(['asset.read','policy.read'],features);
 assert.deepEqual(visible.find(g=>g.key==='resources').pages.map(p=>p.path),['/asset']);
 assert.deepEqual(visible.find(g=>g.key==='access').pages.map(p=>p.path),['/access-policy','/strategy','/command-filter','/ip-set']);
 assert.equal(navigationLocation('/user',visible),undefined);
});
test('active groups, authorization aliases and removed capability routes stay consistent',()=>{
 const all=visibleNavigation(actions,features);
 assert.equal(navigationLocation('/x-asset',all).group.key,'workspace');
 assert.equal(navigationLocation('/asset',all).group.key,'resources');
 assert.equal(navigationLocation('/authorised-database-asset',all).page.path,'/authorised-asset');
 assert.equal(navigationLocation('/identity-settings',all).group.key,'identity');
 assert.equal(navigationLocation('/system-event',all).group.key,'operations');
 assert.equal(navigationLocation('/database-asset',visibleNavigation(actions,{})),undefined);
});
