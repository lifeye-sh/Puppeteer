#!/usr/bin/env bash
set -Eeuo pipefail
[[ $INSTALLER_TEST_PROJECT =~ ^puppeteer-install-test-[0-9]+$ ]]
[[ $INSTALLER_TEST_PARENT == /var/lib/docker/volumes/puppeteer-install-test-*'/_data' ]]
target="$INSTALLER_TEST_PARENT/install"
core_target="$INSTALLER_TEST_PARENT/core"
script=/source/scripts/install.sh
cleanup() { local result=$?; if [[ -x $core_target/puppeteerctl ]]; then "$core_target/puppeteerctl" down -v; fi; if [[ -x $target/puppeteerctl ]]; then if ((result != 0)); then "$target/puppeteerctl" logs --tail=20 web guacd || true; fi; "$target/puppeteerctl" down -v; fi; }
trap cleanup EXIT
bash -n "$script"
if bash "$script" --host 'invalid/path' --dir "$target" > /tmp/invalid.log 2>&1; then exit 1; fi
[[ ! -e $target ]]
bash "$script" --host localhost --port 18447 --bind 127.0.0.1 --dir "$target" --project "$INSTALLER_TEST_PROJECT"
[[ $(stat -c '%a:%u' "$target/config/secrets/database_password") == 400:70 ]]
[[ $(stat -c '%a:%u' "$target/config/secrets/vault_key") == 400:10001 ]]
[[ $(stat -c '%a:%u' "$target/config/tls/server.key") == 400:101 ]]
before=$(sha256sum "$target/config/secrets/"* "$target/config/tls/"*)
if bash "$script" --host localhost --dir "$target" --project "$INSTALLER_TEST_PROJECT" >/tmp/repeat.log 2>&1; then exit 1; fi
[[ $before == "$(sha256sum "$target/config/secrets/"* "$target/config/tls/"*)" ]]
if bash "$script" --host localhost --dir "$INSTALLER_TEST_PARENT/other" --project "$INSTALLER_TEST_PROJECT" >/tmp/reuse.log 2>&1; then exit 1; fi
[[ ! -e $INSTALLER_TEST_PARENT/other ]]
openssl genpkey -algorithm ED25519 -out /tmp/wrong.key
if bash "$script" --host localhost --dir "$INSTALLER_TEST_PARENT/wrong" --cert "$target/config/tls/server.crt" --key /tmp/wrong.key > /tmp/wrong.log 2>&1; then exit 1; fi
[[ ! -e $INSTALLER_TEST_PARENT/wrong ]]
docker network connect "${INSTALLER_TEST_PROJECT}_control" "$INSTALLER_TEST_HELPER"
curl --fail --silent --show-error --noproxy '*' --cacert "$target/config/tls/server.crt" \
  --connect-to localhost:18447:web:8443 https://localhost:18447/healthz
curl --fail --silent --show-error --noproxy '*' --cacert "$target/config/tls/server.crt" \
  --connect-to localhost:18447:web:8443 https://localhost:18447/setup -o /tmp/setup.html
grep -q '<div id="root"' /tmp/setup.html
docker network disconnect "${INSTALLER_TEST_PROJECT}_control" "$INSTALLER_TEST_HELPER"
bash "$script" --core --host localhost --port 18448 --bind 127.0.0.1 --dir "$core_target" --project "$INSTALLER_TEST_PROJECT-core" --cert "$target/config/tls/server.crt" --key "$target/config/tls/server.key"
[[ $("$core_target/puppeteerctl" config --services | wc -l) -eq 3 ]]
cmp "$target/config/tls/server.crt" "$core_target/config/tls/server.crt"
echo 'INSTALLER ACCEPTANCE PASSED: five healthy services, TLS/setup, secret ownership, repeat/data protection, invalid host/key rejection'
echo 'CORE ACCEPTANCE PASSED: three healthy services, custom port, supplied TLS certificate preserved'
