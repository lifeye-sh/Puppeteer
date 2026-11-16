# Puppeteer

Puppeteer is a remote operations and auditing platform built with Go, React and PostgreSQL. It brings together assets, remote sessions, database and website access, approvals, recordings, backups and deployment tasks.

[简体中文](README.md) · [Project overview (Chinese)](docs/PROJECT-OVERVIEW.md) · [Release](https://github.com/lifeye-sh/Puppeteer/releases/tag/beta0.11)

## Current release

**beta0.11**, a beta release for Linux amd64, uses database schema 110 and capability revision 90. Matching server, web, graphics and renderer images are published with pinned digests.

This release adds signed update discovery, administrator-confirmed self-upgrades, automatic image preparation and authorized maintenance-window upgrades. Upgrades run independently on the host, with preflight checks, progress reporting, paired backups and recovery. Automatic installation is disabled by default. See the [release notes](deploy/production/beta0.11.md), [image manifest](deploy/production/dockerhub-beta0.11.json) and [system update guide](docs/SYSTEM-UPDATES.md).

## Features

| Area | Capabilities |
| --- | --- |
| Assets and access | Assets, groups, encrypted credentials, SSH/RDP/VNC/Telnet sessions, Agents, jump hosts and gateways |
| Authorization | Users, departments, roles, batch grants, access approvals and SQL work orders |
| Databases and websites | MySQL/PostgreSQL proxies, database policies and website proxy configuration |
| Audit and files | Session auditing, recordings, MP4 exports and authorized file operations |
| Operations | Deployment preflight and progress, component management, resource profiles, backups, certificates and notifications |
| AI assistant | Conversations, model and usage settings, approved tools and administrator-only read-only update status |

Autonomous inspection and continuous operations across all modules remain a [design plan](docs/AI-OPERATIONS-AGENT-DESIGN.md). The current assistant does not automatically control every system feature.

## Install

Use a Linux amd64 host with Docker Engine, Compose v2, systemd and Python 3.9+. Replace the example IP address:

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/install.sh -o install-puppeteer.sh
sudo bash install-puppeteer.sh --host 192.168.1.100
```

Open `https://YOUR_SERVER_IP/setup` to create the administrator. There are no preset credentials. The installer generates a self-signed HTTPS certificate and initially binds database proxy ports to loopback. See [installation](docs/ONE-CLICK-INSTALL.md) for certificates, ports and management commands.

Resource profiles target budgets of 2 CPU/4 GiB, 4 CPU/8 GiB and 8 CPU/16 GiB. These are engineering budgets, not certified concurrency guarantees. See [resource optimization](docs/RESOURCE-OPTIMIZATION.md) for workload assumptions and validation limits.

## Upgrade an existing installation

Use the upgrade script for existing managed installations; do not rerun the installer. The default directory is `/opt/puppeteer`:

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/upgrade.sh -o upgrade-puppeteer.sh
sudo bash upgrade-puppeteer.sh --dir /opt/puppeteer --version beta0.11 --check
sudo bash upgrade-puppeteer.sh --dir /opt/puppeteer --version beta0.11
```

Upgrades interrupt active connections while preserving keys, named data volumes and component enablement. beta0.10 and earlier installations need this script upgrade once to install the host executor, upgrade-hold configuration and independent progress endpoint. Future releases can then be managed under **系统运维 → 系统更新**.

Automatic checks only fetch release information; automatic preparation downloads images without stopping business services. Window-based installation requires explicit version, expiry and downtime authorization. Web self-upgrades support standard Linux/systemd managed deployments with internal PostgreSQL 18 and local managed volumes. Unsupported layouts, including Docker Desktop, external databases, WAL deployments and RDP shared drives, are rejected. See [upgrade and recovery](docs/UPGRADE.md).

## Browser RDP and deployment scope

Browser RDP uses guacd. Enable Remote Desktop on the target Windows machine, create and authorize an RDP asset, then connect from My Assets. It does not require the native RDP Worker or platform ports 3389/13389. Ports 4822 for guacd and 8090 for the MP4 renderer remain internal. See [browser RDP](docs/BROWSER-RDP.md).

Deployment automation checks ports and dependencies, then reports persistent progress and failures. It does not manage host firewalls, cloud security groups or DNS. A running container alone does not prove executor enrollment or public reachability.

Actual beta0.11 images passed isolated Linux/systemd fresh installation and upgrade from beta0.10. Self-upgrade verification includes scheduled upgrades, executor replacement, independent progress and paired recovery after interrupted switching. Physical power-loss tests, target-host AppArmor checks and the second 10-hour mixed-load validation are not claimed as complete.

## Documentation and development

- [Project overview](docs/PROJECT-OVERVIEW.md) and [system updates](docs/SYSTEM-UPDATES.md)
- [Deployment automation](docs/AUTO-DEPLOYMENT.md), [batch authorization](docs/BATCH-AUTHORIZATION.md) and [backup/recovery](docs/backend/implementation/backup-restore.md)
- [Production deployment](deploy/production/README.md), [Docker Hub images](deploy/production/dockerhub.md) and [source deployment](docs/FRESH-INSTALL.md)
- [Backend development](backend/README.md), [architecture](docs/backend/README.md) and [historical delivery records](docs/backend/migration-rounds.md)

Production configuration lives in `deploy/production`; `backend/compose.yaml` is for development validation. [VERSION](VERSION) and [source metadata](deploy/production/source-release.json) describe the current source release.

Prepare release notes before running `node scripts/version.mjs --set VERSION`, then run `node scripts/version.mjs --check`. These commands manage metadata only. Publishing also requires image digests, installer and upgrade defaults, a GitHub tag and Release, and a signed update index published after the artifacts are available.

## Identity and license

Puppeteer independently maintains its code, releases and support at [lifeye-sh/Puppeteer](https://github.com/lifeye-sh/Puppeteer). It is not the browser automation library of the same name. The Go module is `puppeteer/backend`; application data lives at `/var/lib/puppeteer`. Existing `NT_*` configuration keys and `nt` commands remain stable interfaces.

See [LICENSE](LICENSE) for licensing and retained copyright notices. The graphics client uses official Apache Guacamole sources; [SOURCE.json](backend/renderer/vendor/guacamole-common-js/SOURCE.json) records the release and checksum, with LICENSE and NOTICE included.
