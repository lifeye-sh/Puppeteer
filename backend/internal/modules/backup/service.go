package backup

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"filippo.io/age"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }

type Config struct {
	Recipient       string `json:"recipient"`
	RetentionDays   int    `json:"retentionDays"`
	ScheduleEnabled bool   `json:"scheduleEnabled"`
	ScheduleTime    string `json:"scheduleTime"`
	Timezone        string `json:"timezone"`
	Scope           string `json:"scope"`
	Revision        int64  `json:"revision"`
}

func (s *Service) Config(ctx context.Context) (v Config, e error) {
	e = s.db.Row(ctx, `SELECT recipient,retention_days,schedule_enabled,schedule_time,timezone,scope,revision FROM backup_config WHERE singleton=true`).Scan(&v.Recipient, &v.RetentionDays, &v.ScheduleEnabled, &v.ScheduleTime, &v.Timezone, &v.Scope, &v.Revision)
	return
}
func ValidateConfig(v Config) error {
	if _, e := age.ParseX25519Recipient(v.Recipient); e != nil {
		return fault.Invalid
	}
	if v.RetentionDays < 1 || v.RetentionDays > 3650 || v.Revision < 1 || !regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`).MatchString(v.ScheduleTime) || (v.Scope != "control-plane" && v.Scope != "full") {
		return fault.Invalid
	}
	if _, e := time.LoadLocation(v.Timezone); e != nil {
		return fault.Invalid
	}
	return nil
}
func (s *Service) SaveConfig(ctx context.Context, v Config) error {
	if e := ValidateConfig(v); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `UPDATE backup_config SET recipient=$1,retention_days=$2,schedule_enabled=$3,schedule_time=$4,timezone=$5,scope=$6,revision=revision+1 WHERE singleton=true AND revision=$7`, v.Recipient, v.RetentionDays, v.ScheduleEnabled, v.ScheduleTime, v.Timezone, v.Scope, v.Revision)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}

type File struct {
	ID         string    `json:"id"`
	StorageKey string    `json:"-"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	Manifest   Manifest  `json:"manifest"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (s *Service) Files(ctx context.Context) ([]File, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,storage_key,size,sha256,manifest,created_at FROM backup_files WHERE state='available' ORDER BY created_at DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var f File
		var raw []byte
		if e = rows.Scan(&f.ID, &f.StorageKey, &f.Size, &f.SHA256, &raw, &f.CreatedAt); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &f.Manifest); e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
