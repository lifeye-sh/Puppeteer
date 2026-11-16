package systemupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"puppeteer/backend/internal/platform/runtimeprofile"
)

// The transaction journal lives on the host, outside all application volumes
// and rollback archives. A runner must hold LockTransaction for its lifetime.
// Journals contain no passwords, environment variables or command output.
type UpgradePlan struct {
	PrepareOnly        bool            `json:"prepareOnly,omitempty"`
	AutomationRevision uint64          `json:"automationRevision,omitempty"`
	StartBefore        *time.Time      `json:"startBefore,omitempty"`
	AllowSchemaChange  bool            `json:"allowSchemaChange,omitempty"`
	ID                 string          `json:"id"`
	Actor              string          `json:"actor"`
	Source             string          `json:"source"`
	Target             string          `json:"target"`
	LayoutDigest       string          `json:"layoutDigest"`
	Envelope           json.RawMessage `json:"envelope"`
}

type UpgradeEvent struct {
	Stage string    `json:"stage"`
	At    time.Time `json:"at"`
}

type UpgradeJournal struct {
	Format     int            `json:"format"`
	Plan       UpgradePlan    `json:"plan"`
	PlanDigest string         `json:"planDigest"`
	Stage      string         `json:"stage"`
	Reason     string         `json:"reason,omitempty"`
	Started    time.Time      `json:"started"`
	Updated    time.Time      `json:"updated"`
	Events     []UpgradeEvent `json:"events"`
}

func (j UpgradeJournal) Terminal() bool {
	return j.Stage == "succeeded" || j.Stage == "prepared" || j.Stage == "rolled_back" || j.Stage == "failed"
}

// UpgradeDriver implements fixed host operations, never arbitrary downloaded
// commands. Every method is idempotent. Prepare must verify source version,
// layout hash, actor authorization, available space, immutable images and
// candidate freeze support. Quiesce must repeat authorization/layout checks,
// fence new requests, drain writers, and stop ALL data-writing components.
// Backup and ValidateBackup cover database, files, keys and configuration as
// one consistent set; Restore must restore that entire set with writers stopped.
// Verify checks the target while frozen. Activate is the write-open boundary.
// ResumePrevious reopens the old deployment only AFTER rollback configuration
// and data are ready, and must never restore data itself.
type UpgradeDriver interface {
	Prepare(context.Context, UpgradePlan, Release) error
	Quiesce(context.Context, UpgradePlan) error
	Backup(context.Context, UpgradePlan) error
	ValidateBackup(context.Context, UpgradePlan) error
	Switch(context.Context, UpgradePlan, Release) error
	Verify(context.Context, UpgradePlan, Release) error
	Activate(context.Context, UpgradePlan) error
	Fence(context.Context, UpgradePlan) error
	Restore(context.Context, UpgradePlan) error
	ResumePrevious(context.Context, UpgradePlan) error
}

type Transaction struct {
	path    string
	journal UpgradeJournal
	keys    map[string]ed25519.PublicKey
	now     func() time.Time
	// Replaceable only in package tests to inject durability failures.
	save func(string, any) error
}

var upgradeID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var layoutHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var ErrUpgradeJournal = errors.New("升级状态无法持久化，保持维护状态并等待恢复")
var ErrUpgradeRecovery = errors.New("升级恢复未完成，保持维护状态；请检查宿主机任务记录")
var ErrUpgradeBusy = errors.New("已有升级任务或尚未完成的恢复任务")

// ResolveTarget verifies a preview; CreateTransaction additionally enforces the
// host's persistent release high-water mark when accepting the operation.
func ResolveTarget(p UpgradePlan, keys map[string]ed25519.PublicKey) (Release, error) {
	r, _, _, e := trustedTarget(p, keys, time.Now(), 0, "")
	return r, e
}

func planDigest(p UpgradePlan) string {
	raw, _ := json.Marshal(p)
	d := sha256.Sum256(raw)
	return hex.EncodeToString(d[:])
}

func trustedTarget(p UpgradePlan, keys map[string]ed25519.PublicKey, now time.Time, seq uint64, digest string) (Release, Catalog, string, error) {
	if !upgradeID.MatchString(p.ID) || p.Actor == "" || len(p.Actor) > 128 || !layoutHash.MatchString(p.LayoutDigest) {
		return Release{}, Catalog{}, "", errors.New("升级任务标识或部署快照无效")
	}
	c, hash, e := Verify(p.Envelope, keys, now, seq, digest)
	if e != nil {
		return Release{}, c, hash, e
	}
	for _, r := range c.Releases {
		if r.Version != p.Target {
			continue
		}
		cmp, err := Compare(p.Source, r.Version)
		min, err2 := Compare(p.Source, r.MinSource)
		if err != nil || err2 != nil || cmp >= 0 || min < 0 {
			return Release{}, c, hash, errors.New("不支持该来源版本的升级或降级")
		}
		return r, c, hash, nil
	}
	return Release{}, c, hash, errors.New("目标版本不在可信发布索引中")
}

// CreateTransaction must run under the same host lock used by Run. Repeating
// the exact request returns the original task, including its terminal outcome.
// The independent host high-water mark is not rolled back with application data.
func CreateTransaction(dir string, p UpgradePlan, keys map[string]ed25519.PublicKey) (*Transaction, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("升级状态目录必须为绝对路径")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if !upgradeID.MatchString(p.ID) {
		return nil, errors.New("升级任务标识无效")
	}
	// The idempotency key remains reserved after newer tasks replace upgrade.json.
	archived, archiveErr := readTransaction(filepath.Join(dir, p.ID+".json"), keys)
	if archiveErr == nil {
		if archived.journal.PlanDigest != planDigest(p) {
			return nil, errors.New("相同请求标识不能用于不同升级计划")
		}
		if !archived.journal.Terminal() {
			return nil, ErrUpgradeJournal
		}
		return archived, nil
	}
	if !os.IsNotExist(archiveErr) {
		return nil, archiveErr
	}
	t, e := OpenTransaction(dir, keys)
	if e == nil {
		if t.journal.Plan.ID == p.ID {
			if t.journal.PlanDigest != planDigest(p) {
				return nil, errors.New("相同请求标识不能用于不同升级计划")
			}
			return t, nil
		}
		if !t.journal.Terminal() {
			return nil, ErrUpgradeBusy
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	var high struct {
		Sequence uint64 `json:"sequence"`
		Digest   string `json:"digest"`
	}
	highPath := filepath.Join(dir, "release-high-water.json")
	raw, e := os.ReadFile(highPath)
	if e == nil && strict(raw, &high) != nil {
		return nil, ErrUpgradeJournal
	}
	if e != nil && !os.IsNotExist(e) {
		return nil, ErrUpgradeJournal
	}
	_, c, digest, e := trustedTarget(p, keys, time.Now(), high.Sequence, high.Digest)
	if e != nil {
		return nil, e
	}
	high.Sequence, high.Digest = c.Sequence, digest
	if e = runtimeprofile.AtomicJSON(highPath, high); e != nil {
		return nil, ErrUpgradeJournal
	}
	if t != nil {
		if e = runtimeprofile.AtomicJSON(filepath.Join(dir, t.journal.Plan.ID+".json"), t.journal); e != nil {
			return nil, ErrUpgradeJournal
		}
	}
	now := time.Now().UTC()
	p.Envelope = append(json.RawMessage(nil), p.Envelope...)
	t = &Transaction{path: filepath.Join(dir, "upgrade.json"), keys: keys, now: time.Now, save: runtimeprofile.AtomicJSON,
		journal: UpgradeJournal{Format: 1, Plan: p, PlanDigest: planDigest(p), Started: now}}
	if e = t.stage("queued", ""); e != nil {
		return nil, e
	}
	return t, nil
}

func OpenTransaction(dir string, keys map[string]ed25519.PublicKey) (*Transaction, error) {
	return readTransaction(filepath.Join(dir, "upgrade.json"), keys)
}

func ReadUpgradeTask(dir, id string) (UpgradeJournal, error) {
	if !upgradeID.MatchString(id) {
		return UpgradeJournal{}, ErrUpgradeJournal
	}
	if tx, e := OpenTransaction(dir, nil); e == nil && tx.View().Plan.ID == id {
		return tx.View(), nil
	}
	tx, e := readTransaction(filepath.Join(dir, id+".json"), nil)
	if e != nil {
		return UpgradeJournal{}, e
	}
	if tx.View().Plan.ID != id {
		return UpgradeJournal{}, ErrUpgradeJournal
	}
	return tx.View(), nil
}

func readTransaction(path string, keys map[string]ed25519.PublicKey) (*Transaction, error) {
	t := &Transaction{path: path, keys: keys, now: time.Now, save: runtimeprofile.AtomicJSON}
	f, e := os.Open(t.path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 2*MaxIndexBytes {
		return nil, ErrUpgradeJournal
	}
	raw, e := io.ReadAll(io.LimitReader(f, 2*MaxIndexBytes+1))
	if e != nil || len(raw) > 2*MaxIndexBytes || strict(raw, &t.journal) != nil {
		return nil, ErrUpgradeJournal
	}
	j := t.journal
	if j.Format != 1 || !upgradeID.MatchString(j.Plan.ID) || j.PlanDigest != planDigest(j.Plan) || !knownStage(j.Stage) {
		return nil, ErrUpgradeJournal
	}
	return t, nil
}

// UpgradeBlocked fails closed on corrupted/inaccessible journals. Read-only
// callers use it to exclude deployment, restore and scheduled maintenance.
func UpgradeBlocked(dir string) bool {
	t, e := OpenTransaction(dir, nil)
	if os.IsNotExist(e) {
		return false
	}
	return e != nil || !t.journal.Terminal()
}

func (t *Transaction) View() UpgradeJournal {
	j := t.journal
	j.Events = append([]UpgradeEvent(nil), j.Events...)
	j.Plan.Envelope = append(json.RawMessage(nil), j.Plan.Envelope...)
	return j
}

func knownStage(s string) bool {
	switch s {
	case "queued", "preparing", "prepared", "quiescing", "backing_up", "switching", "verifying", "committing", "rolling_back", "rollback_committing", "succeeded", "rolled_back", "failed", "recovery_required":
		return true
	}
	return false
}

func (t *Transaction) stage(s, reason string) error {
	next := t.View()
	next.Stage, next.Reason, next.Updated = s, reason, t.now().UTC()
	next.Events = append(next.Events, UpgradeEvent{Stage: s, At: next.Updated})
	if len(next.Events) > 128 {
		next.Events = next.Events[len(next.Events)-128:]
	}
	if t.save(t.path, next) != nil {
		return ErrUpgradeJournal
	}
	t.journal = next
	return nil
}

// Run is independent of HTTP, the application and the deployment daemon's
// lifetimes. On process restart it recovers the durable intent, never repeats a
// migration blindly. Cancellation before write-open triggers bounded recovery.
func (t *Transaction) Run(ctx context.Context, d UpgradeDriver) error {
	if d == nil {
		return errors.New("缺少升级部署适配器")
	}
	if t.journal.Terminal() {
		return nil
	}
	switch t.journal.Stage {
	case "committing":
		return t.commit(ctx, d, false)
	case "rollback_committing":
		return t.commit(ctx, d, true)
	case "quiescing", "backing_up":
		return t.recover(d, false, "升级进程在备份前后中断，恢复原部署")
	case "switching", "verifying", "rolling_back":
		return t.recover(d, true, "升级进程中断，恢复配套备份")
	case "recovery_required":
		return ErrUpgradeRecovery // Explicit operator recovery required.
	}
	p := t.journal.Plan
	r, _, _, e := trustedTarget(p, t.keys, t.now(), 0, "")
	if e != nil {
		return t.fail("签名发布信息已失效；未开始停机")
	}
	steps := []struct {
		stage string
		run   func() error
	}{
		{"preparing", func() error { return d.Prepare(ctx, p, r) }},
		{"quiescing", func() error { return d.Quiesce(ctx, p) }},
		{"backing_up", func() error {
			if e := d.Backup(ctx, p); e != nil {
				return e
			}
			return d.ValidateBackup(ctx, p)
		}},
		{"switching", func() error { return d.Switch(ctx, p, r) }},
		{"verifying", func() error { return d.Verify(ctx, p, r) }},
	}
	for _, step := range steps {
		// A download may outlast the index validity. Revalidate before downtime.
		if step.stage == "quiescing" {
			if p.PrepareOnly {
				return t.stage("prepared", "镜像与部署准备已完成，未停止业务；正式升级将重新预检")
			}
			if p.StartBefore != nil && !t.now().Before(*p.StartBefore) {
				return t.fail("维护窗口或授权已到期；未开始停机")
			}
			if _, _, _, e = trustedTarget(p, t.keys, t.now(), 0, ""); e != nil {
				return t.fail("发布索引在准备期间过期；未开始停机")
			}
		}
		if e = t.stage(step.stage, ""); e != nil {
			return t.handleJournalFailure(d, e)
		}
		if e = ctx.Err(); e == nil {
			e = step.run()
		}
		if e != nil {
			if step.stage == "preparing" {
				return t.fail("升级准备失败；原部署未改变" + operationReason(e))
			}
			return t.recover(d, step.stage == "switching" || step.stage == "verifying", "升级在 "+step.stage+" 阶段失败"+operationReason(e))
		}
	}
	// Durably record this boundary BEFORE allowing the first business write.
	if e = t.stage("committing", ""); e != nil {
		return t.handleJournalFailure(d, e)
	}
	return t.commit(ctx, d, false)
}

func (t *Transaction) fail(reason string) error {
	if e := t.stage("failed", reason); e != nil {
		return e
	}
	return errors.New(reason)
}

func (t *Transaction) commit(ctx context.Context, d UpgradeDriver, previous bool) error {
	var e error
	if previous {
		e = d.ResumePrevious(ctx, t.journal.Plan)
	} else {
		e = d.Activate(ctx, t.journal.Plan)
	}
	// A timeout/error can occur AFTER writes opened. Never restore a backup
	// from this point. Keep the committing stage so restart only retries activation.
	if e != nil {
		reason := "业务开放状态待核验；不会自动回退已开放写入的数据" + operationReason(e)
		// Keep the write-open intent while reporting a failed verification. Even
		// if this diagnostic cannot be saved, the earlier intent remains durable.
		if err := t.stage(t.journal.Stage, reason); err != nil {
			return err
		}
		return errors.New(reason)
	}
	if previous {
		return t.stage("rolled_back", t.journal.Reason)
	}
	return t.stage("succeeded", "")
}

func (t *Transaction) recover(d UpgradeDriver, restore bool, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if restore {
		if e := t.stage("rolling_back", reason); e != nil {
			return t.handleJournalFailure(d, e)
		}
		if e := d.Fence(ctx, t.journal.Plan); e != nil {
			return t.recoveryRequired("无法关闭写入，未执行数据恢复" + operationReason(e))
		}
		if e := d.ValidateBackup(ctx, t.journal.Plan); e != nil {
			return t.recoveryRequired("备份校验失败，保持业务关闭" + operationReason(e))
		}
		if e := d.Restore(ctx, t.journal.Plan); e != nil {
			return t.recoveryRequired("配套数据恢复失败，保持业务关闭" + operationReason(e))
		}
	}
	if e := t.stage("rollback_committing", reason); e != nil {
		return t.handleJournalFailure(d, e)
	}
	return t.commit(ctx, d, true)
}

func (t *Transaction) recoveryRequired(reason string) error {
	if e := t.stage("recovery_required", reason); e != nil {
		return e
	}
	return ErrUpgradeRecovery
}

func (t *Transaction) handleJournalFailure(d UpgradeDriver, cause error) error {
	// Do not attempt any unjournaled restore or release. The previous durable
	// stage tells the next runner whether a full paired restore is necessary.
	if t.journal.Stage != "queued" && t.journal.Stage != "preparing" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = d.Fence(ctx, t.journal.Plan)
	}
	return cause
}
