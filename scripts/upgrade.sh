#!/usr/bin/env bash
# Upgrade an install.sh deployment; never regenerate secrets or delete volumes.
set -Eeuo pipefail
umask 077
install_dir=/opt/puppeteer
version=r17-rc13
check_only=0
enable_database_proxy=0
database_host=
database_bind=127.0.0.1
help() {
  cat <<'HELP'
Usage: sudo bash upgrade.sh [--dir /opt/puppeteer] [--version r17-rc13] [--check]
  --check    Validate deployment and release; do not pull, stop or update services.
  --enable-database-proxy     Add missing MySQL/PostgreSQL listeners and mappings
  --database-proxy-host HOST  Certificate hostname (default: host from HTTPS origin)
  --database-proxy-bind IPV4  Bind for new mappings (default 127.0.0.1); preserves existing mappings
Requires Linux amd64 with systemd, root, Docker Compose v2, curl, python3, tar and flock.
Upgrades server/web and packaged graphics/renderer images. Preserves disabled services and custom images.
A maintenance window is required: connections are closed during backup and upgrade.
HELP
}
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
while (($#)); do
  case "$1" in
    --help|-h) help; exit 0;;
    --check) check_only=1; shift;;
    --enable-database-proxy) enable_database_proxy=1; shift;;
    --dir|--version|--database-proxy-host|--database-proxy-bind)
      (($# >= 2)) || die "Missing value for $1"
      case "$1" in --dir) install_dir=$2;; --version) version=$2;; --database-proxy-host) database_host=$2;; --database-proxy-bind) database_bind=$2;; esac
      shift 2;;
    *) die "Unknown argument: $1";;
  esac
done
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || die 'Linux amd64 is required'
[[ $EUID == 0 ]] || die 'Run with sudo or as root'
[[ $version =~ ^r17-rc[0-9]+$ ]] || die 'Version must be an explicit r17-rcN tag'
[[ $install_dir == /* && $install_dir != / && -d $install_dir ]] || die 'Existing absolute installation directory required'
for cmd in docker curl python3 tar flock realpath mktemp grep sha256sum systemctl; do command -v "$cmd" >/dev/null || die "Missing command: $cmd"; done
install_dir=$(realpath -e "$install_dir")
ctl="$install_dir/puppeteerctl"
env_file="$install_dir/config/production.env"
[[ -x $ctl && -f $env_file && ! -L $env_file ]] || die 'Expected install.sh layout: puppeteerctl and config/production.env'
# Serialize upgrades for this installation, including downloads and backups.
exec 9>"$install_dir/.upgrade.lock"
flock -n 9 || die 'Another upgrade is running'
unset PUPPETEER_SERVER_IMAGE PUPPETEER_WEB_IMAGE PUPPETEER_GRAPHICS_IMAGE PUPPETEER_RENDERER_IMAGE
docker compose version >/dev/null
"$ctl" config --quiet
work=$(mktemp -d "$install_dir/.upgrade-XXXXXXXX")
phase=preflight
backup=
deployd_unit=
deployd_stopped=0
cleanup() {
  local status=$?
  if ((status != 0)); then
    printf 'Upgrade failed during %s. Backup: %s; work files: %s\n' "$phase" "${backup:-not created}" "$work" >&2
    if ((deployd_stopped)); then
      printf 'Deployment executor remains stopped until upgrade recovery is complete: %s\n' "$deployd_unit" >&2
    fi
    if [[ $phase == starting ]]; then
      "$ctl" stop "${app_services[@]}" || true
      printf 'Application services stopped. Do not restart an old image against a possibly migrated database. See the recovery guide.\n' >&2
    elif [[ $phase == backup || $phase == switching ]]; then
      printf 'Services may be stopped. Review the backup/error before restarting with puppeteerctl.\n' >&2
    fi
  else
    if ((deployd_stopped)); then systemctl start "$deployd_unit"; fi
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
    assert component in ('server','web','graphics','renderer') and component not in images, 'Unexpected/duplicate component'
    ref=entry['pinnedReference']
    assert re.fullmatch(r'cloners/puppeteer-'+component+r'@sha256:[a-f0-9]{64}',ref), 'Untrusted image reference'
    images[component]=ref
assert {'server','web'} <= set(images), 'Server and web images required'
text=pathlib.Path(env_path).read_text()
for component,ref in images.items():
    key='PUPPETEER_'+component.upper()+'_IMAGE'
    pattern=r'^'+key+r'=.*$'
    matches=re.findall(pattern,text,re.M)
    assert len(matches)<=1 and (matches or component not in ('server','web')), 'Missing/duplicate '+key
    if component in ('graphics','renderer') and matches:
        old=matches[0].split('=',1)[1].strip().strip('\"\'')
        if not re.match(r'(?:cloners/puppeteer-'+component+r'|puppeteer/'+component+r')[:@]',old):
            print('Preserving custom '+component+' image; verify compatibility with the target release.')
            continue
    text=re.sub(pattern,key+'='+ref,text,flags=re.M) if matches else text.rstrip()+'\n'+key+'='+ref+'\n'
pathlib.Path(out,'production.env').write_text(text)
pathlib.Path(out,'images').write_text('\n'.join(images[c] for c in ('server','web','graphics','renderer') if c in images)+'\n')
pathlib.Path(out,'image-map.json').write_text(json.dumps(images))
pathlib.Path(out,'schema').write_text(str(m['schemaVersion']))
PY
# Resolved managed Compose does not use env-file interpolation. Apply published
# component images to standard recipes, including disabled shared-drive recipes.
cat > "$work/update-images.py" <<'PYIMAGES'
import json, pathlib, re
def update_images(model, manifest):
    for component, service in [('server','server'),('web','web'),('graphics','guacd'),('renderer','mp4-renderer')]:
        ref = manifest.get(component)
        if not ref: continue
        targets = [model.get('services', {}).get(service)]
        if component == 'graphics': targets.append(model.get('x-puppeteer-drive', {}).get('recipe'))
        for item in targets:
            if not isinstance(item, dict): continue
            old = item.get('image', '')
            if component in ('server','web') or re.match(r'(?:cloners/puppeteer-'+component+r'|puppeteer/'+component+r')[:@]', old):
                item['image'] = ref
    return model
PYIMAGES
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
candidate_ctl="$ctl"
if ((enable_database_proxy)); then
  "$ctl" config --format json > "$work/compose.json"
  python3 - "$work" "$ctl" "$install_dir" "$database_host" "$database_bind" <<'PYPROXY'
import ipaddress,json,pathlib,re,shlex,sys
from urllib.parse import urlsplit
work,ctl,install,host,bind=sys.argv[1:]
w=pathlib.Path(work); root=pathlib.Path(install)
ipaddress.IPv4Address(bind)
model=json.loads((w/'compose.json').read_text()); server=model['services']['server']
env=server.get('environment') or {}; ports=server.get('ports') or []
if not host:
    text=(w/'production.env').read_text()
    origins=re.findall(r'^PUPPETEER_ORIGIN=(.+)$',text,re.M)
    assert len(origins)==1, 'Set --database-proxy-host explicitly'
    host=urlsplit(origins[0].strip().strip('\"\'')).hostname or ''
assert re.fullmatch(r'[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?',host) and '..' not in host, 'Invalid proxy hostname'
existing_host=env.get('NT_DATABASE_PROXY_NAME')
if existing_host:
    if sys.argv[4] and host!=existing_host: raise ValueError('Existing proxy hostname differs; preserve it or update the certificate explicitly')
    host=existing_host
missing={k:v for k,v in {'NT_DATABASE_PROXY_ADDR':':13306','NT_POSTGRES_PROXY_ADDR':':15432','NT_DATABASE_PROXY_NAME':host}.items() if not env.get(k)}
add_ports=[]
for key,port in [('NT_DATABASE_PROXY_ADDR',13306),('NT_POSTGRES_PROXY_ADDR',15432)]:
    address=env.get(key) or ':'+str(port)
    if address not in (':'+str(port),'0.0.0.0:'+str(port)):
        raise ValueError('Custom listener '+key+' requires manual mapping review; no changes applied')
    if not any(str(p.get('target'))==str(port) and p.get('protocol','tcp')=='tcp' for p in ports):
        add_ports.append({'target':port,'published':str(port),'host_ip':bind,'protocol':'tcp'})
print('Database proxy hostname: '+host+'; new mappings bind: '+bind+'; existing mappings are preserved.')
if missing or add_ports:
    managed=root/'database-proxy-managed.yaml'
    overlay=json.loads(managed.read_text()) if managed.exists() else {'services':{'server':{}}}
    target=overlay['services']['server']
    target.setdefault('environment',{}).update(missing)
    target.setdefault('ports',[]).extend(add_ports)
    # Only rewrite the installer-generated exec command, preserving every existing -f.
    content=pathlib.Path(ctl).read_text(); lines=content.splitlines()
    command=[i for i,line in enumerate(lines) if line.startswith('exec docker compose ')]
    assert len(command)==1, 'Unsupported puppeteerctl; no changes applied'
    index=command[0]; tokens=shlex.split(lines[index])
    assert tokens[-1]=='$@', 'Unsupported puppeteerctl argument forwarding'
    if str(managed) not in tokens:
        tokens[-1:-1]=['-f',str(managed)]
    lines[index]=shlex.join(tokens[:-1])+' "$@"'
    (w/'puppeteerctl.final').write_text('\n'.join(lines)+'\n')
    # Use a temporary overlay for preflight; do not touch the live deployment.
    candidate=w/'database-proxy-managed.yaml'; candidate.write_text(json.dumps(overlay,indent=2)+'\n')
    tokens=[str(candidate) if t==str(managed) else t for t in tokens]
    lines[index]=shlex.join(tokens[:-1])+' "$@"'
    preview=w/'puppeteerctl'; preview.write_text('\n'.join(lines)+'\n'); preview.chmod(0o700)
print('Existing generated certificates are retained. If their SAN does not cover this host, configure a matching proxy certificate in Platform settings before client access.')
PYPROXY
  if [[ -f $work/puppeteerctl ]]; then candidate_ctl="$work/puppeteerctl"; fi
fi

# Validate candidate settings using the original wrapper, preserving all overrides.
PUPPETEER_SERVER_IMAGE="${images[0]}" PUPPETEER_WEB_IMAGE="${images[1]}" "$candidate_ctl" config --quiet
printf 'Current schema: %s; target schema: %s\nServer: %s\nWeb: %s\n' "$current_schema" "$(cat "$work/schema")" "${images[0]}" "${images[1]}"
if ((check_only)); then printf 'Preflight passed. No services or images changed.\n'; exit 0; fi
if [[ -f $install_dir/config/deployd.json ]]; then
  (( ! enable_database_proxy )) || die 'Managed deployments use the proxy deployment page; do not combine with --enable-database-proxy'
  command -v systemctl >/dev/null || die 'systemctl is required for managed deployments'
  project=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["executor"]["Project"])' "$install_dir/config/deployd.json")
  [[ $project =~ ^[a-z][a-z0-9-]{2,40}$ ]] || die 'Invalid managed project'
  deployd_unit="puppeteer-deployd-$project.service"
fi
printf '[2/6] Pulling pinned images before downtime\n'
for image in "${images[@]}"; do docker pull "$image"; done
# Schema 110 introduces the canonical application root. Resolve the existing
# root for backup, and prepare a volume-preserving candidate before downtime.
appdata_root=$("$ctl" config --format json | python3 -c 'import json,sys,posixpath; m=json.load(sys.stdin); p=m["services"]["server"].get("environment",{}).get("NT_RUNTIME_PROFILE", ""); root=p.removesuffix("/runtime/active.json"); assert p.endswith("/runtime/active.json") and root.startswith("/var/lib/") and root.count("/")==3, "Unknown application data root"; print(root)')
if (( $(cat "$work/schema") >= 110 )) && [[ -z $deployd_unit ]]; then
  staged_container=$(docker create "${images[0]}")
  docker cp "$staged_container:/app/deployment/enroll-deployd.py" "$work/enroll-deployd.py" || { docker rm "$staged_container" >/dev/null; die 'Target image lacks path migration helper'; }
  docker rm "$staged_container" >/dev/null
  PUPPETEER_SERVER_IMAGE="${images[0]}" PUPPETEER_WEB_IMAGE="${images[1]}" "$candidate_ctl" config --format json > "$work/runtime-source.json"
  python3 - "$work" "$install_dir" <<'PYROOT'
import json,pathlib,runpy,shlex,sys
work,root=map(pathlib.Path,sys.argv[1:]); model=json.loads((work/'runtime-source.json').read_text())
runpy.run_path(str(work/'enroll-deployd.py'))['migrate_application_paths'](model)
runpy.run_path(str(work/'update-images.py'))['update_images'](model,json.loads((work/'image-map.json').read_text()))
def escape(v):
 if isinstance(v,str): return v.replace('$','$$')
 if isinstance(v,list): return [escape(x) for x in v]
 if isinstance(v,dict): return {k:escape(x) for k,x in v.items()}
 return v
(work/'compose-runtime.json').write_text(json.dumps(escape(model)))
command=['docker','compose','--project-directory',str(root),'--project-name',model['name'],'-f',str(root/'compose-runtime.json')]
(work/'puppeteerctl.runtime').write_text('#!/usr/bin/env bash\nset -euo pipefail\nexec '+shlex.join(command)+' "$@"\n')
PYROOT
  docker compose --project-directory "$install_dir" -f "$work/compose-runtime.json" config --quiet
fi
if [[ -n $deployd_unit ]]; then
  # Extract and validate the matching host binary before stopping anything.
  staged_container=$(docker create "${images[0]}")
  if ! docker cp "$staged_container:/app/puppeteer-deployd" "$work/puppeteer-deployd"; then
    docker rm "$staged_container" >/dev/null
    die 'Target image lacks the managed deployment executor; downgrade refused'
  fi
  # The helper only annotates compatible local graphics recipes; it does not start services.
  docker cp "$staged_container:/app/deployment/enroll-deployd.py" "$work/enroll-deployd.py" || { docker rm "$staged_container" >/dev/null; die 'Target image lacks deployment enrollment helper'; }
  docker rm "$staged_container" >/dev/null
  chmod 700 "$work/puppeteer-deployd"
  "$work/puppeteer-deployd" --help >/dev/null 2>&1
  systemctl stop "$deployd_unit"
  deployd_stopped=1
  python3 - "$install_dir/compose-managed.json" "$work/compose-managed.json" "${images[0]}" "${images[1]}" "$work/enroll-deployd.py" "$work" <<'PYMANAGED'
import json, pathlib, sys, runpy
source, target, server, web, helper, work = sys.argv[1:]
model = json.loads(pathlib.Path(source).read_text())
model['services']['server']['image'] = server
model['services']['web']['image'] = web
runpy.run_path(str(pathlib.Path(work,'update-images.py')))['update_images'](model,json.loads(pathlib.Path(work,'image-map.json').read_text()))
helpers = runpy.run_path(helper)
migrate_paths = helpers.get('migrate_application_paths')
if migrate_paths: migrate_paths(model)
enroll_graphics = helpers.get('enroll_graphics')
if enroll_graphics: enroll_graphics(model)
pathlib.Path(target).write_text(json.dumps(model))
PYMANAGED
  docker compose --project-directory "$install_dir" -p "$project" -f "$work/compose-managed.json" config --quiet
fi
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
  -C "$appdata_root" -cpf - . > "$backup/appdata.tar"
tar -tf "$backup/appdata.tar" > "$backup/appdata-contents.txt"
( cd "$backup" && sha256sum configuration.tar database.dump appdata.tar > SHA256SUMS )
printf '[4/6] Updating image settings (existing secrets and Compose files retained)\n'
phase=switching
if [[ -f $work/puppeteerctl.final ]]; then
  cp "$work/database-proxy-managed.yaml" "$install_dir/database-proxy-managed.yaml.upgrade"
  mv -f "$install_dir/database-proxy-managed.yaml.upgrade" "$install_dir/database-proxy-managed.yaml"
  cp -p "$ctl" "$ctl.upgrade"
  cat "$work/puppeteerctl.final" > "$ctl.upgrade"
  mv -f "$ctl.upgrade" "$ctl"
fi
# Atomic replacement in the same directory, preserving permissions and owner.
cp -p "$env_file" "$env_file.upgrade"
cat "$work/production.env" > "$env_file.upgrade"
mv -f -- "$env_file.upgrade" "$env_file"
if [[ -f $work/puppeteerctl.runtime ]]; then
  install -m 600 "$work/compose-runtime.json" "$install_dir/compose-runtime.json"
  install -m 700 "$work/puppeteerctl.runtime" "$ctl"
fi
if [[ -n $deployd_unit ]]; then
  cp -p "$install_dir/compose-managed.json" "$install_dir/compose-managed.json.upgrade"
  cat "$work/compose-managed.json" > "$install_dir/compose-managed.json.upgrade"
  mv -f "$install_dir/compose-managed.json.upgrade" "$install_dir/compose-managed.json"
  install -m 700 "$work/puppeteer-deployd" "$install_dir/config/puppeteer-deployd.next"
  mv -f "$install_dir/config/puppeteer-deployd.next" "$install_dir/config/puppeteer-deployd"
fi
printf '[5/6] Starting previously running services and waiting for health\n'
phase=starting
"$ctl" up -d --no-build --no-deps --wait --wait-timeout 240 "${app_services[@]}"
"$ctl" up -d --no-build --no-deps --force-recreate --wait --wait-timeout 240 web
printf '[6/6] Verifying application readiness\n'
for index in 0 1; do
  service=server; ((index == 0)) || service=web
  container=$("$ctl" ps -q "$service")
  actual=$(docker inspect --format '{{.Config.Image}}' "$container")
  [[ $actual == "${images[$index]}" ]] || die "Unexpected running image for $service"
done
"$ctl" exec -T server /app/nt health
"$ctl" ps
# Feature-detect the matching release image; legacy images remain supported.
if [[ ! -f $install_dir/config/deployd.json ]] && "$ctl" exec -T server test -x /app/puppeteer-deployd; then
  for dependency in python3 systemctl flock; do command -v "$dependency" >/dev/null || die "Automatic deployment enrollment requires $dependency"; done
  helper_dir="$install_dir/config/deployment-helpers"
  mkdir -p -m 700 "$helper_dir"
  executor_container=$("$ctl" ps -q server)
  docker cp "$executor_container:/app/deployment/." "$helper_dir/"
  bash "$helper_dir/enroll-deployd.sh" "$install_dir"
fi
phase=complete
printf '%s\n' "$version" > "$install_dir/installed-version"
printf 'Upgrade complete: %s\nBackup retained: %s\n' "$version" "$backup"
if ((enable_database_proxy)); then
  printf 'Database proxy deployment configured. Confirm both listener switches in Platform settings and download the matching proxy certificate.\n'
fi
