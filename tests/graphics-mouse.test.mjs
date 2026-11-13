import {test} from 'node:test';
import assert from 'node:assert/strict';
import {coalesceMouse} from '../src/pages/backend/graphics-mouse.ts';
test('motion coalesces without crossing buttons/keys or surviving disconnect',()=>{
 const sent=[],scheduled=new Set();
 const moves=coalesceMouse(v=>sent.push(v),fn=>{scheduled.add(fn);return ()=>scheduled.delete(fn)});
 for(let i=0;i<100;i++)moves.move(i);
 assert.equal(scheduled.size,1);
 [...scheduled][0]();
 assert.deepEqual(sent,[99]);
 moves.move(100);moves.flush();sent.push('button-down');
 moves.move(101);moves.flush();sent.push('key-up');
 assert.deepEqual(sent,[99,100,'button-down',101,'key-up']);
 moves.move(102);moves.clear();moves.flush();
 assert.equal(scheduled.size,0);assert.equal(sent.length,5);
});
