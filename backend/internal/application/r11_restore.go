package application

import (
	"context"
	"errors"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/modules/backup"
	databaseModule "next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/files"
	"next-terminal/backend/internal/modules/notification"
	"next-terminal/backend/internal/modules/pki"
	"next-terminal/backend/internal/modules/storage"
	"next-terminal/backend/internal/modules/website"
	"next-terminal/backend/internal/platform/jobs"
	"strings"
)

// QuarantineRestoredState is an offline restore workflow, never an HTTP route.
// The caller must validate an isolated staging database before constructing App.
func (a *App) QuarantineRestoredState(ctx context.Context, restoreID string, scope ...string) error {
	if len(scope) > 1 || (len(scope) == 1 && scope[0] != "full" && scope[0] != "control-plane") {
		return errors.New("invalid restore scope")
	}
	var database, schema string
	if e := a.db.Row(ctx, "SELECT current_database(),current_schema()").Scan(&database, &schema); e != nil {
		return e
	}
	if !strings.HasPrefix(database, "nt_restore_") && !strings.HasPrefix(schema, "r01_test_") {
		return errors.New("restore quarantine requires an isolated staging database")
	}
	return a.db.Within(ctx, func(c context.Context) error {
		tag, e := a.db.Exec(c, `INSERT INTO restore_quarantine(id) VALUES($1) ON CONFLICT DO NOTHING`, restoreID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		for _, step := range []func(context.Context) error{backup.New(a.db).QuarantineRestore, storage.New(a.db).QuarantineRestore, files.New(a.db).QuarantineRestore, a.audit.QuarantineRestore} {
			if e := step(c); e != nil {
				return e
			}
		}
		if len(scope) == 1 && scope[0] == "control-plane" {
			if e := storage.New(a.db).MarkPayloadUnavailable(c); e != nil {
				return e
			}
			if e := a.audit.MarkPayloadUnavailable(c); e != nil {
				return e
			}
		}
		steps := []func(context.Context) error{a.identity.QuarantineRestore, databaseModule.New(a.db).QuarantineRestore, a.sessions.QuarantineRestore, a.system.QuarantineRestore, a.resources.QuarantineRestore, a.connectivity.QuarantineRestore, a.vault.QuarantineRestore, automation.New(a.db).QuarantineRestore, jobs.New(a.db).QuarantineRestore, notification.New(a.db, a.config.VaultKey).QuarantineRestore, pki.New(a.db, a.config.VaultKey).QuarantineRestore, website.New(a.db).QuarantineRestore}
		for _, step := range steps {
			if e := step(c); e != nil {
				return e
			}
		}
		if e := a.aiService().QuarantineRestore(c); e != nil {
			return e
		}
		return a.audit.Detail(c, "", "restore.quarantined", restoreID, "local", map[string]string{"restoreId": restoreID})
	})
}
