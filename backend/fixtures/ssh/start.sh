#!/bin/sh
set -eu
mkdir -p /hostkeys /home/tester/.ssh /run/sshd
if [ ! -f /hostkeys/ssh_host_ed25519_key ]; then
  ssh-keygen -q -t ed25519 -N '' -f /hostkeys/ssh_host_ed25519_key
fi
# Unlock the account with an unknown random password; SSH password authentication stays disabled.
printf 'tester:%s\n' "$(head -c 32 /dev/urandom | base64)" | chpasswd
cp /fixture/authorized_keys /home/tester/.ssh/authorized_keys
chown -R tester:tester /home/tester/.ssh
chmod 700 /home/tester/.ssh
chmod 600 /home/tester/.ssh/authorized_keys
exec /usr/sbin/sshd -D -e
