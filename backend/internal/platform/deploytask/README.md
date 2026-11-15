# Deployment executor development status

The Linux host daemon, authenticated Unix socket API, application admin API,
UI task polling, Compose planner, host port probes, container application checks,
atomic configuration journal, rollback and disable recovery are implemented.
The daemon operates only on an enrolled standalone Compose configuration.

Firewall and cloud security-group management are explicitly outside scope.
Enabling checks host bind availability, conflicting Compose mappings, application
certificates, running container health, published mappings and container listeners.
This does not prove internet reachability. Existing remote mappings can be enrolled.

Deployment-controlled protocols use NT_DEPLOY_MANAGED_* flags. They take precedence
over legacy listener toggles for the selected protocol. Unmanaged protocols retain
legacy behavior. Disabling stops the container listener as well as removing its
owned mapping. A server recreation currently interrupts all server connections.

scripts/enroll-deployd.sh and enroll-deployd.py prepare and enroll an existing
single-node installation using the matching binary from its running server image.
They preserve the original wrapper and resolved configuration in a private backup.
The image must already include puppeteer-deployd; old release images are rejected.
The upgrade script updates enrolled Compose image references and host binary while
the daemon is stopped. Failures leave the daemon stopped with a recovery message.

Task journals retain the current task and the latest 100 historical tasks. Journal
write failures block subsequent operations while still attempting rollback/fencing.
Restart recovery verifies or rolls back interrupted enables and fences disables.
Real Docker acceptance covers all four protocol toggles, management-port migration,
non-root Nginx progress while the API is stopped and forced executor termination.
Linux installer regression covers automatic adoption and failed-enrollment recovery.
Packaged enrollment and all deployment paths also pass under an actual systemd
manager in a disposable Linux container. Socket directory mode is explicitly set
after the private enrollment umask, and application/web access is checked before
enrollment succeeds. This does not claim a remote-server rollout or data upgrade;
a new published release remains a separate delivery step.

Fresh installation and legacy upgrades now detect the executor binary in the target
image, extract matching packaged enrollment helpers and invoke enrollment. Legacy
images without the binary retain their prior behavior. Tests cover both branches.

The current task has a two-hour read-only progress ticket. Nginx proxies only the
progress path to the host socket; this can serve progress while the API container
is unavailable. Web container recreation still causes a brief reconnect interval.
The route strips cookies/Authorization and does not log tickets. Ticket tests cover
wrong credentials, expiry, method restrictions and inability to access admin APIs.

First-time database enable preflight validates certificate generation without
persisting it; application startup creates the real certificate. Existing selected
certificates must pass identity and validity checks. Managed listener changes via
legacy settings are rejected with a redirecting explanation, avoiding silent no-ops.

Website-specific tasks now preserve a shared gateway while other sites are enabled,
apply disabled state before deployment and enable a site only after verification.
Management-port tasks rewrite one standard server upstream into a separate immutable
Nginx file, update runtime/health addresses, validate with Nginx and preserve rollback
references. See docs/AUTO-DEPLOYMENT.md for supported paths and release boundaries.
