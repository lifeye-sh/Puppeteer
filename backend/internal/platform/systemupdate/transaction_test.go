package systemupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type upgradeFixture struct {
	calls []string
	fail  string
	hook  func(string)
}

func (f *upgradeFixture) step(s string) error {
	f.calls = append(f.calls, s)
	if f.hook != nil {
		f.hook(s)
	}
	if f.fail == s {
		return errors.New("SECRET fixture password; must not be journaled")
	}
	return nil
}
func (f *upgradeFixture) Prepare(context.Context, UpgradePlan, Release) error {
	return f.step("prepare")
}
func (f *upgradeFixture) Quiesce(context.Context, UpgradePlan) error { return f.step("quiesce") }
func (f *upgradeFixture) Backup(context.Context, UpgradePlan) error  { return f.step("backup") }
func (f *upgradeFixture) ValidateBackup(context.Context, UpgradePlan) error {
	return f.step("validate")
}
func (f *upgradeFixture) Switch(context.Context, UpgradePlan, Release) error { return f.step("switch") }
func (f *upgradeFixture) Verify(context.Context, UpgradePlan, Release) error { return f.step("verify") }
func (f *upgradeFixture) Activate(context.Context, UpgradePlan) error        { return f.step("activate") }
func (f *upgradeFixture) Fence(context.Context, UpgradePlan) error           { return f.step("fence") }
func (f *upgradeFixture) Restore(context.Context, UpgradePlan) error         { return f.step("restore") }
func (f *upgradeFixture) ResumePrevious(context.Context, UpgradePlan) error  { return f.step("resume") }

func transactionFixture(t *testing.T) (*Transaction, map[string]ed25519.PublicKey) {
	t.Helper()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	r := Release{Version: "beta0.11", MinSource: "beta0.10", Channel: "beta", PublishedAt: now, Platform: "linux/amd64", SchemaVersion: 110, CapabilitiesRevision: 90, NotesURL: "https://github.com/lifeye-sh/Puppeteer/releases/tag/beta0.11"}
	for _, component := range []string{"server", "web", "graphics", "renderer"} {
		digest := "sha256:" + strings.Repeat("a", 64)
		r.Images = append(r.Images, Image{Component: component, Digest: digest, PinnedReference: "cloners/puppeteer-" + component + "@" + digest})
	}
	payload, _ := json.Marshal(Catalog{Product: "Puppeteer", Format: 1, Sequence: 2, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Releases: []Release{r}})
	raw, _ := json.Marshal(Envelope{KeyID: "fixture", Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))})
	keys := map[string]ed25519.PublicKey{"fixture": pub}
	tx, e := CreateTransaction(t.TempDir(), UpgradePlan{ID: strings.Repeat("a", 32), Actor: "admin-id", Source: "beta0.10", Target: "beta0.11", LayoutDigest: strings.Repeat("b", 64), Envelope: raw}, keys)
	if e != nil {
		t.Fatal(e)
	}
	return tx, keys
}

func TestUpgradeTransactionSuccessAndIdempotence(t *testing.T) {
	tx, keys := transactionFixture(t)
	d := &upgradeFixture{}
	if e := tx.Run(context.Background(), d); e != nil {
		t.Fatal(e)
	}
	want := []string{"prepare", "quiesce", "backup", "validate", "switch", "verify", "activate"}
	if !reflect.DeepEqual(d.calls, want) || tx.View().Stage != "succeeded" {
		t.Fatal(d.calls, tx.View().Stage)
	}
	if UpgradeBlocked(filepath.Dir(tx.path)) {
		t.Fatal("completed task blocked")
	}
	again, e := CreateTransaction(filepath.Dir(tx.path), tx.View().Plan, keys)
	if e != nil {
		t.Fatal(e)
	}
	if e = again.Run(context.Background(), d); e != nil || !reflect.DeepEqual(d.calls, want) {
		t.Fatal("duplicate repeated effects", e, d.calls)
	}
	p := tx.View().Plan
	p.Target = "beta0.12"
	if _, e = CreateTransaction(filepath.Dir(tx.path), p, keys); e == nil {
		t.Fatal("conflicting id accepted")
	}
}

func TestUpgradeStageFailures(t *testing.T) {
	for _, tc := range []struct {
		at, stage string
		calls     []string
	}{
		{"prepare", "failed", []string{"prepare"}},
		{"quiesce", "rolled_back", []string{"prepare", "quiesce", "resume"}},
		{"backup", "rolled_back", []string{"prepare", "quiesce", "backup", "resume"}},
		{"validate", "rolled_back", []string{"prepare", "quiesce", "backup", "validate", "resume"}},
		{"switch", "rolled_back", []string{"prepare", "quiesce", "backup", "validate", "switch", "fence", "validate", "restore", "resume"}},
		{"verify", "rolled_back", []string{"prepare", "quiesce", "backup", "validate", "switch", "verify", "fence", "validate", "restore", "resume"}},
		{"activate", "committing", []string{"prepare", "quiesce", "backup", "validate", "switch", "verify", "activate"}},
	} {
		t.Run(tc.at, func(t *testing.T) {
			tx, _ := transactionFixture(t)
			d := &upgradeFixture{fail: tc.at}
			_ = tx.Run(context.Background(), d)
			if tx.View().Stage != tc.stage || !reflect.DeepEqual(d.calls, tc.calls) {
				t.Fatal(tx.View().Stage, d.calls)
			}
			raw, _ := os.ReadFile(tx.path)
			if strings.Contains(string(raw), "SECRET") {
				t.Fatal("command details leaked")
			}
		})
	}
}

func TestUpgradeRestartRecovery(t *testing.T) {
	for _, tc := range []struct {
		stage, final string
		calls        []string
	}{
		{"quiescing", "rolled_back", []string{"resume"}},
		{"backing_up", "rolled_back", []string{"resume"}},
		{"switching", "rolled_back", []string{"fence", "validate", "restore", "resume"}},
		{"verifying", "rolled_back", []string{"fence", "validate", "restore", "resume"}},
		{"rolling_back", "rolled_back", []string{"fence", "validate", "restore", "resume"}},
		{"committing", "succeeded", []string{"activate"}},
		{"rollback_committing", "rolled_back", []string{"resume"}},
		{"recovery_required", "recovery_required", nil},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			tx, keys := transactionFixture(t)
			if e := tx.stage(tc.stage, ""); e != nil {
				t.Fatal(e)
			}
			restarted, e := OpenTransaction(filepath.Dir(tx.path), keys)
			if e != nil {
				t.Fatal(e)
			}
			d := &upgradeFixture{}
			_ = restarted.Run(context.Background(), d)
			if restarted.View().Stage != tc.final || !reflect.DeepEqual(d.calls, tc.calls) {
				t.Fatal(restarted.View().Stage, d.calls)
			}
		})
	}
}

func TestUpgradeRecoveryFailureNeverOpensWrites(t *testing.T) {
	for _, at := range []string{"fence", "validate", "restore"} {
		t.Run(at, func(t *testing.T) {
			tx, _ := transactionFixture(t)
			_ = tx.stage("switching", "")
			d := &upgradeFixture{fail: at}
			if !errors.Is(tx.Run(context.Background(), d), ErrUpgradeRecovery) {
				t.Fatal("missing recovery failure")
			}
			if tx.View().Stage != "recovery_required" || !UpgradeBlocked(filepath.Dir(tx.path)) {
				t.Fatal("unblocked incomplete recovery")
			}
			for _, c := range d.calls {
				if c == "resume" || c == "activate" {
					t.Fatal("opened writes", d.calls)
				}
			}
		})
	}
}

func TestUpgradeDiskFailureAtCommitNeverRollsBackNewWrites(t *testing.T) {
	tx, keys := transactionFixture(t)
	original := tx.save
	d := &upgradeFixture{hook: func(step string) {
		if step == "activate" {
			tx.save = func(string, any) error { return errors.New("disk full") }
		}
	}}
	if !errors.Is(tx.Run(context.Background(), d), ErrUpgradeJournal) {
		t.Fatal("missing journal error")
	}
	tx.save = original
	restarted, e := OpenTransaction(filepath.Dir(tx.path), keys)
	if e != nil {
		t.Fatal(e)
	}
	if restarted.View().Stage != "committing" {
		t.Fatal("lost durable write-open intent")
	}
	retry := &upgradeFixture{}
	if e = restarted.Run(context.Background(), retry); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(retry.calls, []string{"activate"}) {
		t.Fatal("restored after write-open", retry.calls)
	}
}

func TestUpgradeJournalFailureBeforeSwitchKeepsOriginalData(t *testing.T) {
	tx, keys := transactionFixture(t)
	original := tx.save
	d := &upgradeFixture{hook: func(step string) {
		if step == "validate" {
			tx.save = func(string, any) error { return errors.New("disk full") }
		}
	}}
	if !errors.Is(tx.Run(context.Background(), d), ErrUpgradeJournal) {
		t.Fatal("missing journal error")
	}
	tx.save = original
	restarted, e := OpenTransaction(filepath.Dir(tx.path), keys)
	if e != nil {
		t.Fatal(e)
	}
	if restarted.View().Stage != "backing_up" {
		t.Fatal("wrong persisted phase")
	}
	retry := &upgradeFixture{}
	if e = restarted.Run(context.Background(), retry); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(retry.calls, []string{"resume"}) {
		t.Fatal(retry.calls)
	}
}

func TestUpgradeCancelRecoversWithIndependentContext(t *testing.T) {
	tx, _ := transactionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	d := &upgradeFixture{hook: func(step string) {
		if step == "switch" {
			cancel()
		}
	}}
	_ = tx.Run(ctx, d)
	if tx.View().Stage != "rolled_back" {
		t.Fatal(tx.View().Stage)
	}
	if !reflect.DeepEqual(d.calls[len(d.calls)-4:], []string{"fence", "validate", "restore", "resume"}) {
		t.Fatal(d.calls)
	}
}

func TestUpgradeExpiredIndexCannotStartDowntimeButCanRecover(t *testing.T) {
	tx, _ := transactionFixture(t)
	d := &upgradeFixture{hook: func(step string) {
		if step == "prepare" {
			tx.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
		}
	}}
	_ = tx.Run(context.Background(), d)
	if tx.View().Stage != "failed" || !reflect.DeepEqual(d.calls, []string{"prepare"}) {
		t.Fatal(tx.View().Stage, d.calls)
	}
	_ = tx.stage("switching", "")
	d = &upgradeFixture{}
	if e := tx.Run(context.Background(), d); e != nil {
		t.Fatal(e)
	}
	if tx.View().Stage != "rolled_back" {
		t.Fatal("expired index blocked recovery")
	}
}

func TestUpgradeCorruptJournalAndConcurrentIntentFailClosed(t *testing.T) {
	tx, keys := transactionFixture(t)
	p := tx.View().Plan
	p.ID = strings.Repeat("c", 32)
	if _, e := CreateTransaction(filepath.Dir(tx.path), p, keys); !errors.Is(e, ErrUpgradeBusy) {
		t.Fatal(e)
	}
	if e := os.WriteFile(tx.path, []byte(`{"stage":"succeeded"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if !UpgradeBlocked(filepath.Dir(tx.path)) {
		t.Fatal("corrupt journal permitted deployment")
	}
	if _, e := CreateTransaction(filepath.Dir(tx.path), p, keys); e == nil {
		t.Fatal("overwrote corrupt journal")
	}
}

func TestUpgradeArchivedIDCannotRepeatOrReplaceCurrentTask(t *testing.T) {
	tx, keys := transactionFixture(t)
	p := tx.View().Plan
	if e := tx.Run(context.Background(), &upgradeFixture{}); e != nil {
		t.Fatal(e)
	}
	next := p
	next.ID = strings.Repeat("d", 32)
	if _, e := CreateTransaction(filepath.Dir(tx.path), next, keys); e != nil {
		t.Fatal(e)
	}
	old, e := CreateTransaction(filepath.Dir(tx.path), p, keys)
	if e != nil || old.View().Stage != "succeeded" {
		t.Fatal("idempotency lost after archive", e)
	}
	d := &upgradeFixture{}
	if e = old.Run(context.Background(), d); e != nil || len(d.calls) != 0 {
		t.Fatal("archived request repeated effects")
	}
	active, e := OpenTransaction(filepath.Dir(tx.path), keys)
	if e != nil || active.View().Plan.ID != next.ID {
		t.Fatal("archived request replaced current task")
	}
}

func TestUpgradeHostHighWaterAndSignatureAreIndependent(t *testing.T) {
	tx, keys := transactionFixture(t)
	if e := tx.Run(context.Background(), &upgradeFixture{}); e != nil {
		t.Fatal(e)
	}
	p := tx.View().Plan
	p.ID = strings.Repeat("e", 32)
	path := filepath.Join(filepath.Dir(tx.path), "release-high-water.json")
	if e := os.WriteFile(path, []byte(`{"sequence":3,"digest":"retained-on-host"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := CreateTransaction(filepath.Dir(tx.path), p, keys); e == nil {
		t.Fatal("application rollback bypassed host high-water mark")
	}
	if e := os.WriteFile(path, []byte(`{"sequence":2,"digest":"conflicting-release"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := CreateTransaction(filepath.Dir(tx.path), p, keys); e == nil {
		t.Fatal("same sequence equivocation accepted")
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if _, e := CreateTransaction(filepath.Dir(tx.path), p, nil); e == nil {
		t.Fatal("host trusted application verification without its own keys")
	}
}
