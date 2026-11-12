package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"next-terminal/backend/internal/modules/audit"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/postgres"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"next-terminal/backend/migrations"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"
)

func readSettingsPatch(input io.Reader) (map[string]json.RawMessage, error) {
	raw, err := io.ReadAll(io.LimitReader(input, 512*1024+1))
	if err != nil || len(raw) > 512*1024 {
		return nil, errors.New("configuration patch exceeds 512 KiB")
	}
	var body struct {
		Set map[string]json.RawMessage `json:"set"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || len(body.Set) == 0 {
		return nil, errors.New("expected JSON object with non-empty set")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("unexpected trailing JSON")
	}
	return body.Set, nil
}

// Local database credentials are the privilege boundary. Operator is an existing
// admin identity for attribution, not a substitute for remote authentication.
// These commands never migrate a database or echo database connection secrets.
func settingsCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: nt settings schema|show|retention-preview|check|apply [--revision N --operator ADMIN --reason TEXT]; patch JSON on stdin")
	}
	command := args[0]
	if command != "schema" && command != "show" && command != "check" && command != "apply" && command != "retention-preview" {
		return errors.New("unknown settings command")
	}
	f := flag.NewFlagSet("settings "+command, flag.ContinueOnError)
	revision := f.Int64("revision", 0, "expected current revision (required for check/apply)")
	operator := f.String("operator", "", "existing administrator username")
	reason := f.String("reason", "", "change reason (5..500 characters)")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected argument")
	}
	if command == "schema" {
		return json.NewEncoder(os.Stdout).Encode(append(system.PropertySchema(), system.ReadonlyPropertySchema()...))
	}
	var patch map[string]json.RawMessage
	if command == "check" || command == "apply" || command == "retention-preview" {
		if *revision < 1 || strings.TrimSpace(*operator) == "" || len(*operator) > 128 || len(strings.TrimSpace(*reason)) < 5 || len(*reason) > 500 {
			return errors.New("revision, operator and reason are required")
		}
		var err error
		patch, err = readSettingsPatch(os.Stdin)
		if err != nil {
			return err
		}
	}
	if err := runtimeprofile.Apply(); err != nil {
		return errors.New("runtime profile unavailable")
	}
	if os.Getenv("NT_ENV") != "development" {
		return errors.New("current backend supports development profile only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dsn := os.Getenv("NT_DATABASE_URL")
	if dsn == "" {
		return errors.New("NT_DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	config.MaxConns = 2
	config.AfterConnect = func(c context.Context, conn *pgx.Conn) error {
		_, e := conn.Exec(c, `SELECT pg_advisory_lock_shared(110031)`)
		return e
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer pool.Close()
	db := postgres.New(pool)
	service := system.New(db)
	state, err := service.State(ctx)
	if err != nil {
		return errors.New("cannot read instance state")
	}
	supportedSchema, err := migrations.CurrentVersion()
	if err != nil {
		return errors.New("cannot determine CLI schema version")
	}
	if state.SchemaVersion != supportedSchema {
		return errors.New("schema mismatch; upgrade the server before using this CLI")
	}
	if command == "show" {
		view, e := service.ConfigurationView(ctx, 0)
		if e != nil {
			return errors.New("cannot read configuration")
		}
		return json.NewEncoder(os.Stdout).Encode(view.Redacted())
	}
	var candidate system.Revision
	var preview []system.RetentionPreview
	var changedKeys []string
	dryRun := errors.New("validated without commit")
	err = db.Within(ctx, func(c context.Context) error {
		var actor string
		if e := db.Row(c, `SELECT id FROM users WHERE username=$1 AND type='admin' AND status='enabled' AND deleted_at IS NULL`, *operator).Scan(&actor); e != nil {
			return errors.New("operator must identify an enabled administrator")
		}
		var e error
		if command == "retention-preview" {
			days, err := retentionPatch(patch)
			if err != nil {
				return err
			}
			preview, e = service.PreviewRetention(c, actor, *revision, days)
			if e != nil {
				return e
			}
			return audit.New(db).Detail(c, actor, "settings.cli_retention_preview", "cli", "local", map[string]string{"operator": *operator, "reason": *reason, "revision": fmt.Sprint(*revision)})
		}
		before, e := service.Configuration(c, *revision)
		if e != nil {
			return e
		}
		for key := range patch {
			if key == "recording-enabled" || system.IsForwardConfigurationKey(key) || strings.HasPrefix(key, "ldap-") || strings.HasPrefix(key, "passkey-") || system.IsOAuthLifetimeKey(key) || system.IsFederationConfigurationKey(key) {
				return errors.New("Identity configuration requires administrator password/MFA proof; use the authenticated settings API")
			}
			if key == "disable-password-login" {
				return errors.New("login policy requires authenticated admin and alternate-login proof; use the login-policy API or explicit password-login recovery command")
			}
			if system.IsConfigurationSecret(key) {
				return errors.New("secret fields require the authenticated settings API; CLI does not accept plaintext or secret references")
			}
		}
		candidate, e = service.SaveConfiguration(c, revision, patch, actor)
		if e != nil {
			return e
		}
		changedKeys = configurationDelta(before.Values, candidate.Values)
		if command == "check" {
			return dryRun
		}
		return audit.New(db).Detail(c, actor, "settings.cli_update", "cli", "local", map[string]string{"operator": *operator, "reason": *reason, "revision": fmt.Sprint(candidate.ID)})
	})
	if command == "check" && errors.Is(err, dryRun) {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"validated": true, "persisted": false, "baseRevision": *revision, "values": candidate.Redacted().Values, "changedKeys": changedKeys})
	}
	if err != nil {
		return fmt.Errorf("configuration not applied: %w", err)
	}
	if command == "retention-preview" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"revision": *revision, "items": preview, "validForSeconds": 600, "deleted": false})
	}
	stateLabel := "requested"
	if len(changedKeys) == 0 {
		stateLabel = "unchanged"
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"revision": candidate.ID, "persisted": len(changedKeys) > 0, "state": stateLabel, "changedKeys": changedKeys})
}

func configurationDelta(before, after system.ConfigurationValues) []string {
	changed := []string{}
	for key, value := range after {
		if !reflect.DeepEqual(before[key], value) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

func retentionPatch(patch map[string]json.RawMessage) (map[string]int, error) {
	known := map[string]bool{}
	for _, c := range system.RetentionCategories() {
		known[c.Key] = true
	}
	days := map[string]int{}
	for key, raw := range patch {
		var n int
		if !known[key] || string(raw) == "null" || json.Unmarshal(raw, &n) != nil || !(n == -1 || n >= 1 && n <= 3650) {
			return nil, errors.New("retention-preview accepts registered retention keys and integer -1 or 1..3650 only")
		}
		days[key] = n
	}
	if len(days) == 0 {
		return nil, errors.New("retention-preview requires retention values")
	}
	return days, nil
}
