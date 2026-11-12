package resource

import (
	"context"
	"strings"
)

type ProtocolCount struct {
	Protocol string `json:"protocol"`
	Count    int64  `json:"count"`
}

// Use exactly the directory's visibility rules, including inherited grants.
func (s *Service) ProtocolCounts(ctx context.Context, f Filter) ([]ProtocolCount, error) {
	if f.Targets == nil {
		f.Targets = []string{}
	}
	q := filterSQL
	if f.EnabledOnly {
		q += ` AND r.enabled`
	}
	q = strings.Replace(q, "%s", "a.protocol,count(*)", 1) + ` GROUP BY a.protocol ORDER BY a.protocol`
	rows, err := s.db.Rows(ctx, q, f.Targets, f.All, "%"+f.Search+"%", f.GroupID, f.Tag, f.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProtocolCount{}
	for rows.Next() {
		var v ProtocolCount
		if err = rows.Scan(&v.Protocol, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
