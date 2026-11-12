# Native RDP recording component (Linux)

This component accepts synthetic or worker-produced full BGRA32 frames and
encodes private H.264/yuv420p fragmented MP4 segments. The development FreeRDP
record-view profile feeds this component over an authenticated Unix socket.
The application publishes each sealed segment into the PostgreSQL audit store.
Authorized playback reads segments from the access record using HTTP Range.
Recovery of temporary files left by a hard crash is pending.

## Caller contract

- Authenticate the worker and bind a server-generated session UUID before calling
  `Record`. Never accept a spool path, FFmpeg binary, session owner or quota from
  the RDP client. `Root` must be an existing service-owned private directory;
  each invocation creates a new 0700 session directory and refuses reuse.
- Supply a closable pipe/connection whose `Close` interrupts `Read`. `Record`
  owns it. The pipe provides bounded backpressure; the producer must have its
  own bounded write deadline and terminate its desktop session if recording fails.
- Explicitly configure an absolute FFmpeg path, video byte quota and I/O timeout
  (1 ms to 15 s; use several seconds for deployment). Total concurrent spool
  capacity, CPU/memory, worker admission and filesystem reservation remain the
  supervisor/deployment caller's responsibility.
- The recorded desktop receiver closes the target bridge on a recording error.
  The first accepted frame activates the platform session and access record.
  The receiver sends a completion acknowledgement after the final manifest commit.

## NTRF version 1 wire format

Each packet has a 24-byte header followed by exactly `payloadBytes` bytes.
Multi-byte fields are big endian. Unknown versions, kinds and reserved bits,
sequence gaps, truncated packets and invalid dimensions fail before allocating
an oversized payload.

| Offset | Length | Field |
| --- | --- | --- |
| 0 | 4 | ASCII `NTRF` |
| 4 | 1 | Version: 1 |
| 5 | 1 | Kind: 1 frame, 2 explicit end |
| 6 | 2 | Reserved, zero |
| 8 | 8 | Sequence, starting at zero |
| 16 | 2 | Width |
| 18 | 2 | Height |
| 20 | 4 | Payload bytes |

Frames are tightly packed BGRA32, dimensions 2..1920 by 2..1080, exactly
`width * height * 4` bytes. The producer must sample at 10 fps, repeating the last
complete framebuffer during idle periods. An EndPaint event alone is not the
sampling clock. Every sequence represents 100 ms; timestamps in the manifest
are relative to that sampling clock, not measured packet arrival time. Capture
timing in the worker uses a monotonic clock and advances the sampling timeline
before replacing the last published image. The bounded writer queue interrupts
the stream on overload. The platform records the first encoder write as the
connection time.

End uses the next sequence with zero dimensions/payload and is invalid before
the first frame. EOF alone is never success. The format is limited to 144000
frames (4 hours) and 2048 segments. Excessive resize events also hit the segment
limit. These are this component's bounds, not a published platform session limit.

## Files and failure behavior

- Seal every 100 frames (10 seconds) or on resize; odd dimensions are padded
  right/bottom to an even encoded size. The original dimensions remain recorded.
- FFmpeg reads stdin and writes fragmented MP4 to stdout; a quota-limited writer
  creates 0600 `.part` files. A successful encoder exit, file sync and SHA-256
  precede rename to `segment-NNNNNN.mp4`. Save the bounded manifest atomically,
  sync its file and parent directory. No credentials or frames enter logs.
- The byte quota includes both sealed video and the active segment. Manifest
  overhead is separately bounded at 1 MiB. This is not a filesystem/block quota
  or a global spool reservation.
- Complete requires explicit End plus successful final sealing. Controlled
  errors produce `incomplete` with a fixed reason, discard the active partial
  file and retain only sealed segments. A hard process/power failure can leave
  `writing` and partial/orphan files; treat this as unconfirmed/incomplete.
  Startup reconciliation and authenticated publishing are still pending.
- Process groups, bounded pipe/drain waits and Linux parent-death signals stop
  encoders after cancellation or recorder process death. The creator OS thread
  remains locked until encoder cleanup, as required by Linux Pdeathsig.
- A failed active segment can lose up to 100 sampled frames (10 seconds).
  Filesystem failure can prevent even the failure manifest from being persisted;
  that error is returned. Do not label a `writing`/failed manifest complete.

## Reproduction and evidence

From the repository root, after building the existing native RDP SDK/worker:

```powershell
./scripts/test-native-recording.ps1
# Reuse the fixture image:
./scripts/test-native-recording.ps1 -NoBuild
```

The development image uses Alpine FFmpeg 8.1.3 with libx264/GPL enabled. It is
not a production distribution artifact; final dependency pinning, resource
isolation and distribution requirements belong to the production image work.

Acceptance uses synthetic BGRA and actual FFmpeg/ffprobe, including decoded
pixels, frame count, codec, duration, resize, odd dimensions, 1080p, SHA-256,
cross-segment quota, malformed input, unexpected EOF, read/write/drain stalls,
cancellation and forced parent death. An isolated child imposes a real kernel
file-size limit to exercise failed storage writes. It does not fill the host
disk. Protocol tests also run a TLS ingress client against a FreeRDP NLA target
with a private SAM. They check red/blue bitmap updates at the client and in
the decoded MP4, refusal on wrong password, quota failure and active-session
revocation. Windows targets, API playback and load testing remain pending.
