#!/bin/sh
# Disposable container only. No host database directories or production secrets.
set -eu
umask 077
test "${PUPPETEER_DISPOSABLE_WAL_DRILL:-}" = yes
test ! -e /var/lib/postgresql/18/docker/PG_VERSION
mkdir -p /var/lib/postgresql/18/docker /tmp/restore-socket
initdb -D /var/lib/postgresql/18/docker -U puppeteer -A trust >/tmp/init.log
cat >> /var/lib/postgresql/18/docker/postgresql.conf <<'CONFIG'
listen_addresses=''
archive_mode=on
archive_timeout=60
archive_command='pgbackrest-file --stanza=puppeteer archive-push %p'
CONFIG
pg_ctl -D /var/lib/postgresql/18/docker -l /tmp/source.log -w start
trap 'pg_ctl -D /tmp/restore -m immediate stop >/dev/null 2>&1 || true; pg_ctl -D /var/lib/postgresql/18/docker -m fast stop >/dev/null 2>&1 || true' EXIT
pgbackrest-file --stanza=puppeteer stanza-create
pgbackrest-file --stanza=puppeteer check
psql -U puppeteer -d postgres -v ON_ERROR_STOP=1 -c 'CREATE TABLE marker(id text primary key)' >/dev/null
pgbackrest-file --stanza=puppeteer --type=full --start-fast backup
psql -U puppeteer -d postgres -v ON_ERROR_STOP=1 -c "INSERT INTO marker VALUES('keep');" >/dev/null
psql -U puppeteer -d postgres -Atc "SELECT pg_create_restore_point('r17_before_discard')" >/dev/null
psql -U puppeteer -d postgres -v ON_ERROR_STOP=1 -c "INSERT INTO marker VALUES('discard');" >/dev/null
pgbackrest-file --stanza=puppeteer check
pgbackrest-file --stanza=puppeteer --pg1-path=/tmp/restore --type=name --target=r17_before_discard --target-action=promote --archive-mode=off restore
pg_ctl -D /tmp/restore -l /tmp/restore.log -o '-p 5433 -k /tmp/restore-socket' -w start
ready=no
for i in $(seq 1 30); do
  if [ "$(psql -h /tmp/restore-socket -p 5433 -U puppeteer -d postgres -Atc 'SELECT pg_is_in_recovery()' 2>/dev/null || true)" = f ]; then ready=yes; break; fi
  sleep 1
done
if [ "$ready" != yes ]; then cat /tmp/restore.log; exit 1; fi
test "$(psql -h /tmp/restore-socket -p 5433 -U puppeteer -d postgres -Atc 'SELECT string_agg(id, chr(44) ORDER BY id) FROM marker')" = keep
test "$(psql -U puppeteer -d postgres -Atc 'SELECT count(*) FROM marker')" = 2
test "$(psql -h /tmp/restore-socket -p 5433 -U puppeteer -d postgres -Atc 'SHOW archive_mode')" = off
pgbackrest-file version
printf '%s\n' 'PASS: encrypted full backup + WAL, named recovery point, later transaction excluded, original unchanged, restored archive disabled.'
