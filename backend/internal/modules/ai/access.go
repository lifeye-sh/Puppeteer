package ai

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
)

type AssetAccess struct {
	Enabled bool  `json:"enabled"`
	Version int64 `json:"version"`
}

func (s *Service) AssetAccess(ctx context.Context, id string) (v AssetAccess, e error) {
	e = s.db.Row(ctx, `SELECT coalesce(a.enabled,false),coalesce(a.version,0) FROM assets v JOIN resource_registry r ON r.id=v.id LEFT JOIN ai_asset_access a ON a.asset_id=r.id WHERE r.id=$1 AND r.deleted_at IS NULL`, id).Scan(&v.Enabled, &v.Version)
	return
}
func (s *Service) SaveAssetAccess(ctx context.Context, id string, v AssetAccess) error {
	tag, e := s.db.Exec(ctx, `INSERT INTO ai_asset_access(asset_id,enabled) SELECT $1,$2 WHERE $3=0 ON CONFLICT(asset_id) DO UPDATE SET enabled=$2,version=ai_asset_access.version+1 WHERE ai_asset_access.version=$3`, id, v.Enabled, v.Version)
	// UPDATE must also support an existing row when the insert SELECT is empty.
	if e == nil && tag.RowsAffected() == 0 && v.Version > 0 {
		tag, e = s.db.Exec(ctx, `UPDATE ai_asset_access SET enabled=$2,version=version+1 WHERE asset_id=$1 AND version=$3`, id, v.Enabled, v.Version)
	}
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
