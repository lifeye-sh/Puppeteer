#!/usr/bin/env bash
# Puppeteer r17-rc1, fresh Linux amd64 deployment. Never replaces existing secrets/data.
set -Eeuo pipefail
umask 077
revision=3064f73a2690ce10443ea93f763b2f978cc72cda
install_dir=/opt/puppeteer
project=puppeteer
host_name=
port=443
bind=0.0.0.0
cert=
key=
core=0
help() {
  cat <<'HELP'
Usage: sudo bash install.sh --host DOMAIN_OR_IPV4 [options]
  --port PORT        HTTPS port (default 443)
  --bind IPV4        Host bind address (default 0.0.0.0)
  --dir PATH         New installation directory (default /opt/puppeteer)
  --project NAME     New Compose project (default puppeteer)
  --cert FILE --key FILE  Existing TLS chain/key; otherwise generate self-signed TLS
  --core             SSH/SFTP only; omit graphics/shared-drive/MP4 services
Requires Linux amd64, Docker with Compose v2, bash, curl and openssl.
HELP
}
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
while (($#)); do
  case "$1" in
    --help|-h) help; exit 0;;
    --core) core=1; shift;;
    --host|--port|--bind|--dir|--project|--cert|--key)
      (($# >= 2)) || die "Missing value for $1"
      case "$1" in
        --host) host_name=$2;; --port) port=$2;; --bind) bind=$2;;
        --dir) install_dir=$2;; --project) project=$2;; --cert) cert=$2;; --key) key=$2;;
      esac
      shift 2;;
    *) die "Unknown argument: $1";;
  esac
done
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || die 'Linux amd64 is required'
[[ $EUID == 0 ]] || die 'Run with sudo or as root'
for cmd in docker curl openssl install df awk; do command -v "$cmd" >/dev/null || die "Missing command: $cmd"; done
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
if (( ! core )); then
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
PUPPETEER_SERVER_IMAGE=cloners/puppeteer-server@sha256:3b9ae1c21e44ca200a578e1c7dc0ae808a8d6efece0c55dde57f45c7b7c4f969
PUPPETEER_WEB_IMAGE=cloners/puppeteer-web@sha256:a629cfa3667811fa2f2a032153e45ebe6d56aa7dd0a4f84336d8b5a00cdc8a94
PUPPETEER_GRAPHICS_IMAGE=cloners/puppeteer-graphics@sha256:c20c63352bd1cf8764ba9b47857d82957ac3785e9a21ab07a73eb79e20ceea22
PUPPETEER_RENDERER_IMAGE=cloners/puppeteer-renderer@sha256:497fdacf02e3b7131cd7c6b9d0376ed2d267547a9f1439e8c1485981d785784e
ENV
printf '#!/usr/bin/env bash\nset -euo pipefail\nexec docker compose --env-file %q -p %q -f %q ' "$config/production.env" "$project" "$install_dir/compose.yaml" > "$install_dir/puppeteerctl"
if (( ! core )); then printf '%s %q ' '-f' "$install_dir/services.yaml" >> "$install_dir/puppeteerctl"; fi
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
printf '\nInstalled. Open %s/setup to create your administrator.\n' "$origin"
printf 'Manage: sudo %s ps (also supports logs, stop, and up -d --no-build --wait).\n' "$ctl"
if [[ -z $cert ]]; then printf 'TLS uses a generated self-signed certificate. Trust %s/tls/server.crt on your client or replace it with your CA-issued certificate.\n' "$config"; fi
printf 'Back up %s securely. No preset administrator or test data was installed.\n' "$config"
