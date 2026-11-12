#!/bin/sh
set -eu
ssh-keygen -A >/dev/null 2>&1
mkdir -p /run/sshd
printf 'root:%s\n' "$(cat /fixture/admin-password)" | chpasswd
exec /usr/sbin/sshd -D -e
