#!/usr/bin/env bash
# Enroll an existing single-node Linux Compose installation. Run only with a
# server image containing the matching puppeteer-deployd binary.
set -Eeuo pipefail
umask 077
install_dir=${1:-/opt/puppeteer}
[[ $EUID == 0 && $(uname -s) == Linux ]] || { echo 'Linux root is required' >&2; exit 1; }
[[ $install_dir =~ ^/[a-zA-Z0-9_/-]+$ && $install_dir != / ]] || { echo 'Invalid installation directory' >&2; exit 1; }
install_dir=$(realpath -e "$install_dir")
ctl="$install_dir/puppeteerctl"
[[ -x $ctl && -d $install_dir/config ]] || { echo 'Existing one-click installation required' >&2; exit 1; }
for command in docker python3 systemctl flock; do command -v "$command" >/dev/null || { echo "Missing $command" >&2; exit 1; }; done
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
[[ -f $script_dir/enroll-deployd.py ]] || { echo 'Enrollment helper missing' >&2; exit 1; }
exec 9>"$install_dir/.deploy-enrollment.lock"
flock -n 9 || { echo 'Another enrollment is running' >&2; exit 1; }
[[ ! -e $install_dir/config/deployd.json ]] || { echo 'Already enrolled; refusing overwrite' >&2; exit 1; }
server=$("$ctl" ps -q server)
[[ -n $server && $server != *$'\n'* ]] || { echo 'Exactly one running server required' >&2; exit 1; }
stage=$(mktemp -d "$install_dir/.deploy-enroll-XXXXXXXX")
printf '[1/5] Checking executor binary and resolving existing configuration\n'
docker cp "$server:/app/puppeteer-deployd" "$stage/puppeteer-deployd"
chmod 700 "$stage/puppeteer-deployd"
"$stage/puppeteer-deployd" --help >/dev/null 2>&1
"$ctl" config --format json > "$stage/source.json"
project=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["name"])' "$stage/source.json")
[[ $project =~ ^[a-z][a-z0-9-]{2,40}$ ]] || { echo 'Invalid project' >&2; exit 1; }
unit="puppeteer-deployd-$project.service"
[[ ! -e /etc/systemd/system/$unit ]] || { echo 'Systemd unit already exists' >&2; exit 1; }
cp -p "$ctl" "$stage/puppeteerctl.before"
# All paths below are enrollment-owned and must be absent before recovery is armed.
artifacts=("$install_dir/compose-managed.json" "$install_dir/config/deployd.json"
  "$install_dir/config/deployd-token" "$install_dir/config/deployd-state"
  "$install_dir/config/deployd-run" "$install_dir/config/deployment-progress.conf"
  "$install_dir/config/puppeteer-deployd")
for artifact in "${artifacts[@]}"; do
  [[ ! -e $artifact && ! -L $artifact ]] || { echo "Enrollment artifact already exists: $artifact" >&2; exit 1; }
done
unit_created=false
refresh_started=false
recover_enrollment() {
  local status=$1
  trap - ERR INT TERM
  set +e
  local recovered=true
  if [[ $unit_created == true ]]; then
    systemctl disable --now "$unit" || recovered=false
  fi
  if [[ $recovered == true ]]; then
    cp -p "$stage/puppeteerctl.before" "$ctl"
    if [[ $refresh_started == true ]]; then
      "$ctl" up -d --no-deps --no-build --pull never --force-recreate --wait --wait-timeout 120 server web || recovered=false
    fi
  fi
  if [[ $recovered == true ]]; then
    if [[ $unit_created == true ]]; then
      rm -f -- "/etc/systemd/system/$unit"
      systemctl daemon-reload || recovered=false
    fi
    mkdir -p "$stage/failed-enrollment"
    for artifact in "${artifacts[@]}"; do
      if [[ -e $artifact || -L $artifact ]]; then
        mv -- "$artifact" "$stage/failed-enrollment/" || recovered=false
      fi
    done
  fi
  if [[ $recovered == true ]]; then
    echo "Enrollment failed; original services restored automatically. Retry is safe. Protected diagnostics: $stage" >&2
  else
    echo "Enrollment and automatic recovery failed; configuration retained at $stage. Inspect service health before retrying." >&2
  fi
  exit "$status"
}
trap 'recover_enrollment "$?"' ERR
trap 'recover_enrollment 130' INT
trap 'recover_enrollment 143' TERM
printf '[2/5] Preparing protected executor configuration\n'
python3 "$script_dir/enroll-deployd.py" --source "$stage/source.json" --directory "$install_dir" --docker "$(command -v docker)"
install -m 700 "$stage/puppeteer-deployd" "$install_dir/config/puppeteer-deployd"
cat > "$stage/puppeteerctl" <<CTL
#!/usr/bin/env bash
set -euo pipefail
exec docker compose --project-directory '$install_dir' --project-name '$project' -f '$install_dir/compose-managed.json' "\$@"
CTL
chmod 700 "$stage/puppeteerctl"
"$stage/puppeteerctl" config --quiet
printf '[3/5] Installing host executor service\n'
unit_created=true
cat > "/etc/systemd/system/$unit" <<UNIT
[Unit]
Description=Puppeteer restricted deployment executor ($project)
After=docker.service
Requires=docker.service
[Service]
Type=simple
ExecStart=$install_dir/config/puppeteer-deployd --config $install_dir/config/deployd.json
Restart=on-failure
RestartSec=3
TimeoutStopSec=600
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=$install_dir
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now "$unit"
printf '[4/5] Activating managed configuration and refreshing application\n'
cp -p "$stage/puppeteerctl" "$install_dir/puppeteerctl.next"
mv -f "$install_dir/puppeteerctl.next" "$ctl"
refresh_started=true
"$ctl" up -d --no-deps --no-build --pull never --force-recreate --wait --wait-timeout 120 server web
printf '[5/5] Checking executor service\n'
systemctl is-active --quiet "$unit"
"$ctl" exec -T server test -r /run/secrets/deployd_token
"$ctl" exec -T server test -w /run/puppeteer-deploy/deployd.sock
"$ctl" exec -T web test -w /run/puppeteer-deploy/deployd.sock
trap - ERR INT TERM
printf 'Executor enrolled. Configuration and original wrapper retained at %s. Firewall and cloud security groups are outside deployment scope.\n' "$stage"
