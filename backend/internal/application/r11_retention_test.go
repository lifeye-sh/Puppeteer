package application

import (
	"context"
	"next-terminal/backend/internal/modules/backup"
	"os"
	"path/filepath"
	"testing"
)

func TestR11BackupRetentionProtectsDownloadsAndLatest(t *testing.T) {
	p, a, _ := r02Server(t, false)
	ctx := context.Background()
	dir := t.TempDir()
	s := backup.New(a.db)
	makeFile := func(old bool) string {
		t.Helper()
		var id string
		if e := p.QueryRow(ctx, `INSERT INTO jobs(kind,state,ended_at) VALUES('backup.create','succeeded',now()) RETURNING id::text`).Scan(&id); e != nil {
			t.Fatal(e)
		}
		if _, e := p.Exec(ctx, `INSERT INTO backup_tasks(id,request_key,config_snapshot) VALUES($1,$1,'{}')`, id); e != nil {
			t.Fatal(e)
		}
		if _, e := p.Exec(ctx, `INSERT INTO backup_files(id,storage_key,size,sha256,manifest,created_at) VALUES($1,$2,7,'fixture','{}',CASE WHEN $3 THEN now()-interval '40 days' ELSE now() END)`, id, id+".ntbackup.age", old); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(dir, id+".ntbackup.age"), []byte("fixture"), 0600); e != nil {
			t.Fatal(e)
		}
		return id
	}
	expired, locked, ticketed, newest := makeFile(true), makeFile(true), makeFile(true), makeFile(false)
	if _, e := p.Exec(ctx, `INSERT INTO backup_export_tickets(token_hash,session_hash,file_id) VALUES('fixture','fixture',$1)`, ticketed); e != nil {
		t.Fatal(e)
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM backup_files WHERE id=$1 FOR SHARE`, locked); e != nil {
		t.Fatal(e)
	}
	if e = s.Prune(ctx, dir); e != nil {
		t.Fatal(e)
	}
	exists := func(id string, want bool) {
		t.Helper()
		_, e := os.Stat(filepath.Join(dir, id+".ntbackup.age"))
		if (e == nil) != want {
			t.Fatalf("file retention mismatch: exists=%v want=%v", e == nil, want)
		}
	}
	exists(expired, false)
	exists(locked, true)
	exists(ticketed, true)
	exists(newest, true)
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE backup_export_tickets SET expires_at=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	if e = s.Prune(ctx, dir); e != nil {
		t.Fatal(e)
	}
	exists(locked, false)
	exists(ticketed, false)
	exists(newest, true)
}
