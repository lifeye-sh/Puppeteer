package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"next-terminal/backend/internal/application"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/postgres"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "routes" {
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: nt routes")
			os.Exit(2)
		}
		if e := json.NewEncoder(os.Stdout).Encode(map[string]any{"release": application.Release, "scope": "registered HTTP routes only; not business or permission acceptance", "routes": application.RegisteredHTTPRoutes()}); e != nil {
			fmt.Fprintln(os.Stderr, "route export failed")
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "settings" {
		if err := settingsCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "settings command failed:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "restore" && os.Args[2] == "prepare" {
		if err := prepareRestore(os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, "restore preparation failed:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		if err := backupCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "backup command failed:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "restore" {
		if e := restoreDeployment(os.Args[2:]); e != nil {
			fmt.Fprintln(os.Stderr, "restore command failed:", e)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "seed-r03" || os.Args[1] == "seed-r04") {
		f := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
		operator := f.String("operator", "", "existing administrator")
		_ = f.Parse(os.Args[2:])
		if f.NArg() != 0 {
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		p, e := application.Open(ctx)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		defer p.Close()
		key, _ := hex.DecodeString(os.Getenv("NT_MFA_KEY"))
		vaultKey, _ := hex.DecodeString(os.Getenv("NT_VAULT_KEY"))
		a := application.New(p, application.Config{EncryptionKey: key, VaultKey: vaultKey})
		var result map[string]string
		if os.Args[1] == "seed-r04" {
			result, e = a.SeedR04(ctx, *operator)
		} else {
			result, e = a.SeedR03(ctx, *operator)
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, "fixture setup failed:", e)
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(result)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "seed-r02" {
		f := flag.NewFlagSet("seed-r02", flag.ExitOnError)
		operator := f.String("operator", "", "existing administrator")
		_ = f.Parse(os.Args[2:])
		if f.NArg() != 0 {
			os.Exit(2)
		}
		var users []application.FixtureUser
		decoder := json.NewDecoder(io.LimitReader(os.Stdin, 16384))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&users) != nil {
			fmt.Fprintln(os.Stderr, "invalid fixture JSON on stdin")
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		p, e := application.Open(ctx)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		defer p.Close()
		key, _ := hex.DecodeString(os.Getenv("NT_MFA_KEY"))
		vaultKey, _ := hex.DecodeString(os.Getenv("NT_VAULT_KEY"))
		a := application.New(p, application.Config{EncryptionKey: key, VaultKey: vaultKey})
		if e = a.SeedR02(ctx, *operator, users); e != nil {
			fmt.Fprintln(os.Stderr, "fixture setup failed; existing accounts were not overwritten:", e)
			os.Exit(1)
		}
		fmt.Println("R02 fixtures ready: operator, reviewer, denied; two departments and two roles.")
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "reset-mfa" || os.Args[1] == "enable-password-login") {
		f := flag.NewFlagSet("reset-mfa", flag.ExitOnError)
		username := f.String("user", "", "target username")
		operator := f.String("operator", "", "local operator name")
		reason := f.String("reason", "", "recovery reason, at least 5 characters")
		_ = f.Parse(os.Args[2:])
		if f.NArg() != 0 {
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		p, e := application.Open(ctx)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		defer p.Close()
		key, _ := hex.DecodeString(os.Getenv("NT_MFA_KEY"))
		vaultKey, _ := hex.DecodeString(os.Getenv("NT_VAULT_KEY"))
		a := application.New(p, application.Config{EncryptionKey: key, VaultKey: vaultKey})
		if os.Args[1] == "enable-password-login" {
			e = a.EnablePasswordLoginFromCLI(ctx, *operator, *reason)
		} else {
			e = a.ResetMFAFromCLI(ctx, *username, *operator, *reason)
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, "MFA reset failed:", e)
			os.Exit(1)
		}
		if os.Args[1] == "enable-password-login" {
			fmt.Println("Password login re-enabled; recovery action audited.")
		} else {
			fmt.Println("MFA reset recorded; sessions revoked; next login requires enrollment.")
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "health" {
		client := &http.Client{Timeout: 2 * time.Second}
		res, err := client.Get("http://127.0.0.1:8089/readyz")
		if err != nil {
			os.Exit(1)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 2 || (os.Args[1] != "status" && os.Args[1] != "migrate") {
		fmt.Fprintln(os.Stderr, "usage: nt status|migrate|health (development environment only)")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, e := application.Open(ctx)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer p.Close()
	s, e := system.New(postgres.New(p)).State(ctx)
	if e != nil {
		fmt.Fprintln(os.Stderr, "cannot read instance state")
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(s)
}
