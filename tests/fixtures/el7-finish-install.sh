#!/usr/bin/env bash
# Run only in a disposable container; never mount the host /opt or Docker socket.
set -euo pipefail
helper=${1:?helper path required}
test ! -e /opt/puppeteer
mkdir -p /opt/puppeteer/config /test-bin
export PATH=/test-bin:$PATH
export SERVER_IMAGE=cloners/puppeteer-server@sha256:d0c3a23525b35b08593e760c4e65d6bf92894cf3afa740be5bdbc97b6fe9a7dd
cat > /test-bin/rpm <<'SH'
#!/bin/bash
echo centos-release-7
SH
cat > /test-bin/runc <<'SH'
#!/bin/bash
echo "libseccomp: ${TEST_LIBRARY:-2.6.1}"
SH
cat > /test-bin/docker <<'SH'
#!/bin/bash
[[ $1 == inspect ]] || exit 9
echo "$SERVER_IMAGE"
SH
cat > /test-bin/systemctl <<'SH'
#!/bin/bash
echo systemctl >> /tmp/finish-events
[[ ${TEST_FAIL:-} != executor ]]
SH
cat > /opt/puppeteer/puppeteerctl <<'SH'
#!/bin/bash
set -eu
case "$*" in
 'config --format json') printf '{"name":"puppeteer","services":{"server":{"image":"%s"}}}\n' "$SERVER_IMAGE";;
 'up -d --no-deps --no-build --force-recreate --wait --wait-timeout 240 postgres')
  echo recreate-postgres >> /tmp/finish-events
  [[ ${TEST_FAIL:-} != postgres ]];;
 'up -d --no-build --wait --wait-timeout 240')
  grep -qx recreate-postgres /tmp/finish-events
  echo start-stack >> /tmp/finish-events
  [[ ${TEST_FAIL:-} != stack ]];;
 'logs --tail=80 postgres') echo postgres-logs >> /tmp/finish-events;;
 'exec -T server /app/nt health') echo health >> /tmp/finish-events; [[ ${TEST_FAIL:-} != health ]];;
 'ps -q server') printf '%064d\n' 1;;
 ps) echo ps >> /tmp/finish-events;;
 *) echo "Unexpected command: $*" >&2; exit 9;;
esac
SH
chmod +x /test-bin/* /opt/puppeteer/puppeteerctl
touch /opt/puppeteer/config/deployd.json
for failure in postgres stack health executor; do
 export TEST_FAIL=$failure
 : > /tmp/finish-events
 if bash "$helper" --finish-install > /tmp/finish-output 2>&1; then
  echo "Unexpected success: $failure"; exit 1
 fi
 test ! -e /opt/puppeteer/installed-version
 if [[ $failure == postgres ]]; then
  diff -u <(printf 'recreate-postgres\npostgres-logs\n') /tmp/finish-events
  grep -q 'Existing volumes and configuration are retained' /tmp/finish-output
 fi
done
unset TEST_FAIL
export TEST_LIBRARY=2.3.1
: > /tmp/finish-events
if bash "$helper" --finish-install > /tmp/finish-output 2>&1; then exit 1; fi
test ! -s /tmp/finish-events
test ! -e /opt/puppeteer/installed-version
unset TEST_LIBRARY
bash "$helper" --finish-install
diff -u <(printf 'recreate-postgres\nstart-stack\nhealth\nhealth\nsystemctl\nps\n') /tmp/finish-events
grep -qx beta0.12 /opt/puppeteer/installed-version
: > /tmp/finish-events
if bash "$helper" --finish-install > /tmp/finish-output 2>&1; then exit 1; fi
test ! -s /tmp/finish-events
echo 'PASS: recreation order, database/stack/health/executor failures, inactive library and completed-install guards. Container operations are mocked.'
