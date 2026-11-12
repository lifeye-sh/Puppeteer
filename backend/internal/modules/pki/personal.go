package pki

import "context"

// Personal lists public certificate metadata only, scoped in SQL to its owner.
func (s *Service) Personal(ctx context.Context, user string) ([]Certificate, error) {
	rows, err := s.db.Rows(ctx, "SELECT "+columns+" FROM certificates WHERE kind='client' AND user_id=$1 ORDER BY created_at DESC", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Certificate{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// OwnedClient must be called in a transaction for export/revocation. The row
// lock serializes this operation with administrative certificate revocation.
func (s *Service) OwnedClient(ctx context.Context, user, id string) (Certificate, error) {
	return scan(s.db.Row(ctx, "SELECT "+columns+" FROM certificates WHERE id=$1 AND kind='client' AND user_id=$2 FOR UPDATE", id, user))
}
