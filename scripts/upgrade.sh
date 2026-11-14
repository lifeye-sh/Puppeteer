#!/usr/bin/env bash
# Upgrade an install.sh deployment; never regenerate secrets or delete volumes.
set -Eeuo pipefail
umask 077
install_dir=/opt/puppeteer
version=r17-rc11
check_only=0
help() {
  cat <<'HELP'
Usage: sudo bash upgrade.sh [--dir /opt/puppeteer] [--version r17-rc11] [--check]
  --check    Validate deployment and release; do not pull, stop or update services.
Requires Linux amd64, root, Docker Compose v2, curl, python3, tar and flock.
Upgrades server/web only. Keeps existing Compose files, secrets and optional images.
A maintenance window is required: connections are closed during backup and upgrade.
HELP
}
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
while (($#)); do
  case "$1" in
    --help|-h) help; exit 0;;
    --check) check_only=1; shift;;
    --dir|--version)
      (($# >= 2)) || die "Missing value for $1"
      case "$1" in --dir) install_dir=$2;; --version) version=$2;; esac
      shift 2;;
    *) die "Unknown argument: $1";;
  esac
done
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || die 'Linux amd64 is required'
[[ $EUID == 0 ]] || die 'Run with sudo or as root'
[[ $version =~ ^r17-rc[0-9]+$ ]] || die 'Version must be an explicit r17-rcN tag'
[[ $install_dir == /* && $install_dir != / && -d $install_dir ]] || die 'Existing absolute installation directory required'
for cmd in docker curl python3 tar flock realpath mktemp grep sha256sum; do command -v "$cmd" >/dev/null || die "Missing command: $cmd"; done
install_dir=$(realpath -e "$install_dir")
ctl="$install_dir/puppeteerctl"
env_file="$install_dir/config/production.env"
[[ -x $ctl && -f $env_file && ! -L $env_file ]] || die 'Expected install.sh layout: puppeteerctl and config/production.env'
# Serialize upgrades for this installation, including downloads and backups.
exec 9>"$install_dir/.upgrade.lock"
flock -n 9 || die 'Another upgrade is running'
unset PUPPETEER_SERVER_IMAGE PUPPETEER_WEB_IMAGE
docker compose version >/dev/null
"$ctl" config --quiet
work=$(mktemp -d "$install_dir/.upgrade-XXXXXXXX")
phase=preflight
backup=
cleanup() {
  local status=$?
  if ((status != 0)); then
    printf 'Upgrade failed during %s. Backup: %s; work files: %s\n' "$phase" "${backup:-not created}" "$work" >&2
    if [[ $phase == starting ]]; then
      "$ctl" stop "${app_services[@]}" || true
      printf 'Application services stopped. Do not restart an old image against a possibly migrated database. See the recovery guide.\n' >&2
    elif [[ $phase == backup || $phase == switching ]]; then
      printf 'Services may be stopped. Review the backup/error before restarting with puppeteerctl.\n' >&2
    fi
  else
    rm -rf -- "$work"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
printf '[1/6] Checking deployment and release %s\n' "$version"
curl --fail --show-error --silent --location --retry 3 --connect-timeout 20 --max-time 120 \
  "https://raw.githubusercontent.com/lifeye-sh/Puppeteer/$version/deploy/production/dockerhub-$version.json" -o "$work/release.json"
# Parse data, never source an env file or execute release-provided commands.
python3 - "$work/release.json" "$version" "$env_file" "$work" <<'PY'
import json, pathlib, re, sys
manifest, version, env_path, out = sys.argv[1:]
m=json.loads(pathlib.Path(manifest).read_text())
assert m['product']=='Puppeteer' and m['version']==version and m['platform']=='linux/amd64', 'Unexpected release identity/platform'
assert isinstance(m['schemaVersion'],int) and m['schemaVersion']>0, 'Missing schema version'
images={}
for entry in m['images']:
    component=entry['component']
    assert component in ('server','web') and component not in images, 'Unexpected/duplicate component'
    ref=entry['pinnedReference']
    assert re.fullmatch(r'cloners/puppeteer-'+component+r'@sha256:[a-f0-9]{64}',ref), 'Untrusted image reference'
    images[component]=ref
assert set(images)=={'server','web'}, 'Both images required'
text=pathlib.Path(env_path).read_text()
for component,ref in images.items():
    key='PUPPETEER_'+component.upper()+'_IMAGE'
    pattern=r'^'+key+r'=.*$'
    assert len(re.findall(pattern,text,re.M))==1, 'Missing/duplicate '+key
    text=re.sub(pattern,key+'='+ref,text,flags=re.M)
pathlib.Path(out,'production.env').write_text(text)
pathlib.Path(out,'images').write_text(images['server']+'\n'+images['web']+'\n')
pathlib.Path(out,'schema').write_text(str(m['schemaVersion']))
PY
mapfile -t images < "$work/images"
mapfile -t running < <("$ctl" ps --status running --services)
app_services=()
for name in "${running[@]}"; do [[ $name == postgres ]] || app_services+=("$name"); done
for required in server web postgres; do
  printf '%s\n' "${running[@]}" | grep -qx "$required" || die "Required service is not running: $required"
done
server_id=$("$ctl" ps -q server)
[[ $server_id =~ ^[a-f0-9]{12,64}$ ]] || die 'Expected exactly one server container'
current_schema=$("$ctl" exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "SELECT schema_version FROM instance_state WHERE singleton=1"')
[[ $current_schema =~ ^[0-9]+$ ]] || die 'Cannot determine current database schema'
((current_schema <= $(cat "$work/schema"))) || die 'Downgrade to an older database schema is not supported'
# Validate candidate settings using the original wrapper, preserving all overrides.
PUPPETEER_SERVER_IMAGE="${images[0]}" PUPPETEER_WEB_IMAGE="${images[1]}" "$ctl" config --quiet
printf 'Current schema: %s; target schema: %s\nServer: %s\nWeb: %s\n' "$current_schema" "$(cat "$work/schema")" "${images[0]}" "${images[1]}"
if ((check_only)); then printf 'Preflight passed. No services or images changed.\n'; exit 0; fi
printf '[2/6] Pulling pinned images before downtime\n'
docker pull "${images[0]}"
docker pull "${images[1]}"
backup="$install_dir/backups/upgrade-$(date -u +%Y%m%dT%H%M%SZ)-$$"
mkdir -p -m 700 "$backup"
cp "$work/release.json" "$backup/target-release.json"
printf '%s\n' "${running[@]}" > "$backup/running-services.txt"
"$ctl" config --images > "$backup/previous-images.txt"
printf '[3/6] Stopping application services and backing up data\n'
phase=backup
"$ctl" stop "${app_services[@]}"
# Archive configuration (including custom files and secrets), excluding backups/work.
tar --exclude='./backups' --exclude='./.upgrade-*' --exclude='./.upgrade.lock' -C "$install_dir" -cpf "$backup/configuration.tar" .
"$ctl" exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$backup/database.dump"
"$ctl" exec -T postgres pg_restore --list < "$backup/database.dump" > "$backup/database-contents.txt"
# Includes nested storageconfig mount; use the old image already on this host.
old_image=$(docker inspect --format '{{.Image}}' "$server_id")
docker run --rm --network none --read-only --user 0 --security-opt no-new-privileges \
  --volumes-from "$server_id:ro" --entrypoint tar "$old_image" \
  -C /var/lib/next-terminal -cpf - . > "$backup/appdata.tar"
tar -tf "$backup/appdata.tar" > "$backup/appdata-contents.txt"
( cd "$backup" && sha256sum configuration.tar database.dump appdata.tar > SHA256SUMS )
printf '[4/6] Updating image settings (existing secrets and Compose files retained)\n'
phase=switching
# Atomic replacement in the same directory, preserving permissions and owner.
cp -p "$env_file" "$env_file.upgrade"
cat "$work/production.env" > "$env_file.upgrade"
mv -f -- "$env_file.upgrade" "$env_file"
printf '[5/6] Starting previously running services and waiting for health\n'
phase=starting
"$ctl" up -d --no-build --no-deps --wait --wait-timeout 240 "${app_services[@]}"
printf '[6/6] Verifying application readiness\n'
for index in 0 1; do
  service=server; ((index == 0)) || service=web
  container=$("$ctl" ps -q "$service")
  actual=$(docker inspect --format '{{.Config.Image}}' "$container")
  [[ $actual == "${images[$index]}" ]] || die "Unexpected running image for $service"
done
"$ctl" exec -T server /app/nt health
"$ctl" ps
phase=complete
printf '%s\n' "$version" > "$install_dir/installed-version"
printf 'Upgrade complete: %s\nBackup retained: %s\n' "$version" "$backup"
