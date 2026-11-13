#!/usr/bin/env bash
# Recover an interrupted Puppeteer installation on an otherwise empty Ubuntu Docker Snap.
# Keep Snap data and all Puppeteer configuration; never migrate or delete volumes.
set -Eeuo pipefail
umask 077
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
[[ $EUID == 0 ]] || die 'Run with sudo or as root'
. /etc/os-release
[[ $ID == ubuntu && $(uname -m) == x86_64 ]] || die 'Only Ubuntu amd64 is supported by this recovery script'
case "${UBUNTU_CODENAME:-$VERSION_CODENAME}" in resolute|noble|jammy|stonking) ;; *) die 'Ubuntu release not supported by this script';; esac
[[ -z ${DOCKER_HOST:-} && -z ${DOCKER_CONTEXT:-} ]] || die 'Unset remote Docker context variables first'
config=/opt/puppeteer/config
ctl=/opt/puppeteer/puppeteerctl
[[ -s $config/production.env && -x $ctl ]] || die 'Existing Puppeteer configuration is incomplete; no changes made'
[[ -x /snap/bin/docker ]] || die 'Docker Snap executable not found; no changes made'
[[ $(/snap/bin/docker context show) == default ]] || die 'Docker Snap must use the local default context'
[[ $(/snap/bin/docker info --format '{{.DockerRootDir}}') == /var/snap/docker/* ]] || die 'Selected engine is not the Docker Snap daemon'
assert_empty_snap() {
  local containers volumes
  containers=$(/snap/bin/docker ps -aq) || die 'Cannot inspect Snap containers; engine switch cancelled'
  volumes=$(/snap/bin/docker volume ls -q) || die 'Cannot inspect Snap volumes; engine switch cancelled'
  [[ -z $containers ]] || die 'Snap contains containers; refusing to interrupt or migrate them'
  [[ -z $volumes ]] || die 'Snap contains data volumes; refusing to switch engines'
}
assert_empty_snap
if [[ -d /var/lib/docker && -n $(find /var/lib/docker -mindepth 1 -maxdepth 1 -print -quit) ]]; then
  die '/var/lib/docker already contains data; inspect the existing Engine before switching'
fi
for package in docker.io docker-compose docker-compose-v2 docker-doc docker-buildx podman-docker containerd runc; do
  status=$(dpkg-query -W -f='${db:Status-Status}' "$package" 2>/dev/null || true)
  [[ $status != installed ]] || die "Conflicting package $package is installed; it was not removed"
done
[[ ! -e /etc/apt/sources.list.d/docker.list ]] || die 'Existing docker.list found; inspect apt configuration first'
source_file=/etc/apt/sources.list.d/docker.sources
if [[ -e $source_file ]] && ! grep -q '^# Puppeteer Snap recovery$' "$source_file"; then
  die 'Existing Docker apt source was not replaced'
fi
for name in database_password database_url mfa_key vault_key mp4_token; do
  [[ -s $config/secrets/$name ]] || die "Missing existing secret $name; refusing to regenerate keys"
done
[[ -s $config/tls/server.crt && -s $config/tls/server.key ]] || die 'Existing TLS files are incomplete'
before=$(sha256sum "$config/production.env" "$config"/secrets/* "$config"/tls/*)
snap_disabled=0
trap 'printf "Recovery interrupted. Existing Puppeteer configuration and Snap data were retained. Snap disabled=%s. Do not delete directories or regenerate keys; inspect the error above.\n" "$snap_disabled" >&2' ERR
printf '[1/4] Configuring the official Docker apt repository\n'
apt-get update
apt-get install -y ca-certificates curl
install -m 0755 -d /etc/apt/keyrings
curl -fsSL --retry 3 https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
cat > "$source_file" <<EOF
# Puppeteer Snap recovery
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: ${UBUNTU_CODENAME:-$VERSION_CODENAME}
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF
chmod 644 "$source_file"
apt-get update
# Download first: a repository/network failure must not stop the existing Snap daemon.
apt-get install -y --download-only docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
# Recheck just before switching, in case a workload was created during downloads.
assert_empty_snap
printf '[2/4] Disabling the empty Docker Snap (preserving its data)\n'
snap disable docker
snap_disabled=1
printf '[3/4] Installing Docker Engine and Compose\n'
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
hash -r
[[ $(command -v docker) == /usr/bin/docker ]] || die 'Unexpected docker executable in PATH; inspect before starting'
/usr/bin/docker info >/dev/null
/usr/bin/docker compose version
[[ $before == "$(sha256sum "$config/production.env" "$config"/secrets/* "$config"/tls/*)" ]] || die 'Existing configuration changed; startup stopped'
printf '[4/4] Continuing Puppeteer installation with existing keys and certificates\n'
"$ctl" config --quiet
"$ctl" pull
"$ctl" up -d --no-build --wait --wait-timeout 240
"$ctl" exec -T server /app/nt health
"$ctl" ps
origin=$(sed -n 's/^PUPPETEER_ORIGIN=//p' "$config/production.env")
printf '\nRecovery completed. Open %s/setup to create the administrator.\n' "$origin"
printf 'In an existing terminal, run hash -r if docker still resolves to the old Snap path.\n'
