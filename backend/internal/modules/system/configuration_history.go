package system

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"reflect"
	"time"
)

type RevisionSummary struct {
	ID          int64     `json:"id"`
	PreviousID  int64     `json:"previousId"`
	ChangedKeys []string  `json:"changedKeys"`
	Actor       string    `json:"actor"`
	Legacy      bool      `json:"legacyWrite"`
	CreatedAt   time.Time `json:"createdAt"`
}
type RevisionPage struct {
	Items      []RevisionSummary `json:"items"`
	CurrentID  int64             `json:"currentId"`
	HasMore    bool              `json:"hasMore"`
	NextBefore int64             `json:"nextBefore"`
}

// Immutable revisions use keyset pagination, so later saves cannot displace
// records on an older page. The listing never returns snapshot values.
func (s *Service) ConfigurationHistory(ctx context.Context, before int64, limit int) (RevisionPage, error) {
	out := RevisionPage{Items: []RevisionSummary{}}
	if before < 0 || limit < 1 || limit > 50 {
		return out, fault.Invalid
	}
	if e := s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1`).Scan(&out.CurrentID); e != nil {
		return out, e
	}
	rows, e := s.db.Rows(ctx, `SELECT r.id,coalesce((SELECT max(p.id) FROM configuration_revisions p WHERE p.id<r.id),0),r.changed_keys,coalesce(u.username,''),r.legacy_write,r.created_at FROM configuration_revisions r LEFT JOIN users u ON u.id=r.actor_id WHERE r.id<=$1 AND ($2::bigint=0 OR r.id<$2) ORDER BY r.id DESC LIMIT $3`, out.CurrentID, before, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var v RevisionSummary
		if e = rows.Scan(&v.ID, &v.PreviousID, &v.ChangedKeys, &v.Actor, &v.Legacy, &v.CreatedAt); e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Items) > limit {
		out.HasMore = true
		out.Items = out.Items[:limit]
		out.NextBefore = out.Items[len(out.Items)-1].ID
	}
	return out, nil
}

type ConfigurationChange struct {
	Key           string `json:"key"`
	Label         string `json:"label"`
	Type          string `json:"type"`
	Unit          string `json:"unit"`
	Before        any    `json:"before"`
	After         any    `json:"after"`
	BeforePresent bool   `json:"beforePresent"`
	AfterPresent  bool   `json:"afterPresent"`
}
type ConfigurationDiff struct {
	FromID  int64                 `json:"fromId"`
	ToID    int64                 `json:"toId"`
	Changes []ConfigurationChange `json:"changes"`
}

// Compare only the registered non-secret configuration scope. Unknown historic
// values are excluded. Future secret adapters must provide a redacted projection.
func (s *Service) CompareConfiguration(ctx context.Context, to int64, from *int64) (ConfigurationDiff, error) {
	out := ConfigurationDiff{ToID: to, Changes: []ConfigurationChange{}}
	if to < 1 || (from != nil && *from < 0) {
		return out, fault.Invalid
	}
	target, e := s.Configuration(ctx, to)
	if e != nil {
		return out, e
	}
	if from == nil {
		if e = s.db.Row(ctx, `SELECT coalesce(max(id),0) FROM configuration_revisions WHERE id<$1`, to).Scan(&out.FromID); e != nil {
			return out, e
		}
	} else {
		out.FromID = *from
	}
	previous := Revision{Values: ConfigurationValues{}}
	if out.FromID != 0 {
		previous, e = s.Configuration(ctx, out.FromID)
		if e != nil {
			return out, e
		}
	}
	for _, p := range PropertySchema() {
		old, oldOK := previous.Values[p.Key]
		next, nextOK := target.Values[p.Key]
		if oldOK == nextOK && reflect.DeepEqual(old, next) {
			continue
		}
		if p.Type == "secret" {
			old = MaskConfigurationSecret(old)
			next = MaskConfigurationSecret(next)
		}
		out.Changes = append(out.Changes, ConfigurationChange{Key: p.Key, Label: p.Label, Type: p.Type, Unit: p.Unit, Before: old, After: next, BeforePresent: oldOK, AfterPresent: nextOK})
	}
	return out, nil
}
