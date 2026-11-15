import test from 'node:test';
import assert from 'node:assert/strict';
import {displayedDimension} from '../src/pages/backend/configuration-status-model.ts';
test('failed refresh invalidates previously green evidence without rewriting server state',()=>{
 const value={state:'running',scope:'node',reason:'old observation'};
 assert.equal(displayedDimension(value,true).state,'stale');
 assert.equal(value.state,'running');
 assert.equal(displayedDimension(value,false),value);
 for(const state of ['unknown','not_applicable'])assert.equal(displayedDimension({state,scope:'policy'},true).state,state);
});
