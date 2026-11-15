"""Disposable Docker fixture: verify VNC graphics instructions and shutdown EOF."""
import socket
import sys
from pathlib import Path

marker = Path(sys.argv[1])
sock = socket.create_connection(('guacd', 4822), timeout=10)
sock.settimeout(180)
stream = sock.makefile('rb')

def send(*parts):
    sock.sendall((','.join(str(len(str(p))) + '.' + str(p) for p in parts) + ';').encode())

def receive():
    parts = []
    while True:
        size = b''
        while True:
            char = stream.read(1)
            if not char:
                raise EOFError()
            if char == b'.':
                break
            size += char
            if len(size) > 8:
                raise RuntimeError('Unbounded instruction')
        value = stream.read(int(size)).decode()
        parts.append(value)
        delimiter = stream.read(1)
        if delimiter == b';':
            return parts
        if delimiter != b',':
            raise RuntimeError('Invalid instruction')

send('select', 'vnc')
args = receive()
assert args[0] == 'args'
send('size', 320, 240, 96)
send('audio')
send('video')
send('image', 'image/png', 'image/jpeg')
values = {'hostname': 'vnc-fixture', 'port': '5900', 'read-only': 'true', 'encodings': 'raw'}
send('connect', *[name if name.startswith('VERSION_') else values.get(name, '') for name in args[1:]])
ready = False
painted = False
try:
    while True:
        instruction = receive()
        if instruction[0] == 'ready':
            ready = True
        if instruction[0] in ('png', 'img', 'blob', 'cfill'):
            painted = True
        if instruction[0] == 'sync':
            send(*instruction)
        if instruction[0] == 'error' and not (ready and painted):
            raise RuntimeError('Guacd session error before frame: ' + str(instruction))
        if ready and painted:
            marker.with_suffix('.ready').write_text('VNC frame received')
except (EOFError, ConnectionResetError, BrokenPipeError):
    if not ready or not painted:
        raise RuntimeError('Session ended before receiving graphics')
    marker.with_suffix('.closed').write_text('Existing session closed')
finally:
    sock.close()
