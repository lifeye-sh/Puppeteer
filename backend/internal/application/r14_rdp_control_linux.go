//go:build linux

package application

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"next-terminal/backend/internal/modules/audit"
	"next-terminal/backend/internal/modules/connectivity"
	"next-terminal/backend/internal/modules/files"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/modules/storage"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/rdprecording"
	"next-terminal/backend/internal/platform/rdpworker"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Installed only through Supervisor.ServeControlFactory. No HTTP handler or
// production listener is exposed until the required FreeRDP/recording gates pass.
func (a *App) nativeRDPControlFactory(ctx context.Context, root string) func(rdpworker.WorkerIdentity) rdpworker.ControlBackend {
	return a.nativeRDPRecordingFactory(ctx, root, nil)
}

func (a *App) nativeRDPRecordingFactory(ctx context.Context, root string, opt *rdprecording.Options) func(rdpworker.WorkerIdentity) rdpworker.ControlBackend {
	return func(peer rdpworker.WorkerIdentity) rdpworker.ControlBackend {
		if !resource.ValidID(peer.ID) || peer.PID <= 0 || net.ParseIP(peer.SourceIP) == nil {
			return nil
		}
		return &nativeRDPControl{app: a, ctx: ctx, root: root, peer: peer, recordingOptions: opt}
	}
}

type nativeRDPControl struct {
	mu                         sync.Mutex
	app                        *App
	ctx                        context.Context
	root                       string
	peer                       rdpworker.WorkerIdentity
	id, family                 string
	prepared, finalized        bool
	idleExpired                bool
	recordingComplete          bool
	bridge                     *rdpworker.TargetBridge
	recordingOptions           *rdprecording.Options
	recorder                   *rdpworker.RecordingReceiver
	driveEvents                int
	driveRead, driveWrite      int64
	driveDenied, driveOverflow int64
}

var _ rdpworker.ControlBackend = (*nativeRDPControl)(nil)

// Client-drive profile limits: a single redirected read/write never exceeds the
// web file channel chunk limit, and the session totals stay well below the
// storage quota so a runaway desktop cannot exhaust the audit budget or the
// browser-side share. All four values are delivered to the worker, which
// re-validates them.
var (
	nativeDriveMaxOp        = int64(files.MaxChunk) // 1 MiB per single read/write IRP
	nativeDriveSessionTotal = int64(256 * 1024 * 1024)
	// The per-operation audit is bounded per session; the totals survive even
	// past the budget and land in the finalization summary.
	nativeDriveEventBudget = 10000
)

type nativeRDPTarget struct {
	SocketPath             string `json:"socketPath"`
	ServerName             string `json:"serverName"`
	CertificateFingerprint string `json:"certificateFingerprint"`
	Username               string `json:"username"`
	Password               string `json:"password"`
	Security               string `json:"security"`
	Readonly               bool   `json:"readonly"`
	// Clipboard transfer policy from the same user channel policy the web
	// graphics session uses. Copy moves content target→user, paste
	// user→target; paste is never enabled on a readonly session. Audio remains
	// disabled until its own gated profile is accepted.
	ClipboardCopy  bool `json:"clipboardCopy"`
	ClipboardPaste bool `json:"clipboardPaste"`
	Audio          bool `json:"audio"`
	// Client-drive profile: gated by the platform drive switch and the same
	// user channel policy as the web drive mount. Upload moves the user's disk
	// content toward the target (target reads), download the reverse.
	Drives             bool   `json:"drives"`
	DriveUpload        bool   `json:"driveUpload"`
	DriveDownload      bool   `json:"driveDownload"`
	DriveMaxReadOp     int64  `json:"driveMaxReadOp,omitempty"`
	DriveMaxWriteOp    int64  `json:"driveMaxWriteOp,omitempty"`
	DriveMaxReadTotal  int64  `json:"driveMaxReadTotal,omitempty"`
	DriveMaxWriteTotal int64  `json:"driveMaxWriteTotal,omitempty"`
	RecordingSocket    string `json:"recordingSocket,omitempty"`
}

func (b *nativeRDPControl) ConsumeTicket(ctx context.Context, ticket string) (rdpworker.ControlReply, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.id != "" || b.finalized {
		return rdpworker.ControlReply{}, fault.Denied
	}
	v, f, e := b.app.consumeNativeRDPTicket(ctx, ticket, b.peer.ID, b.peer.SourceIP)
	if e != nil {
		return rdpworker.ControlReply{}, e
	}
	b.id, b.family = v.ID, f
	return rdpworker.ControlReply{SessionID: v.ID, LeaseUntil: v.LeaseExpiresAt}, nil
}

func (b *nativeRDPControl) PrepareTarget(ctx context.Context, id string) (json.RawMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" || id != b.id || b.prepared || b.finalized {
		return nil, fault.Denied
	}
	b.prepared = true // Target/preparation failure never restores the consumed ticket.
	var asset resource.Asset
	var v session.Session
	var out nativeRDPTarget
	e := b.app.db.Within(ctx, func(c context.Context) error {
		u, e := b.app.identity.NativeFamilyUser(c, b.family)
		if e != nil {
			return e
		}
		v, e = b.app.sessions.Get(c, id)
		if e != nil {
			return e
		}
		asset, e = b.app.checkLiveUser(c, v, u)
		if e != nil {
			return e
		}
		var owner bool
		if e = b.app.db.Row(c, `SELECT worker_id=$2 AND consumed_at IS NOT NULL FROM native_rdp_tickets WHERE session_id=$1`, id, b.peer.ID).Scan(&owner); e != nil || !owner {
			return fault.Denied
		}
		if asset.Protocol != "rdp" || v.State != "connecting" {
			return fault.Denied
		}
		pin := strings.TrimPrefix(asset.CertFingerprint, "sha256:")
		decoded, e := hex.DecodeString(pin)
		if e != nil || len(decoded) != 32 || len(pin) == len(asset.CertFingerprint) {
			return fault.Denied
		}
		cred, password, e := b.app.vault.GraphicsAuth(c, asset.CredentialID, v.ActorID, id)
		if e != nil {
			return e
		}
		if cred.Version != v.CredentialVersion {
			return fault.Denied
		}
		// Fail closed: an unavailable channel policy must not silently enable
		// clipboard transfer.
		policy, e := storage.New(b.app.db).Policy(c, v.ActorID)
		if e != nil {
			return e
		}
		out = nativeRDPTarget{ServerName: asset.Host, CertificateFingerprint: asset.CertFingerprint, Username: cred.Username, Password: password, Security: "nla", Readonly: v.Readonly,
			ClipboardCopy: policy.Copy, ClipboardPaste: policy.Paste && !v.Readonly}
		// Client-drive profile: gated by the platform switch. The user channel
		// policy decides each direction; upload is impossible on a readonly
		// session (the worker denies input, so nothing can initiate reads).
		driveUpload := policy.Upload && !v.Readonly
		driveDownload := policy.Download
		if b.app.config.RDPDriveEnabled && (driveUpload || driveDownload) {
			out.Drives, out.DriveUpload, out.DriveDownload = true, driveUpload, driveDownload
			out.DriveMaxReadOp, out.DriveMaxWriteOp = nativeDriveMaxOp, nativeDriveMaxOp
			out.DriveMaxReadTotal, out.DriveMaxWriteTotal = nativeDriveSessionTotal, nativeDriveSessionTotal
		}
		return b.app.audit.Detail(c, v.ActorID, "native.rdp_target_prepared", "", v.ClientIP, map[string]string{"sessionId": id, "assetId": asset.ID, "workerId": b.peer.ID,
			"clipboardCopy": strconv.FormatBool(out.ClipboardCopy), "clipboardPaste": strconv.FormatBool(out.ClipboardPaste), "drives": strconv.FormatBool(out.Drives)})
	})
	if e != nil {
		return nil, e
	}
	defer func() { out.Password = "" }()
	authorize := func(c context.Context) error { _, e := b.app.checkNativeLive(c, v, b.family); return e }
	bridge, e := rdpworker.NewTargetBridge(b.ctx, b.root, b.peer, v.LeaseExpiresAt, func(c context.Context) (net.Conn, error) {
		if e := authorize(c); e != nil {
			return nil, e
		}
		var dial connectivity.Dialer = connectivity.DialTCP
		switch {
		case asset.AgentID != "":
			dial = func(c context.Context, h string, p int) (net.Conn, error) {
				return b.app.connectivity.DialAgent(b.ctx, c, asset.AgentID, id, h, p, authorize)
			}
		case asset.JumpID != "":
			dial = b.app.jumpDialer(b.ctx, asset.JumpID, v.ActorID, id)
		case asset.GatewayGroupID != "":
			dial = b.app.groupDialer(b.ctx, asset.GatewayGroupID, v.ActorID, id, authorize)
		case asset.ProxyID != "":
			dial = b.app.proxyDialer(b.ctx, asset.ProxyID, v.ActorID, id)
		}
		return dial(c, asset.Host, asset.Port)
	})
	if e != nil {
		return nil, e
	}
	b.bridge = bridge
	out.SocketPath = bridge.Path()
	if b.recordingOptions != nil {
		opt := *b.recordingOptions
		enabled, err := b.app.audit.RecordingEnabled(ctx, id)
		if err != nil {
			bridge.Close()
			return nil, err
		}
		opt.Discard = !enabled
		opt.SessionID = b.id
		opt.Publish = func(ctx context.Context, seq int, seg rdprecording.Segment, path string) error {
			if seg.Bytes < 1 || seg.Bytes > audit.MaxMediaSegment {
				return fault.Invalid
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			payload, err := io.ReadAll(io.LimitReader(file, seg.Bytes+1))
			if err != nil {
				return err
			}
			if int64(len(payload)) != seg.Bytes {
				return fault.Conflict
			}
			commit, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			return b.app.audit.AppendMedia(commit, id, seq, seg.StartMS, seg.DurationMS, seg.SHA256, payload)
		}
		family := b.family
		opt.FirstFrame = func(ctx context.Context) error {
			check, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			return b.app.db.Within(check, func(c context.Context) error {
				if _, err := b.app.checkNativeLive(c, v, family); err != nil {
					return err
				}
				if err := b.app.sessions.Connected(c, id); err != nil {
					return err
				}
				if err := b.app.audit.BeginMedia(c, id); err != nil {
					return err
				}
				if err := b.app.audit.AccessState(c, id, "connected", "", false); err != nil {
					return err
				}
				return b.app.audit.Detail(c, v.ActorID, "native.rdp_connected", "", b.peer.SourceIP, map[string]string{"sessionId": id, "workerId": b.peer.ID, "profile": "record-view"})
			})
		}
		b.recorder, e = rdpworker.NewRecordingReceiver(b.ctx, b.root, b.peer, opt, bridge.Close)
		if e != nil {
			bridge.Close()
			return nil, e
		}
		out.RecordingSocket = b.recorder.Path
	}
	go func() {
		<-bridge.Done()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = b.Finalize(cleanup, id)
	}()
	payload, e := json.Marshal(out)
	if e != nil {
		bridge.Close()
	}
	return payload, e
}

// nativeRDPDriveOp mirrors the worker-side event struct; every field is
// validated so malformed worker JSON fails instead of polluting the audit.
type nativeRDPDriveOp struct {
	Op         int    `json:"op"`
	Direction  int    `json:"dir"`
	Device     int64  `json:"device"`
	Completion int64  `json:"completion"`
	Bytes      int64  `json:"bytes"`
	Offset     int64  `json:"offset"`
	Name       string `json:"name,omitempty"`
}

type nativeRDPDriveBatch struct {
	Ops        []nativeRDPDriveOp `json:"ops"`
	ReadTotal  int64              `json:"readTotal"`
	WriteTotal int64              `json:"writeTotal"`
}

var nativeRDPDriveActions = map[int]string{
	1: "native.rdp_drive_device",
	2: "native.rdp_drive_create",
	3: "native.rdp_drive_read",
	4: "native.rdp_drive_write",
	5: "native.rdp_drive_denied",
	6: "native.rdp_drive_denied",
	7: "native.rdp_drive_overflow",
	8: "native.rdp_drive_overflow",
}

func (b *nativeRDPControl) ReportDeviceIO(ctx context.Context, id string, payload json.RawMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" || id != b.id || b.finalized {
		return fault.Denied
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var batch nativeRDPDriveBatch
	if decoder.Decode(&batch) != nil {
		return fault.Invalid
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return fault.Invalid
	}
	if len(batch.Ops) > 256 || batch.ReadTotal < 0 || batch.WriteTotal < 0 ||
		batch.ReadTotal > 4<<30 || batch.WriteTotal > 4<<30 {
		return fault.Invalid
	}
	b.driveRead, b.driveWrite = batch.ReadTotal, batch.WriteTotal
	var actor string
	if u, e := b.app.identity.NativeFamilyUser(ctx, b.family); e == nil && u != nil {
		actor = u.ID
	}
	for _, op := range batch.Ops {
		if op.Op < 1 || op.Op > 8 || (op.Direction != 1 && op.Direction != 2) ||
			op.Device < 0 || op.Completion < 0 || op.Device > 1<<32 || op.Completion > 1<<32 ||
			op.Bytes < 0 || op.Offset < 0 || len(op.Name) > 32 {
			return fault.Invalid
		}
		b.driveEvents++
		switch op.Op {
		case 5, 6:
			b.driveDenied++
		case 7, 8:
			b.driveOverflow++
		}
		action := nativeRDPDriveActions[op.Op]
		if actor == "" || action == "" || b.driveEvents > nativeDriveEventBudget {
			continue
		}
		detail := map[string]string{"sessionId": id, "direction": strconv.Itoa(op.Direction), "bytes": strconv.FormatInt(op.Bytes, 10)}
		switch op.Op {
		case 3, 4:
			detail["device"] = strconv.FormatInt(op.Device, 10)
			detail["offset"] = strconv.FormatInt(op.Offset, 10)
			detail["completion"] = strconv.FormatInt(op.Completion, 10)
		}
		if op.Name != "" {
			detail["name"] = op.Name
		}
		if e := b.app.audit.Detail(ctx, actor, action, "", b.peer.SourceIP, detail); e != nil {
			return e
		}
	}
	return nil
}

func (b *nativeRDPControl) RenewSession(ctx context.Context, id string) (time.Time, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" || id != b.id || b.finalized {
		return time.Time{}, fault.Denied
	}
	expiry, e := b.app.renewNativeRDPSession(ctx, id, b.peer.ID)
	if errors.Is(e, session.ErrIdle) {
		b.idleExpired = true
	}
	if e == nil && b.bridge != nil {
		e = b.bridge.Renew(expiry)
	}
	if e != nil && b.bridge != nil {
		if b.recorder != nil {
			b.recorder.Close()
		}
		b.bridge.Close()
	}
	return expiry, e
}

func (b *nativeRDPControl) Finalize(ctx context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" || id != b.id {
		return fault.Denied
	}
	if b.finalized {
		return nil
	}
	if b.bridge != nil {
		b.bridge.Close()
	}
	if b.recorder != nil {
		b.recordingComplete = b.recorder.Drain(ctx)
		b.recorder = nil
	}
	e := b.app.db.Within(ctx, func(c context.Context) error {
		closed, e := b.app.sessions.LockFinalization(c, id)
		if e != nil {
			return e
		}
		if closed {
			return nil
		}
		state, reason := "interrupted", "native_rdp_worker_closed"
		if b.recordingComplete {
			state, reason = "closed", "native_rdp_desktop_closed"
		}
		if b.idleExpired {
			state, reason = "closed", "idle_timeout"
		}
		if e = b.app.sessions.Finish(c, id, state, reason); e != nil {
			return e
		}
		if e = b.app.audit.AccessState(c, id, state, reason, true); e != nil {
			return e
		}
		summary := map[string]string{"sessionId": id, "workerId": b.peer.ID}
		if b.driveEvents > 0 {
			summary["driveEvents"] = strconv.Itoa(b.driveEvents)
			summary["driveReadTotal"] = strconv.FormatInt(b.driveRead, 10)
			summary["driveWriteTotal"] = strconv.FormatInt(b.driveWrite, 10)
			summary["driveDenied"] = strconv.FormatInt(b.driveDenied, 10)
			summary["driveOverflow"] = strconv.FormatInt(b.driveOverflow, 10)
		}
		return b.app.audit.Detail(c, "", "native.rdp_worker_finalized", "", b.peer.SourceIP, summary)
	})
	if e == nil {
		b.finalized = true
	}
	return e
}
