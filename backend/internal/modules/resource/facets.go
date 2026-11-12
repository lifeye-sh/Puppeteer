package resource

import (
	"context"
	"strings"
)

// Facets share the authorization predicate, without leaking unrelated group names or tags.
func (s *Service) Facets(ctx context.Context, f Filter) (map[string]any, error) {
	if f.Targets == nil {
		f.Targets = []string{}
	}
	args := []any{f.Targets, f.All, "%", "", "", ""}
	base := filterSQL + ` AND r.enabled`
	q := strings.Replace(base, "%s", `DISTINCT a.group_id::text,(SELECT display_name FROM resource_registry WHERE id=a.group_id)`, 1) + ` AND a.group_id IS NOT NULL ORDER BY 2`
	rows, e := s.db.Rows(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	groups := []Group{}
	for rows.Next() {
		var g Group
		if e = rows.Scan(&g.ID, &g.Name); e != nil {
			rows.Close()
			return nil, e
		}
		groups = append(groups, g)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	q = `SELECT DISTINCT tag FROM resource_tags WHERE resource_id IN (` + strings.Replace(base, "%s", "a.id", 1) + `) ORDER BY tag`
	rows, e = s.db.Rows(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	tags := []string{}
	for rows.Next() {
		var t string
		if e = rows.Scan(&t); e != nil {
			return nil, e
		}
		tags = append(tags, t)
	}
	return map[string]any{"groups": groups, "tags": tags}, rows.Err()
}
