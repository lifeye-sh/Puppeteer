package backup

import (
	"next-terminal/backend/migrations"
	"testing"
)

// Keep this explicit: a schema upgrade needs restore/isolation verification,
// rather than automatically permitting a new schema without round-trip tests.
func TestBackupSchemaMatchesEmbeddedMigrations(t *testing.T) {
	latest, err := migrations.CurrentVersion()
	if err != nil {
		t.Fatal(err)
	}
	if SupportedSchema != latest {
		t.Fatalf("backup supports schema %d; embedded schema is %d: verify backup/restore compatibility before release", SupportedSchema, latest)
	}
}
