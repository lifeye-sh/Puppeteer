package authorization

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }

type Permission struct {
	Action string `json:"action"`
	Menu   string `json:"menu"`
	Label  string `json:"label"`
}

func (s *Service) Catalog(ctx context.Context) ([]Permission, error) {
	rows, e := s.db.Rows(ctx, `SELECT action,menu_key,label FROM permissions ORDER BY action`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Permission{}
	for rows.Next() {
		var p Permission
		if e = rows.Scan(&p.Action, &p.Menu, &p.Label); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Service) Actions(ctx context.Context, id, kind string) ([]string, error) {
	if kind == "admin" {
		p, e := s.Catalog(ctx)
		out := []string{"identity.manage"}
		for _, v := range p {
			out = append(out, v.Action)
		}
		return out, e
	}
	rows, e := s.db.Rows(ctx, `SELECT DISTINCT rp.action FROM user_roles ur JOIN role_permissions rp ON rp.role_id=ur.role_id WHERE ur.user_id=$1 ORDER BY rp.action`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var a string
		if e = rows.Scan(&a); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Service) Assign(ctx context.Context, userID string, roles []string) error {
	if _, e := s.db.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, userID); e != nil {
		return e
	}
	for _, id := range roles {
		if _, e := s.db.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, id); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) UserRoles(ctx context.Context, id string) ([]string, error) {
	rows, e := s.db.Rows(ctx, `SELECT role_id::text FROM user_roles WHERE user_id=$1 ORDER BY role_id`, id)
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

type Role struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     int64    `json:"version"`
	Permissions []string `json:"permissions"`
}

func (s *Service) Roles(ctx context.Context) ([]Role, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,name,version,ARRAY(SELECT action FROM role_permissions WHERE role_id=roles.id ORDER BY action) FROM roles ORDER BY name,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		if e = rows.Scan(&r.ID, &r.Name, &r.Version, &r.Permissions); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) Save(ctx context.Context, r Role) (string, error) {
	if r.ID == "" {
		if e := s.db.Row(ctx, `INSERT INTO roles(name) VALUES($1) RETURNING id`, r.Name).Scan(&r.ID); e != nil {
			return "", e
		}
	} else {
		tag, e := s.db.Exec(ctx, `UPDATE roles SET name=$2,version=version+1 WHERE id=$1 AND version=$3`, r.ID, r.Name, r.Version)
		if e != nil {
			return "", e
		}
		if tag.RowsAffected() != 1 {
			return "", fault.Conflict
		}
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1`, r.ID); e != nil {
		return "", e
	}
	for _, p := range r.Permissions {
		if _, e := s.db.Exec(ctx, `INSERT INTO role_permissions(role_id,action) VALUES($1,$2) ON CONFLICT DO NOTHING`, r.ID, p); e != nil {
			return "", e
		}
	}
	return r.ID, nil
}
func (s *Service) Delete(ctx context.Context, id string) error {
	tag, e := s.db.Exec(ctx, `DELETE FROM roles WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM user_roles WHERE role_id=$1)`, id)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}
func (s *Service) RemovePermissions(ctx context.Context, id string) error {
	_, e := s.db.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1`, id)
	return e
}
