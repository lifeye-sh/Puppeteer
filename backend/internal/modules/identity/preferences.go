package identity

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/platform/fault"
)

type AIPreferences struct {
	DisplayLevel           string `json:"displayLevel"`
	IncludeCommandSnippets bool   `json:"includeCommandSnippets"`
	DefaultProfileID       string `json:"defaultProfileId"`
	Version                int64  `json:"version"`
}

func (s *Service) AIPreferences(ctx context.Context, user string) (v AIPreferences, e error) {
	v.DisplayLevel = "standard"
	var raw []byte
	var version int64
	e = s.db.Row(ctx, `SELECT value,version FROM user_preferences WHERE user_id=$1 AND namespace='ai'`, user).Scan(&raw, &version)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, nil
	}
	if e == nil {
		e = json.Unmarshal(raw, &v)
		v.Version = version
	}
	return
}
func (s *Service) SaveAIPreferences(ctx context.Context, user string, v AIPreferences) error {
	if v.DisplayLevel != "compact" && v.DisplayLevel != "standard" && v.DisplayLevel != "detailed" {
		return fault.Invalid
	}
	if len(v.DefaultProfileID) > 36 || v.Version < 0 {
		return fault.Invalid
	}
	raw, _ := json.Marshal(v)
	var e error
	if v.Version == 0 {
		tag, err := s.db.Exec(ctx, `INSERT INTO user_preferences(user_id,namespace,value) VALUES($1,'ai',$2) ON CONFLICT DO NOTHING`, user, raw)
		e = err
		if e == nil && tag.RowsAffected() != 1 {
			return fault.Conflict
		}
	} else {
		tag, err := s.db.Exec(ctx, `UPDATE user_preferences SET value=$2,version=version+1 WHERE user_id=$1 AND namespace='ai' AND version=$3`, user, raw, v.Version)
		e = err
		if e == nil && tag.RowsAffected() != 1 {
			return fault.Conflict
		}
	}
	return e
}
