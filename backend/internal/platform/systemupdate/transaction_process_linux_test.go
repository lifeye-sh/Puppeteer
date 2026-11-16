//go:build linux

package systemupdate

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// A real child process is killed after a durable stage but before returning
// from its host operation. The operation fixture does not replace Docker tests.
func TestUpgradeCrashProcessHelper(t *testing.T) {
	dir := os.Getenv("PUPPETEER_UPGRADE_CRASH_FIXTURE")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	pub, err := base64.StdEncoding.DecodeString(os.Getenv("PUPPETEER_UPGRADE_FIXTURE_PUBLIC"))
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := LockTransaction(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	tx, err := OpenTransaction(dir, map[string]ed25519.PublicKey{"fixture": pub})
	if err != nil {
		t.Fatal(err)
	}
	d := &upgradeFixture{hook: func(stage string) {
		if stage == os.Getenv("PUPPETEER_UPGRADE_CRASH_STAGE") {
			if err := os.WriteFile(filepath.Join(dir, "operation-reached"), []byte(stage), 0600); err != nil {
				t.Fatal(err)
			}
			for {
				time.Sleep(time.Hour)
			}
		}
	}}
	if err = tx.Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeRecoveryAfterKilledRunner(t *testing.T) {
	for _, at := range []string{"switch", "activate"} {
		t.Run(at, func(t *testing.T) {
			tx, keys := transactionFixture(t)
			dir := filepath.Dir(tx.path)
			cmd := exec.Command(os.Args[0], "-test.run=^TestUpgradeCrashProcessHelper$")
			cmd.Env = append(os.Environ(), "PUPPETEER_UPGRADE_CRASH_FIXTURE="+dir, "PUPPETEER_UPGRADE_FIXTURE_PUBLIC="+base64.StdEncoding.EncodeToString(keys["fixture"]), "PUPPETEER_UPGRADE_CRASH_STAGE="+at)
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, e := os.Stat(filepath.Join(dir, "operation-reached")); e == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child did not reach target stage")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if release, e := LockTransaction(dir); !errors.Is(e, ErrUpgradeBusy) {
				if release != nil {
					release()
				}
				t.Fatal("live child did not hold host lock", e)
			}
			if e := cmd.Process.Kill(); e != nil {
				t.Fatal(e)
			}
			_ = cmd.Wait()
			unlock, e := LockTransaction(dir)
			if e != nil {
				t.Fatal("killed runner retained lock", e)
			}
			defer unlock()
			recovered, e := OpenTransaction(dir, keys)
			if e != nil {
				t.Fatal(e)
			}
			d := &upgradeFixture{}
			if e = recovered.Run(context.Background(), d); e != nil {
				t.Fatal(e)
			}
			want := []string{"fence", "validate", "restore", "resume"}
			if at == "activate" {
				want = []string{"activate"}
			}
			if !reflect.DeepEqual(d.calls, want) {
				t.Fatal("wrong recovery after process death", d.calls)
			}
		})
	}
}
