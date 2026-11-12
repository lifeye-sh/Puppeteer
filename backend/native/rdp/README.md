# Native RDP worker

The optional Linux development listener starts when writes are enabled and
`NT_NATIVE_RDP_LOOPBACK` specifies a loopback address and port. Set absolute
paths in `NT_NATIVE_RDP_WORKER`, `NT_NATIVE_RDP_CERT`, `NT_NATIVE_RDP_KEY`
and `NT_NATIVE_RDP_FFMPEG`. The runtime needs the pinned worker, its shared
libraries, FFmpeg and a certificate trusted by the client for the endpoint.
For Windows host acceptance, `backend/compose.native-rdp-host.yaml` adds the
server port mapping `127.0.0.1:13389:13389`. The local profile advertises
`backend.localhost:13389`, matching the SAN of the trusted development
certificate. It uses the previously built SDK/FFmpeg image as a read-only
Docker image mount and retains the ordinary database and backup tools.
Build the Linux server into `backend/.local/rdp-view/server` before starting
this overlay. It requires Docker with image mounts and Compose subpath support.

An administrator can run the interactive acceptance with
`node scripts/test-windows-rdp.mjs ASSET_UUID` on the Windows host after
enabling native PKCE access. Complete browser consent, view the desktop
for at least twelve seconds, then close mstsc and check the corresponding
session in Access Records. The script deletes its temporary file and revokes
its temporary grant on exit. Restore the prior native access setting after
acceptance.

POST `/api/native/v1/rdp-file` with a native bearer grant, sessions:connect
scope and an assetId to obtain an attachment. It contains a single-use ticket,
valid for up to five minutes. The Windows command is
`nt-client rdp --server HTTPS_ORIGIN --asset UUID`. It reconstructs a restricted
desktop file, starts mstsc in public mode and removes the file on normal exit.
The development listener admits two simultaneous workers. The record-file
protocol test exercises the API, socket and target together.

The development worker uses FreeRDP 3.32.2. The `--auth-probe` profile ends at
upstream authentication. The `--record-view` profile opens a view-only desktop
after the authenticated parent recorder confirms its initial manifest.

The Go supervisor supplies an accepted TCP connection on FD 3, a fresh 32-byte
nonce on FD 4, and the READY pipe on FD 5. The three arguments after the profile
are the parent-owned control socket, ingress certificate PEM and private key PEM.
Tickets and target passwords must never appear in arguments, environment,
configuration or logs. Standard FreeRDP logging is disabled. The private
`<control socket>.trace` file contains only fixed diagnostic stage names.
The runtime parent must remove the entire per-worker private directory after
the process exits, including the fresh HOME and certificate cache beneath it.

The TLS `ServerPeerLogon` callback occurs before ClientInfo and cannot authorize
the supplied username. `ServerFetchTargetAddr` is the mandatory ticket gate;
both outgoing connection hooks also require successful consumption/preparation.
The authenticated parent determines the target and supplies a private Unix
socket. The worker separates that transport path from the original server name,
enforces SHA-256 DER fingerprint and certificate validity, and requires NLA.
It never falls back to plain TLS or legacy RDP security. The probe disables all
input, redirection, clipboard, device and audio channels.

Bootstrap verifies the parent's PID/UID with SO_PEERCRED, and the parent verifies
the child PID/UID plus the one-use nonce. READY is emitted only after server
context initialization. The worker renews the platform lease every two seconds;
lease expiry, authorization loss, control closure or signals stop the process.
The current platform adapter marks interrupted recordings honestly.

Run from repository root with the existing PostgreSQL test service healthy:

```powershell
./scripts/test-native-rdp-worker.ps1 -BuildSdk # first build
./scripts/test-native-rdp-worker.ps1           # subsequent runs
```

The acceptance fixture uses a real headless FreeRDP ingress client, the C worker,
the Go supervisor/control adapter, isolated PostgreSQL/Vault and a local
X.224/TLS target. That target observes CredSSP and intentionally rejects login;
it is not a Windows server and does not prove successful NLA authentication.
No production RDP listener or capability is enabled by this image or script.

Run `./scripts/test-native-recording.ps1` for recorded desktop acceptance.
It also runs an NLA target with its own test SAM, sends known RDP bitmap updates,
and compares the decoded colors at the client and in the sealed MP4. Wrong
password, quota failure and live revocation use the same protocol stack.

The version-specific SDK patch retains the negotiated GDI bitmap/surface
decoders and updates the framebuffer before forwarding to the client. EndPaint
publishes an owned BGRA snapshot. A monotonic 10 Hz sampler advances on every
paint and during idle time. A bounded queue of 16 full images separates capture
from socket writes. Queue overflow fails the session. The Go receiver checks
PID/UID and binds the recording to the consumed platform ticket. Target
credentials are cleared from the upstream settings before reactivation copies
negotiated settings to the ingress peer. Input and redirection are disabled.

Clipboard transfer is policy-driven. The supervisor delivers a
`clipboardCopy` / `clipboardPaste` pair in the target payload, mirroring the
same user channel policy the web graphics session uses; paste is refused on a
readonly session. Cliprdr is the only static channel in the passthrough list;
the back-end announcement gate rejects every other name (and all dynamic
channels), and per-direction MS-RDPECLIP message filters limit the channel to
the text subset, dropping file-object and unknown types. The pure-C policy
module and its unit test build and run inside the worker image. Real Windows
clipboard acceptance, direction probing and DVC bypass cases are pending.

Client drive redirection runs as a dedicated profile. The supervisor
delivers `drives` with `driveUpload` / `driveDownload` and four limits; the
platform gate is the drive switch plus the same user channel policy that web
drive mounts use, and upload is never enabled on a readonly session. The
listener permanently uses `Intercept=rdpdr`: the proxy's relay state machines
negotiate MS-RDPDR with the target and the ingress peer, the pinned patch
`rdpdr-policy.patch` places weak hooks at the three crossing points, and the
worker provides their strong definitions. Announcements reach the target with
filesystem devices only; create/read/write IRPs are gated per operation (1 MiB
per single IRP, 256 MiB per session direction, fail closed on overflow),
create paths are audited, and per-operation events are batched to the platform
over the control socket (`ReportDeviceIO`) with the session totals in every
batch. The pure-C policy module and its unit test also build and run inside
the worker image. Windows end-to-end drive acceptance and the limit/audit
budget drills are pending.

The platform records the first encoder write as the connection time. MP4
segments are staged in private files and committed to the audit catalog before
their temporary video files are removed. The browser requests each video with
the session playback permission. Windows acceptance and production admission
are pending.
