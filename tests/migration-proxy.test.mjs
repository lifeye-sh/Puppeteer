import assert from 'node:assert/strict';
import test from 'node:test';
import http from 'node:http';
import {createHash} from 'node:crypto';
import {once} from 'node:events';
import {mkdtemp, mkdir, writeFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {createServer} from 'vite';
import {backendProfile} from '../scripts/frontend-backend-profile.ts';

test('isolated profiles ignore the shared target and reject credential-bearing or non-origin targets', () => {
    const env = {DEV_API_TARGET: 'http://wrong-backend:9999'};
    assert.equal(backendProfile('legacy', env).server.proxy['/api/'].target, 'http://localhost:8088');
    assert.equal(backendProfile('backend', env).server.proxy['/api/'].target, 'http://127.0.0.1:8089');
    assert.equal(backendProfile('development', env).server.proxy['/api/'].target, env.DEV_API_TARGET);
    for (const value of ['http://user:secret@localhost', 'https://host/api', 'file:///tmp', 'http://host?token=x']) {
        assert.throws(() => backendProfile('backend', {BACKEND_API_TARGET: value}));
    }
});

function request(port, path, {method = 'GET', body, host = 'backend.localhost', headers = {}} = {}) {
    return new Promise((resolve, reject) => {
        const req = http.request({hostname: '127.0.0.1', port, path, method, headers: {host, ...headers}}, res => {
            const parts = [];
            res.on('data', chunk => parts.push(chunk));
            res.on('end', () => resolve({status: res.statusCode, headers: res.headers, body: Buffer.concat(parts)}));
        });
        req.on('error', reject);
        req.end(body);
    });
}

test('isolated profile accepts HTTP/2 authority and rejects a foreign host', () => {
    const profile = backendProfile('backend', {});
    let handler;
    profile.plugin.configureServer({middlewares:{use(fn){handler=fn}}});
    for (const [headers,allowed] of [[{':authority':'backend.localhost:5174'},true],[{':authority':'evil.localhost:5174'},false],[{host:'evil.localhost',':authority':'backend.localhost:5174'},false]]) {
        let next=false;
        const res={statusCode:200,setHeader(){},end(){}};
        handler({headers,url:'/',method:'GET'},res,()=>{next=true});
        assert.equal(next,allowed);
        if(!allowed)assert.equal(res.statusCode,421);
    }
});

test('real Vite proxy keeps HTTP, binary, errors, cookies and WS on one target', {timeout: 30000}, async () => {
    const fixtureRoot = await mkdtemp(path.join(tmpdir(), 'next-terminal-proxy-'));
    const secretPath = path.join(fixtureRoot, 'backend/.local/acceptance-account.json');
    await mkdir(path.dirname(secretPath), {recursive:true});
    await writeFile(secretPath, '{"password":"fixture-must-not-be-served"}');
    const sockets = new Set();
    const received = [];
    const upstream = http.createServer((req, res) => {
        received.push({url: req.url, origin: req.headers.origin});
        if (req.url === '/api/unauthorized') {res.writeHead(401); res.end('unauthorized'); return;}
        if (req.url === '/api/download') {
            res.writeHead(206, {'Content-Type': 'application/octet-stream', 'Content-Range': 'bytes 0-2/3'});
            res.end(Buffer.from([0, 128, 255])); return;
        }
        if (req.url === '/api/login') {
            res.setHeader('Set-Cookie', 'session=fixture; Domain=internal.example; Path=/; HttpOnly; SameSite=Lax');
        }
        req.pipe(res);
    });
    upstream.on('connection', socket => {sockets.add(socket); socket.on('close', () => sockets.delete(socket));});
    upstream.on('upgrade', (req, socket) => {
        received.push({url: req.url, origin: req.headers.origin});
        const accept = createHash('sha1').update(req.headers['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
        socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
        socket.write(Buffer.from([0x81, 2, 0x6f, 0x6b]));
    });
    let vite;
    try {
        upstream.listen(0, '127.0.0.1'); await once(upstream, 'listening');
        const profile = backendProfile('backend', {BACKEND_API_TARGET: `http://127.0.0.1:${upstream.address().port}`});
        vite = await createServer({root: fixtureRoot, configFile: false, plugins: [profile.plugin], server: {...profile.server, port: 0, open: false, watch: null}, logLevel: 'silent'});
        await vite.listen();
        const port = vite.httpServer.address().port;
        const origin = 'http://backend.localhost:5174';
        for (const secretURL of ['/backend/.local/acceptance-account.json', '/backend/.local/acceptance-account.json?raw',
            '/@fs/' + secretPath.replaceAll('\\', '/')]) {
            const denied = await request(port, secretURL);
            assert([403,404].includes(denied.status), `${secretURL}: ${denied.status}`);
            assert.doesNotMatch(denied.body.toString(), /fixture-must-not-be-served/);
        }
        const uploaded = Buffer.from([1, 0, 128, 254]);
        const upload = await request(port, '/api/upload?name=a', {method: 'POST', body: uploaded, headers: {origin}});
        assert.deepEqual(upload.body, uploaded);
        assert.equal(received.at(-1).origin, origin);
        assert.equal(received.at(-1).url, '/api/upload?name=a');
        const downloaded = await request(port, '/api/download', {headers: {Range: 'bytes=0-2'}});
        assert.equal(downloaded.status, 206);
        assert.deepEqual(downloaded.body, Buffer.from([0, 128, 255]));
        const login = await request(port, '/api/login');
        assert.doesNotMatch(login.headers['set-cookie'][0], /domain=/i);
        assert.match(login.headers['set-cookie'][0], /HttpOnly/);
        assert.equal((await request(port, '/api/unauthorized')).status, 401);
        assert.equal((await request(port, '/api/login', {host: 'localhost'})).status, 421);
        const configured = JSON.parse((await request(port, '/__migration/profile')).body);
        assert.equal(configured.profile, 'backend');
        await new Promise((resolve, reject) => {
            const req = http.request({hostname: '127.0.0.1', port, path: '/api/access/terminal', headers: {
                Host: 'backend.localhost', Origin: origin, Connection: 'Upgrade', Upgrade: 'websocket',
                'Sec-WebSocket-Version': '13', 'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ==',
            }});
            req.on('error', reject);
            req.on('response', res => reject(new Error(`Expected WS upgrade, got ${res.statusCode}`)));
            req.on('upgrade', (res, socket, head) => {
                assert.equal(res.statusCode, 101);
                assert.equal(received.at(-1).origin, origin);
                const done = chunk => {assert.deepEqual(chunk, Buffer.from([0x81, 2, 0x6f, 0x6b])); socket.destroy(); resolve();};
                if (head.length) done(head); else socket.once('data', done);
            });
            req.end();
        });
    } finally {
        for (const socket of sockets) socket.destroy();
        await vite?.close();
        await new Promise(resolve => upstream.close(resolve));
        assert.equal(path.dirname(fixtureRoot), path.resolve(tmpdir()));
        assert(path.basename(fixtureRoot).startsWith('next-terminal-proxy-'));
        await rm(fixtureRoot, {recursive: true, force: true});
    }
});
