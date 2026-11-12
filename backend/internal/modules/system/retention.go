package system

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/platform/fault"
)

type AccessRetention struct {
	Days     int   `json:"days"`
	Revision int64 `json:"revision"`
}

func (s *Service) AccessRetention(ctx context.Context) (v AccessRetention, e error) {
	var revision Revision
	revision, e = s.Configuration(ctx, 0)
	if e == nil {
		v.Days = revision.Values.Int("access-log-saved-limit-days")
		v.Revision = revision.ID
		if !validDays(v.Days) {
			e = fault.Invalid
		}
	}
	return
}
func (s *Service) SetAccessRetention(ctx context.Context, v AccessRetention, actor string) error {
	if !validDays(v.Days) || v.Revision < 1 {
		return fault.Invalid
	}
	raw, _ := json.Marshal(v.Days)
	_, e := s.SaveConfiguration(ctx, &v.Revision, map[string]json.RawMessage{"access-log-saved-limit-days": raw}, actor)
	return e
}
