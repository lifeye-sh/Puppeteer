import {test} from 'node:test';
import assert from 'node:assert/strict';
import {graphicsSize} from '../src/pages/backend/graphics-quality.ts';
test('RDP profiles bound landscape/portrait sizes without upscale or resize drift',()=>{
    for(const [name,width,height] of [['economy',1280,720],['balanced',1920,1080],['sharp',3840,2160]]) {
        assert.deepEqual(graphicsSize(name,3840,2160),{width,height});
        for (const [w,h] of [[1200,2000],[700,500],[3839,2159]]) {
            const size=graphicsSize(name,w,h);
            assert.ok(size.width<=width&&size.height<=height&&size.width<=w&&size.height<=h);
            assert.deepEqual(graphicsSize(name,size.width,size.height),size);
            assert.ok(Math.abs(size.width/size.height-w/h)<0.02);
        }
    }
});
