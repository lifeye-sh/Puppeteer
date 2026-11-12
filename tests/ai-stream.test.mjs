import test from 'node:test';
import assert from 'node:assert/strict';
import {readEventStream} from '../src/api/core/event-stream.ts';

const response = text => {
 const bytes = new TextEncoder().encode(text); let at = 0;
 return new Response(new ReadableStream({pull(c) {if(at===bytes.length)c.close();else c.enqueue(bytes.slice(at,++at));}}),{headers:{'Content-Type':'text/event-stream'}});
};
test('SSE reconstructs split Chinese bytes and complete snapshot frames',async()=>{
 const got=[];
 await readEventStream(response('event: turn\ndata: {"output":"你好","revision":1}\n\nevent: done\ndata: {"output":"你好世界","revision":2}\n\n'),(event,value)=>got.push({event,value}));
 assert.deepEqual(got.map(x=>x.value.output),['你好','你好世界']);
 assert.equal(got.at(-1).event,'done');
});
test('truncated response or explicit error cannot appear complete',async()=>{
 for(const text of ['event: turn\ndata: {"output":"half"}\n\n','event: done\ndata: {}','event: error\ndata: {"message":"save failed"}\n\n']){
  await assert.rejects(readEventStream(response(text),()=>{}));
 }
});

test('diagnostics use their own completion event and disconnected message',async()=>{
 const options={completionEvent:'complete',disconnectMessage:'诊断连接提前断开'};
 const got=[];
 await readEventStream(response('event: sample\ndata: {"sequence":1,"ok":true}\n\nevent: complete\ndata: {"sent":1}\n\n'),(event,data)=>got.push({event,data}),options);
 assert.equal(got.at(-1).event,'complete');
 await assert.rejects(readEventStream(response('event: sample\ndata: {"sequence":1}\n\n'),()=>{},options),/诊断连接提前断开/);
 await assert.rejects(readEventStream(response('event: done\ndata: {}\n\n'),()=>{},options),/诊断连接提前断开/);
});
