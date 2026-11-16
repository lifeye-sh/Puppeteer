#!/usr/bin/env bash
# Puppeteer beta0.10, fresh Linux amd64 deployment. Never replaces existing secrets/data.
set -Eeuo pipefail
umask 077
revision=2be832131da346668daa784d8c96126f3dd049da
version=beta0.10
install_dir=/opt/puppeteer
project=puppeteer
host_name=
port=443
bind=0.0.0.0
cert=
key=
core=0
database_proxy=1
database_bind=127.0.0.1
help() {
  cat <<'HELP'
Usage: sudo bash install.sh --host DOMAIN_OR_IPV4 [options]
  --port PORT        HTTPS port (default 443)
  --bind IPV4        Host bind address (default 0.0.0.0)
  --dir PATH         New installation directory (default /opt/puppeteer)
  --project NAME     New Compose project (default puppeteer)
  --cert FILE --key FILE  Existing TLS chain/key; otherwise generate self-signed TLS
  --core             Omit graphics/shared-drive/MP4 services
  --database-proxy-bind IPV4  Database proxy bind address (default 127.0.0.1)
  --no-database-proxy         Do not configure database proxy listeners/ports
Requires Linux amd64 with systemd, Docker with Compose v2, bash, curl, openssl, python3 and flock.
HELP
}
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
while (($#)); do
  case "$1" in
    --help|-h) help; exit 0;;
    --core) core=1; shift;;
    --no-database-proxy) database_proxy=0; shift;;
    --host|--port|--bind|--dir|--project|--cert|--key|--database-proxy-bind)
      (($# >= 2)) || die "Missing value for $1"
      case "$1" in
        --database-proxy-bind) database_bind=$2;;
        --host) host_name=$2;; --port) port=$2;; --bind) bind=$2;;
        --dir) install_dir=$2;; --project) project=$2;; --cert) cert=$2;; --key) key=$2;;
      esac
      shift 2;;
    *) die "Unknown argument: $1";;
  esac
done
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || die 'Linux amd64 is required'
[[ $EUID == 0 ]] || die 'Run with sudo or as root'
for cmd in docker curl openssl install df awk python3 systemctl flock sha256sum cmp grep; do command -v "$cmd" >/dev/null || die "Missing command: $cmd"; done
docker_bin=$(command -v docker)
docker_resolved=$(readlink -f "$docker_bin" 2>/dev/null || printf '%s' "$docker_bin")
case "$docker_bin:$docker_resolved" in
  /snap/*:*|*:/snap/*|*:/usr/bin/snap)
    die 'Snap Docker is not supported by this installer: confinement may hide /opt and block required container options. Use Docker Engine with Compose v2; preserve existing installation files and volumes.';;
esac
[[ -n $host_name && ${#host_name} -le 253 && $host_name =~ ^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$ && $host_name != *..* ]] || die 'Provide --host with a domain or IPv4 address (no scheme/path/port)'
ipv4() {
  local a b c d extra
  [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  IFS=. read -r a b c d extra <<< "$1"
  local v; for v in "$a" "$b" "$c" "$d"; do [[ ${#v} -le 3 ]] && ((10#$v <= 255)) || return 1; done
}
ipv4 "$database_bind" || die "Database proxy bind must be IPv4"
ipv4 "$bind" || die 'Bind address must be IPv4'
san="DNS:$host_name"
if [[ $host_name =~ ^[0-9.]+$ ]]; then ipv4 "$host_name" || die 'Invalid host IPv4'; san="IP:$host_name"; fi
[[ $port =~ ^[1-9][0-9]{0,4}$ ]] && ((port <= 65535)) || die 'Invalid HTTPS port'
[[ $project =~ ^[a-z][a-z0-9-]{2,40}$ ]] || die 'Invalid project name'
[[ $install_dir =~ ^/[a-zA-Z0-9_./-]+$ && $install_dir != / && $install_dir != */../* && $install_dir != */.. && $install_dir != */. && $install_dir != */ ]] || die 'Use a simple absolute directory path without traversal or trailing slash'
[[ -d $(dirname "$install_dir") ]] || die 'Installation parent directory must already exist'
[[ ! -e $install_dir && ! -L $install_dir ]] || die 'Installation directory already exists; refusing to overwrite'
if [[ -n $cert || -n $key ]]; then
  [[ -f $cert && -f $key ]] || die 'Provide both --cert and --key'
  openssl x509 -in "$cert" -noout -checkend 86400 >/dev/null || die 'TLS certificate expires too soon'
  openssl verify -partial_chain -trusted "$cert" -purpose sslserver "$cert" >/dev/null || die 'TLS certificate is not currently valid for a server'
  if [[ $san == IP:* ]]; then openssl x509 -in "$cert" -noout -checkip "$host_name" >/dev/null; else openssl x509 -in "$cert" -noout -checkhost "$host_name" >/dev/null; fi || die 'Certificate does not cover host'
  cert_pub=$(openssl x509 -in "$cert" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256)
  key_pub=$(openssl pkey -in "$key" -passin pass: -pubout -outform DER | openssl dgst -sha256)
  [[ $cert_pub == "$key_pub" ]] || die 'Certificate and unencrypted private key do not match'
fi
docker compose version >/dev/null || die 'Install Docker Compose v2 first'
read -r os cpus memory < <(docker info --format '{{.OSType}} {{.NCPU}} {{.MemTotal}}')
[[ $os == linux ]] && ((cpus >= 2 && memory >= 4294967296)) || die 'Docker needs Linux containers, >=2 CPUs and >=4 GiB memory'
docker_root=$(docker info --format '{{.DockerRootDir}}')
[[ $docker_root != /var/snap/* ]] || die 'Snap Docker daemon detected; use supported Docker Engine without changing existing deployment files or deleting volumes'
[[ -z $(docker ps -aq --filter "label=com.docker.compose.project=$project") ]] || die 'Compose project already has containers'
[[ -z $(docker volume ls -q --filter "label=com.docker.compose.project=$project") ]] || die 'Compose project already has data volumes'
available=$(df -Pk "$(dirname "$install_dir")" | awk 'END {print $4}')
((available >= 5242880)) || die 'At least 5 GiB free space required on configuration filesystem; also check Docker disk capacity'
graphics_apparmor=0
if (( ! core )); then
  if docker info --format '{{json .SecurityOptions}}' | grep -q apparmor; then
    command -v apparmor_parser >/dev/null || die 'AppArmor requires apparmor_parser before shared-drive installation'
    graphics_apparmor=1
  fi
  if [[ ! -c /dev/fuse ]] && command -v modprobe >/dev/null; then modprobe fuse || true; fi
  [[ -c /dev/fuse ]] || die 'FUSE is unavailable; enable /dev/fuse or use --core'
fi
origin="https://$host_name"
[[ $port == 443 ]] || origin+=":$port"
mkdir -m 700 "$install_dir"
trap 'printf "Installation interrupted. Files retained in %s; no volumes were deleted. Review the error above. If puppeteerctl exists, use it to retry startup.\n" "$install_dir" >&2' ERR
printf '[1/5] Downloading pinned deployment configuration\n'
for file in compose.yaml services.yaml nginx.conf; do
  curl --fail --show-error --silent --location --retry 3 --connect-timeout 20 \
    "https://raw.githubusercontent.com/lifeye-sh/Puppeteer/$revision/deploy/production/$file" -o "$install_dir/$file"
done
chmod 644 "$install_dir/compose.yaml" "$install_dir/services.yaml" "$install_dir/nginx.conf"
config="$install_dir/config"
mkdir -m 700 "$config" "$config/secrets" "$config/tls"
chown 101:101 "$config/tls"
if ((graphics_apparmor)); then
  # Bundle the profile: the pinned public-image revision predates this file.
  cat > "$config/drive-profile.template" <<'PUPPETEER_DRIVE_PROFILE'
# Based on Moby's Apache-2.0 default container profile.
# https://github.com/moby/profiles/blob/main/apparmor/template.go
# Only the mount rule is relaxed, for the platform's FUSE shared drive.
abi <abi/3.0>,
#include <tunables/global>
profile puppeteer-rdp-storage flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>
  network,
  deny network ax25,
  deny network ipx,
  deny network appletalk,
  deny network netrom,
  deny network rose,
  deny network econet,
  deny network atmsvc,
  deny network irda,
  deny network wanpipe,
  deny network isdn,
  deny network caif,
  deny network alg,
  deny network vsock,
  capability,
  file,
  mount fstype=fuse.nt-storage options=(rw,nosuid,nodev,noexec) nt-user-storage -> /mnt/nt-rdp/,
  umount /mnt/nt-rdp/,
  signal (receive) peer=unconfined,
  signal (receive) peer=runc,
  signal (receive) peer=crun,
  signal (send,receive) peer=puppeteer-rdp-storage,
  deny @{PROC}/* w,
  deny @{PROC}/{[^1-9/],[^1-9/][^0-9/],[^1-9s/][^0-9y/][^0-9s/],[^1-9/][^0-9/][^0-9/][^0-9/]*}/** w,
  deny @{PROC}/sys/[^k]** w,
  deny @{PROC}/sys/kernel/{?,??,[^s][^h][^m]**} w,
  deny @{PROC}/sysrq-trigger rwklx,
  deny @{PROC}/kcore rwklx,
  deny /sys/[^f]*/** wklx,
  deny /sys/f[^s]*/** wklx,
  deny /sys/fs/[^c]*/** wklx,
  deny /sys/fs/c[^g]*/** wklx,
  deny /sys/fs/cg[^r]*/** wklx,
  deny /sys/firmware/** rwklx,
  deny /sys/devices/virtual/powercap/** rwklx,
  deny /sys/kernel/security/** rwklx,
  ptrace (trace,tracedby,read,readby) peer=puppeteer-rdp-storage,
}
PUPPETEER_DRIVE_PROFILE
  printf '770a612f9a830d14236f9a8e9c158681d799665c39baf92894160e46becc8e16  %s\n' "$config/drive-profile.template" | sha256sum -c -
  profile_name="puppeteer-rdp-storage-$project"
  profile_file="/etc/apparmor.d/$profile_name"
  python3 - "$config/drive-profile.template" "$config/drive-profile" "$profile_name" <<'PYDRIVE'
from pathlib import Path
import sys
source,target,name=sys.argv[1:]
Path(target).write_text(Path(source).read_text().replace('puppeteer-rdp-storage',name))
PYDRIVE
  apparmor_parser --skip-kernel-load --skip-cache "$config/drive-profile"
  if [[ -e $profile_file || -L $profile_file ]]; then
    [[ -f $profile_file && ! -L $profile_file ]] && cmp -s "$config/drive-profile" "$profile_file" || die 'Existing AppArmor profile differs; refusing overwrite'
  else
    install -m 644 "$config/drive-profile" "$profile_file"
  fi
  apparmor_parser -r "$profile_file"
  printf 'services:\n  guacd:\n    security_opt: ["apparmor=%s"]\n' "$profile_name" > "$install_dir/graphics-apparmor.yaml"
fi
printf '[2/5] Generating private configuration and TLS\n'
password=$(openssl rand -hex 32)
printf '%s\n' "$password" > "$config/secrets/database_password"
printf 'postgres://puppeteer:%s@postgres:5432/puppeteer?sslmode=disable\n' "$password" > "$config/secrets/database_url"
unset password
for name in mfa_key vault_key mp4_token; do openssl rand -hex 32 > "$config/secrets/$name"; done
chmod 400 "$config"/secrets/*
chown 10001:10001 "$config"/secrets/*
chown 70:70 "$config/secrets/database_password"
if [[ -n $cert ]]; then
  install -o 101 -g 101 -m 400 "$cert" "$config/tls/server.crt"
  install -o 101 -g 101 -m 400 "$key" "$config/tls/server.key"
else
  openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 365 \
    -keyout "$config/tls/server.key" -out "$config/tls/server.crt" \
    -subj "/CN=$host_name" -addext "subjectAltName=$san" \
    -addext 'basicConstraints=critical,CA:FALSE' -addext 'extendedKeyUsage=serverAuth' 2>/dev/null
  chmod 400 "$config"/tls/*
  chown 101:101 "$config"/tls/*
fi
cat > "$config/production.env" <<ENV
PUPPETEER_CONFIG_DIR=$config
PUPPETEER_ORIGIN=$origin
PUPPETEER_BIND=$bind
PUPPETEER_HTTPS_PORT=$port
PUPPETEER_SERVER_IMAGE=cloners/puppeteer-server@sha256:330e1deca9f2a31e3459842ead4ed36b560fbe3ec6d6526b189f8f92b7c3399f
PUPPETEER_WEB_IMAGE=cloners/puppeteer-web@sha256:04fe9d79559c29247c2e8c17d75749d53b9a00323208e6759d067fbe0674e616
PUPPETEER_GRAPHICS_IMAGE=cloners/puppeteer-graphics@sha256:7330d213133c3c66d76f19d57578e31747fa5346b6d99dcfeb4336dc212f2566
PUPPETEER_RENDERER_IMAGE=cloners/puppeteer-renderer@sha256:028d5952437d73bae13e9b3d9922e7249c3406bfce6bb9da5443f906eb07fb06
ENV
printf '#!/usr/bin/env bash\nset -euo pipefail\nexec docker compose --env-file %q -p %q -f %q ' "$config/production.env" "$project" "$install_dir/compose.yaml" > "$install_dir/puppeteerctl"
if (( ! core )); then printf '%s %q ' '-f' "$install_dir/services.yaml" >> "$install_dir/puppeteerctl"; fi
if ((graphics_apparmor)); then printf '%s %q ' '-f' "$install_dir/graphics-apparmor.yaml" >> "$install_dir/puppeteerctl"; fi
if ((database_proxy)); then
  cat > "$install_dir/database-proxy.yaml" <<YAML
services:
  server:
    environment:
      NT_DATABASE_PROXY_ADDR: ":13306"
      NT_POSTGRES_PROXY_ADDR: ":15432"
      NT_DATABASE_PROXY_NAME: "$host_name"
    ports:
      - "$database_bind:13306:13306"
      - "$database_bind:15432:15432"
YAML
  printf '%s %q ' '-f' "$install_dir/database-proxy.yaml" >> "$install_dir/puppeteerctl"
fi
printf '"$@"\n' >> "$install_dir/puppeteerctl"
chmod 700 "$install_dir/puppeteerctl"
ctl="$install_dir/puppeteerctl"
[[ -s $config/production.env && -r $config/production.env ]] || die "Configuration file was not created or is unreadable: $config/production.env"
if ! "$ctl" config --quiet; then
  die "The configuration exists at $config/production.env but Compose could not read/validate it. Check Docker confinement and the error above. Existing secrets were preserved; do not regenerate them."
fi
printf '[3/5] Pulling pinned release images\n'
"$ctl" pull
printf '[4/5] Starting services and waiting for health checks\n'
"$ctl" up -d --no-build --wait --wait-timeout 240
printf '[5/5] Verifying application readiness\n'
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
printf '%s\n' "$version" > "$install_dir/installed-version"
printf '\nInstalled %s. Open %s/setup to create your administrator.\n' "$version" "$origin"
printf 'Manage: sudo %s ps (also supports logs, stop, and up -d --no-build --wait).\n' "$ctl"
if [[ -z $cert ]]; then printf 'TLS uses a generated self-signed certificate. Trust %s/tls/server.crt on your client or replace it with your CA-issued certificate.\n' "$config"; fi
printf 'Back up %s securely. No preset administrator or test data was installed.\n' "$config"
if ((database_proxy)); then
  printf 'Database proxies: %s:13306 (MySQL), %s:15432 (PostgreSQL); certificate host: %s.\n' "$database_bind" "$database_bind" "$host_name"
  printf 'Download the proxy certificate in Database assets. Remote access requires a reachable bind address and firewall rules.\n'
fi
