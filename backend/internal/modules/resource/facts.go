package resource

import "context"

// Scope returns stable resource identities including current ancestor groups.
// Policy and grant evaluators do not duplicate the resource hierarchy query.
func (s *Service) Scope(ctx context.Context, id string) ([]string, error) {
	rows, e := s.db.Rows(ctx, `WITH RECURSIVE ancestors AS (
 SELECT group_id AS id FROM assets WHERE id=$1
 UNION SELECT group_id AS id FROM websites WHERE id=$1
 UNION SELECT g.parent_id FROM resource_groups g JOIN ancestors a ON a.id=g.id WHERE g.parent_id IS NOT NULL
 ) SELECT $1::text UNION SELECT id::text FROM ancestors WHERE id IS NOT NULL`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
