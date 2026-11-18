#!/usr/bin/env bash
# EL7 x86_64 compatibility library; keeps RPM-managed files and seccomp enabled.
set -Eeuo pipefail
umask 077
mode=${1:---check}
[[ $# -le 1 ]] || { echo "Expected one mode argument" >&2; exit 2; }
case "$mode" in --check|--apply|--rollback|--finish-install) ;; *) echo 'Usage: repair-el7-libseccomp.sh [--check|--apply|--rollback|--finish-install]' >&2; exit 2;; esac
prefix=/opt/puppeteer-compat/libseccomp-2.6.1
conf=/etc/ld.so.conf.d/00-puppeteer-libseccomp.conf
package_sha=235d6fcd1413d96667813cd1b70dcdd6af031cb5dad5f83507b544ed8d62b1f0
package_url=https://github.com/lifeye-sh/Puppeteer/releases/download/beta0.12/puppeteer-el7-libseccomp-2.6.1-amd64.tar.gz
image=postgres:18-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5
die() { echo "ERROR: $*" >&2; exit 1; }
[[ $EUID == 0 && $(uname -m) == x86_64 && $(uname -s) == Linux ]] || die 'Linux x86_64 root is required'
for cmd in rpm runc ldd ldconfig python3 sha256sum curl docker flock install; do command -v "$cmd" >/dev/null || die "Missing command: $cmd"; done
rpm -q --whatprovides /etc/redhat-release | grep -Eq '(centos|redhat|alinux|system)-release.*7' || die 'This helper is restricted to EL7 hosts'
[[ ! -L /opt/puppeteer-compat && ! -L $prefix && ! -L $conf ]] || die 'Compatibility paths must not be symbolic links'
if [[ $mode == --check ]]; then
 runc --version
 rpm -q libseccomp containerd.io docker-ce
 ldd "$(command -v runc)" | grep libseccomp
 echo 'Checks complete. --apply activates an isolated library prefix; --rollback removes its linker selection.'
 exit 0
fi
if [[ $mode == --finish-install ]]; then
 ctl=/opt/puppeteer/puppeteerctl
 [[ -x $ctl && -d /opt/puppeteer/config && ! -L /opt/puppeteer ]] || die 'Expected the existing /opt/puppeteer installation'
 [[ ! -e /opt/puppeteer/installed-version && ! -L /opt/puppeteer/installed-version ]] || die 'Installation already marked complete; use puppeteerctl for normal management'
 project=$("$ctl" config --format json | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["services"]["server"]["image"] == "cloners/puppeteer-server@sha256:d0c3a23525b35b08593e760c4e65d6bf92894cf3afa740be5bdbc97b6fe9a7dd", "Unexpected server image"; print(d["name"])')
 [[ $project == puppeteer ]] || die 'Finish helper is restricted to the default puppeteer project'
 runc --version | grep -q '^libseccomp: 2\.6\.1$' || die 'Run --apply successfully before finishing installation'
 echo '[1/3] Recreating PostgreSQL with the active runtime library; existing volumes are retained'
 if ! "$ctl" up -d --no-deps --no-build --force-recreate --wait --wait-timeout 240 postgres; then
  "$ctl" logs --tail=80 postgres >&2 || true
  die 'PostgreSQL is not healthy; installation remains incomplete. Existing volumes and configuration are retained.'
 fi
 echo '[2/3] Starting the remaining services and checking health'
 "$ctl" up -d --no-build --wait --wait-timeout 240
 "$ctl" exec -T server /app/nt health
 server=$("$ctl" ps -q server)
 [[ $server =~ ^[a-f0-9]{64}$ ]] || die 'Expected one server container'
 expected=cloners/puppeteer-server@sha256:d0c3a23525b35b08593e760c4e65d6bf92894cf3afa740be5bdbc97b6fe9a7dd
 [[ $(docker inspect "$server" --format '{{.Config.Image}}') == "$expected" ]] || die 'Finish helper is restricted to the published beta0.12 server image'
 echo '[3/3] Completing deployment executor enrollment'
 if [[ ! -e /opt/puppeteer/config/deployd.json ]]; then
  helpers=/opt/puppeteer/config/deployment-helpers
  [[ ! -L $helpers ]] || die 'Unexpected helper symlink'
  install -d -m 700 "$helpers"
  docker cp "$server:/app/deployment/." "$helpers/"
  bash "$helpers/enroll-deployd.sh" /opt/puppeteer
 fi
 "$ctl" exec -T server /app/nt health
 systemctl is-active --quiet puppeteer-deployd-puppeteer.service
 printf 'beta0.12\n' > /opt/puppeteer/installed-version
 "$ctl" ps
 echo 'Existing installation completed; use its configured HTTPS address to create the administrator.'
 exit 0
fi
if [[ -d /opt/puppeteer-compat ]]; then
 [[ $(stat -c %u /opt/puppeteer-compat) == 0 ]] || die 'Compatibility directory must be root owned'
 [[ -z $(find /opt/puppeteer-compat -maxdepth 0 -perm /022 -print) ]] || die 'Compatibility directory must not be writable by other users'
fi
install -d -m 755 /opt/puppeteer-compat
exec 9>/opt/puppeteer-compat/.repair.lock
flock -n 9 || die 'Another compatibility operation is running'
if [[ -e $conf ]]; then
 [[ -f $conf && $(cat "$conf") == "$prefix/lib" ]] || die 'Existing linker configuration differs; refusing overwrite'
fi
runc_bin=$(command -v runc)
ldd "$runc_bin" | grep -q 'libseccomp.so.2 =>' || die 'runc does not dynamically link libseccomp'
if [[ $mode == --rollback ]]; then
 [[ -f $conf ]] || die 'Compatibility library is not activated'
 saved=$(mktemp /opt/puppeteer-compat/linker-disabled.XXXXXX)
 mv -- "$conf" "$saved"
 ldconfig
 runc --version
 echo "Linker override removed; library and logs retained. Existing containers unchanged. Saved configuration: $saved"
 exit 0
fi
runc --version
rpm -q libseccomp containerd.io docker-ce
[[ $(docker version --format '{{.Server.Version}}') == 26.1.4 ]] || die 'This helper was prepared for Docker 26.1.4; inspect other versions separately'
[[ $(runc --version | head -n1) == 'runc version 1.1.12' ]] || die 'This helper was verified with runc 1.1.12'
[[ $(docker context inspect --format '{{.Endpoints.docker.Host}}') == unix://* ]] || die 'A local Docker daemon is required'
docker image inspect "$image" >/dev/null || die 'The pinned PostgreSQL image must already be present'
work=$(mktemp -d /opt/puppeteer-compat/repair.XXXXXX)
if [[ ! -d $prefix ]]; then
 echo '[1/4] Downloading and verifying the EL7 compatibility package'
 curl --fail --show-error --location --retry 3 --connect-timeout 20 "$package_url" -o "$work/package.tar.gz"
 printf '%s  %s\n' "$package_sha" "$work/package.tar.gz" | sha256sum -c -
 python3 - "$work/package.tar.gz" "$work" <<'PY'
import tarfile,sys,os
archive,out=sys.argv[1:]
with tarfile.open(archive) as t:
    for m in t.getmembers():
        p=m.name.split('/')
        if not p or p[0]!='package' or any(x in ('','..','.') for x in p) or not (m.isfile() or m.isdir()):
            raise SystemExit('Unexpected archive entry')
    t.extractall(out)
PY
 (cd "$work/package" && sha256sum -c SHA256SUMS)
 install -d -m 755 "$prefix" "$prefix/lib" "$prefix/bin" "$prefix/source"
 install -m 644 "$work/package/lib/libseccomp.so.2.6.1" "$prefix/lib/"
 ln -s libseccomp.so.2.6.1 "$prefix/lib/libseccomp.so.2"
 install -m 755 "$work/package/bin/scmp_sys_resolver" "$prefix/bin/"
 install -m 644 "$work/package/LICENSE" "$work/package/SHA256SUMS" "$prefix/"
 install -m 644 "$work/package/source/libseccomp-2.6.1.tar.gz" "$prefix/source/"
fi
[[ -z $(find "$prefix" -type d -perm /022 -print) ]] || die 'Library directories must not be writable by other users'
[[ -z $(find "$prefix" ! -user root -print) ]] || die 'Library files must be root owned'
(cd "$prefix" && sha256sum -c SHA256SUMS) || die 'Installed compatibility package is incomplete or changed; existing files retained'
echo '[2/4] Checking runc ABI and syscall resolution before activation'
LD_LIBRARY_PATH="$prefix/lib" "$runc_bin" --version > "$work/runc-candidate.txt"
grep -q 'libseccomp: 2.6.1' "$work/runc-candidate.txt" || die 'Candidate library was not loaded'
[[ $(LD_LIBRARY_PATH="$prefix/lib" "$prefix/bin/scmp_sys_resolver" -a x86_64 pwritev2) == 328 ]] || die 'pwritev2 resolution failed'
created=false
if [[ ! -e $conf ]]; then
 printf '%s\n' "$prefix/lib" > "$work/linker.conf"
 install -m 644 "$work/linker.conf" "$conf"
 created=true
fi
rollback_failed_activation() {
 status=$?
 trap - ERR
 if [[ $created == true ]]; then
  mv -- "$conf" "$work/linker.conf.failed"
  ldconfig
 fi
 if [[ -f $work/postgres-probe.log ]]; then tail -n 60 "$work/postgres-probe.log" >&2; fi
 echo "Compatibility verification failed; previous linker selection restored when newly activated. Logs: $work. No project files or volumes were changed." >&2
 exit "$status"
}
trap rollback_failed_activation ERR
ldconfig
runc --version > "$work/runc-active.txt"
grep -q 'libseccomp: 2.6.1' "$work/runc-active.txt"
echo '[3/4] Checking real PostgreSQL initialization in disposable memory storage'
docker run --rm --pull never --network none --user 70:70 --memory 512m --pids-limit 128 \
 --tmpfs /probe:rw,uid=70,gid=70,mode=0700 --entrypoint initdb "$image" -D /probe/data > "$work/postgres-probe.log" 2>&1
trap - ERR
echo '[4/4] Compatibility library activated; PostgreSQL probe passed'
cat "$work/runc-active.txt"
echo "Logs: $work"
echo 'No Docker restart, project restart, image replacement or data deletion was performed.'
echo 'Rollback: bash repair-el7-libseccomp.sh --rollback (affects future process/container starts).'
