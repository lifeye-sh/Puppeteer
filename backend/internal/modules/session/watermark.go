package session

import (
	"context"
	"next-terminal/backend/internal/modules/system"
)

type Watermark = system.Watermark

func (s *Service) captureWatermark(ctx context.Context, id, actor, asset string) error {
	var user, assetUser, name string
	if e := s.db.Row(ctx, `SELECT u.username,c.username,r.display_name FROM users u CROSS JOIN assets a JOIN credentials c ON c.id=a.credential_id JOIN resource_registry r ON r.id=a.id WHERE u.id=$1 AND a.id=$2`, actor, asset).Scan(&user, &assetUser, &name); e != nil {
		return e
	}
	w, e := system.New(s.db).SessionWatermark(ctx, user, assetUser, name)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `UPDATE sessions SET watermark=$2 WHERE id=$1`, id, w)
	return e
}
