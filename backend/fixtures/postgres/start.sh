#!/bin/sh
set -eu
cp /fixture/server.pem /tmp/r16-server.pem
cp /fixture/server.key /tmp/r16-server.key
chown postgres:postgres /tmp/r16-server.pem /tmp/r16-server.key
chmod 600 /tmp/r16-server.key
exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/r16-server.pem -c ssl_key_file=/tmp/r16-server.key -c password_encryption=scram-sha-256
