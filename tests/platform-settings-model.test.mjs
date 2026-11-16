import test from 'node:test';
import assert from 'node:assert/strict';
import {categoryForSection,settingsDestination} from '../src/pages/backend/platform-settings-model.ts';
test('dependencies remain separate from business policy and host deployment',()=>{
 for(const section of ['aiModel','databasePolicy','forward','oauthServer','mail','ldap','passkey'])assert.equal(categoryForSection(section),'dependencies');
 for(const section of ['ai','postgres','recording','login','oauthLifetime'])assert.equal(categoryForSection(section),'policies');
 for(const section of ['deployment','components','drive','listeners','mp4','agent-repository','wal','web-access'])assert.equal(categoryForSection(section),'deployment');
 for(const section of ['publish','runtime','license'])assert.equal(categoryForSection(section),'status');
});
test('existing section links resolve to their new canonical routes',()=>{
 assert.equal(settingsDestination('mail'),'/integration-settings?section=mail');
 assert.equal(settingsDestination('resourceBudget'),'/deployment-settings?section=resourceBudget');
 assert.equal(settingsDestination('drive'),'/deployment-settings?section=drive');
 assert.equal(settingsDestination('components'),'/deployment-settings?section=components');
 assert.equal(settingsDestination('listeners'),'/deployment-settings?section=listeners');
 assert.equal(settingsDestination('ai'),'/policy-settings?section=ai');
 assert.equal(settingsDestination('publish'),'/configuration-status?section=publish');
 assert.equal(settingsDestination('unknown'),'/system-status');
 for(const key of ['unknown','constructor','toString','__proto__']){assert.equal(categoryForSection(key),undefined);assert.equal(settingsDestination(key),'/system-status');}
});
