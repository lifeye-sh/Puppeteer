import test from 'node:test';
import assert from 'node:assert/strict';
import {parseExpression} from '@babel/parser';
import {requestPath} from '../scripts/backend-inventory-paths.mjs';
const render=(source,group)=>requestPath(parseExpression(source,{plugins:['typescript']}),source,group);
test('concatenated URL inventory preserves complete identifiers and suffixes',()=>{
 assert.equal(render("'/admin/ai/assets/'+asset"),'/api/admin/ai/assets/${asset}');
 assert.equal(render("'/admin/ai/profiles/'+editing.id+'/models'"),'/api/admin/ai/profiles/${editing.id}/models');
 assert.equal(render("baseUrl()+'/sessions/'+id+'/recording'"),'/api/sessions/${id}/recording');
});
test('templates retain dynamic expressions while resolving known base helpers',()=>{
 assert.equal(render('`${baseUrl()}/${this.group}/${id}`','assets'),'/api/assets/${id}');
 assert.equal(render('`${baseWebSocketUrl()}/sessions/${id}/ws`'),'/api/sessions/${id}/ws');
 assert.equal(render('url'),'${url}');
 assert.equal(render("'/api'+base+'/files/content'"),'/api${base}/files/content');
 assert.equal(render("'/apiculture'"),'/api/apiculture');
 assert.equal(render("'/x?q='+encodeURIComponent(search)"),'/api/x?q=${encodeURIComponent(search)}');
});
