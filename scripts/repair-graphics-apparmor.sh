#!/usr/bin/env bash
# Repair the default /opt/puppeteer installer deployment without replacing data.
set -Eeuo pipefail
umask 077
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
[[ $EUID == 0 && $(uname -s) == Linux ]] || die 'Run as root on the Docker Linux host'
for cmd in docker apparmor_parser curl sha256sum; do command -v "$cmd" >/dev/null || die "Missing command: $cmd"; done
[[ -z ${DOCKER_HOST:-} && -z ${DOCKER_CONTEXT:-} && $(docker context show) == default ]] || die 'Use the local default Docker context'
[[ $(docker context inspect default --format '{{.Endpoints.docker.Host}}') == unix:///var/run/docker.sock ]] || die 'Unexpected Docker endpoint'
docker info --format '{{json .SecurityOptions}}' | grep -q apparmor || die 'Docker does not report AppArmor enabled'
ctl=/opt/puppeteer/puppeteerctl
overlay=/opt/puppeteer/graphics-apparmor.yaml
[[ -x $ctl && -s /opt/puppeteer/config/production.env ]] || die 'Existing installation not found'
grep -Fq -- '-f /opt/puppeteer/services.yaml ' "$ctl" || die 'Expected installer wrapper not found; no changes made'
[[ $(docker inspect puppeteer-guacd-1 --format '{{index .Config.Labels "com.docker.compose.project"}}') == puppeteer ]] || die 'Expected project not found'
work=$(mktemp -d)
trap 'rm -f "$work/profile" "$work/ctl"; rmdir "$work"' EXIT
curl -fsSL --retry 3 https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/deploy/production/puppeteer-rdp-storage.apparmor -o "$work/profile"
printf '770a612f9a830d14236f9a8e9c158681d799665c39baf92894160e46becc8e16  %s\n' "$work/profile" | sha256sum -c -
apparmor_parser --skip-kernel-load --skip-cache "$work/profile"
install -m 644 "$work/profile" /etc/apparmor.d/puppeteer-rdp-storage
apparmor_parser -r /etc/apparmor.d/puppeteer-rdp-storage
cat > "$overlay" <<'YAML'
services:
  guacd:
    security_opt:
      - apparmor=puppeteer-rdp-storage
YAML
chmod 600 "$overlay"
if ! grep -Fq "$overlay" "$ctl"; then
  sed 's| "\$@"$| -f /opt/puppeteer/graphics-apparmor.yaml "$@"|' "$ctl" > "$work/ctl"
  grep -Fq "$overlay" "$work/ctl" || die 'Could not update wrapper safely'
  bash -n "$work/ctl"
  bash "$work/ctl" config --quiet
  cp -p "$ctl" "$ctl.before-apparmor-$(date +%Y%m%d%H%M%S)"
  install -m 700 "$work/ctl" "$ctl"
fi
"$ctl" config --quiet
"$ctl" up -d --no-build --no-deps --force-recreate --wait --wait-timeout 150 guacd
"$ctl" exec -T guacd /usr/local/bin/nt-rdp-storage health
docker inspect puppeteer-guacd-1 --format 'AppArmor={{.AppArmorProfile}} Health={{.State.Health.Status}}'
printf 'Graphics repaired. Existing configuration, keys and volumes were preserved.\n'
