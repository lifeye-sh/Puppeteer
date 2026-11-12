#!/bin/sh
set -eu
# Synthetic password for a new keyring in this disposable container only.
export GNOME_KEYRING_CONTROL="$XDG_RUNTIME_DIR/keyring"
printf '%s' 'r14-disposable-fixture-password' | gnome-keyring-daemon --unlock --components=secrets --control-directory="$GNOME_KEYRING_CONTROL" >/tmp/keyring-start.log
export NT_TEST_ISOLATED_KEYRING=1
exec /acceptance/nativeclient.test -test.v -test.timeout=120s
