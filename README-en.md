# Puppeteer

A Go + PostgreSQL remote operations and auditing platform with asset management, remote sessions, files, recordings, database access, approvals, automation and an AI assistant.

[Installation](docs/ONE-CLICK-INSTALL.md) · [Upgrade existing deployments](docs/UPGRADE.md) · [Browser RDP](docs/BROWSER-RDP.md) · [r17-rc13 image release](deploy/production/r17-rc13.md)

[简体中文](README.md) · [Architecture](docs/backend/README.md) · [Delivery plan](docs/backend/migration-rounds.md)

The current release candidate is **r17-rc13**, schema 110 and capability revision 90. Matching server, web, graphics and renderer images are published for Linux amd64. Installation and upgrade scripts pin their digests and enroll the deployment executor. Upgrades preserve keys, named data volumes and disabled component states. Use the upgrade script for existing deployments.

Browser RDP uses guacd. Enable Remote Desktop on the target Windows machine, configure its address, port and credentials as an RDP asset, grant access and connect from My Assets. Browser access does **not** require the native RDP Worker or a platform port mapping for 3389/13389. The missing native Worker message concerns native clients such as mstsc; it does not indicate a browser RDP failure. Ports 4822 (guacd) and 8090 (MP4 renderer) remain internal.

Deployment tasks perform preflight checks and expose persistent progress and failure reasons. They do not modify host firewalls, cloud security groups or DNS. Linux/systemd deployment and Windows Docker Desktop validation have different prerequisites; see [deployment automation](docs/AUTO-DEPLOYMENT.md).

Prepare release notes before running `node scripts/version.mjs --set r17-rcN`; run `node scripts/version.mjs --check` before committing. These commands manage source metadata without changing installer image defaults or publishing containers.

Puppeteer independently maintains its code, releases, deployment and support at [lifeye-sh/Puppeteer](https://github.com/lifeye-sh/Puppeteer). It is not the browser automation library of the same name. The Go module is `puppeteer/backend`; application data lives at `/var/lib/puppeteer`. Existing `NT_*` configuration keys and `nt` commands remain stable interfaces.

See [LICENSE](LICENSE) for the license and retained copyright notices. The graphics client uses official Apache Guacamole sources; [SOURCE.json](backend/renderer/vendor/guacamole-common-js/SOURCE.json) records the release and checksum, with its LICENSE and NOTICE included.
