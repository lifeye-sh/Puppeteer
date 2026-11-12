#!/bin/sh
set -eu
umask 077
mkdir -p /drill/source /drill/repository /drill/log /drill/socket /drill/socket-restore /drill/offline
printf ephemeral-r11-pitr > /drill/.r11-pitr-context
initdb -D /drill/source -A trust > /drill/log/init.log
python3 -c 'import secrets;print(secrets.token_hex(32))' > /drill/offline/repository-key
export PGBACKREST_REPO1_CIPHER_PASS="$(cat /drill/offline/repository-key)"
export PGBACKREST_CONFIG=/drill/pgbackrest.conf
cat > "$PGBACKREST_CONFIG" <<'CONFIG'
[global]
repo1-path=/drill/repository
repo1-cipher-type=aes-256-cbc
repo1-retention-full=2
archive-timeout=60
log-level-console=warn
log-path=/drill/log
lock-path=/drill/lock
spool-path=/drill/spool
[r11]
pg1-path=/drill/source
pg1-socket-path=/drill/socket
pg1-port=5432
pg1-user=postgres
CONFIG
cat >> /drill/source/postgresql.conf <<'CONFIG'
listen_addresses='127.0.0.1'
unix_socket_directories='/drill/socket'
archive_mode=on
archive_timeout=60
archive_command='pgbackrest --stanza=r11 archive-push %p'
CONFIG
pg_ctl -D /drill/source -l /drill/log/source.log -w start
trap 'pg_ctl -D /drill/restore -m immediate stop >/dev/null 2>&1 || true; pg_ctl -D /drill/source -m fast stop >/dev/null 2>&1 || true' EXIT
createdb -h /drill/socket nt_restore_pitr_fixture
NT_PITR_MODE=seed NT_PITR_FIXTURE_DSN='postgres://postgres@127.0.0.1:5432/nt_restore_pitr_fixture?sslmode=disable' /fixtures/r11-application.test -test.run '^TestR11PITRFixture$' -test.v
pgbackrest version
pgbackrest --stanza=r11 stanza-create
pgbackrest --stanza=r11 check
pgbackrest --stanza=r11 --type=full --start-fast backup
sleep 2
psql -h /drill/socket -d nt_restore_pitr_fixture -v ON_ERROR_STOP=1 -c "INSERT INTO r11_pitr_marker(id) VALUES('keep')" >/dev/null
target=$(psql -h /drill/socket -d nt_restore_pitr_fixture -Atc 'SELECT clock_timestamp()')
sleep 1
psql -h /drill/socket -d nt_restore_pitr_fixture -v ON_ERROR_STOP=1 -c "INSERT INTO r11_pitr_marker(id) VALUES('discard')" >/dev/null
last_commit=$(psql -h /drill/socket -d nt_restore_pitr_fixture -Atc 'SELECT max(created_at) FROM r11_pitr_marker')
pgbackrest --stanza=r11 check
archive_at=$(python3 -c 'import time; print(time.time())')
database_bytes=$(psql -h /drill/socket -d nt_restore_pitr_fixture -Atc 'SELECT pg_database_size(current_database())')
pgbackrest --stanza=r11 --output=json info > /drill/log/repository-info.json
started=$(python3 -c 'import time; print(time.time())')
export PGBACKREST_REPO1_CIPHER_PASS="$(cat /drill/offline/repository-key)"
pgbackrest --stanza=r11 --pg1-path=/drill/restore --type=time --target="$target" --target-action=promote --archive-mode=off restore
pg_ctl -D /drill/restore -l /drill/log/restore.log -o '-p 5433 -k /drill/socket-restore' -w start
ready=0
for attempt in $(seq 1 90); do
 if [ "$(psql -h /drill/socket-restore -p 5433 -d nt_restore_pitr_fixture -Atc 'SELECT pg_is_in_recovery()' 2>/dev/null || true)" = f ]; then ready=1; break; fi
 sleep 1
done
if [ "$ready" != 1 ]; then cat /drill/log/restore.log; exit 1; fi
NT_PITR_MODE=verify NT_PITR_FIXTURE_DSN='postgres://postgres@127.0.0.1:5433/nt_restore_pitr_fixture?sslmode=disable' /fixtures/r11-application.test -test.run '^TestR11PITRFixture$' -test.v
finished=$(python3 -c 'import time; print(time.time())')
python3 - "$target" "$last_commit" "$archive_at" "$started" "$finished" "$database_bytes" <<'PY'
import datetime,json,sys
target,last,archived,started,finished,size=sys.argv[1:]
dt=lambda v:datetime.datetime.fromisoformat(v).timestamp()
print(json.dumps({'status':'passed','postgresMajor':18,'databaseBytes':int(size),'scope':'isolated full application database','recoveryTarget':target,'lastCommitArchiveLagSeconds':round(max(0,float(archived)-dt(last)),3),'recoveryPointAgeSeconds':round(max(0,float(started)-dt(target)),3),'rtoSeconds':round(float(finished)-float(started),3),'checks':['target commit present','later commit absent','secret decryption','shared-file digest','old session revoked','old jobs quarantined','new login ready']}))
PY
