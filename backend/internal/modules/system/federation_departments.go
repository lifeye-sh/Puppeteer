package system

import (
	"context"
	"github.com/google/uuid"
	"next-terminal/backend/internal/platform/fault"
)

func isFederationDepartmentKey(key string) bool {
	return key == "oidc-department" || key == "wechat-work-department"
}
func (s *Service) applyFederationDepartment(ctx context.Context, key, department string) error {
	kind := "oidc"
	if key == "wechat-work-department" {
		kind = "wecom"
	}
	if department == "" {
		_, e := s.db.Exec(ctx, `DELETE FROM federation_default_departments WHERE kind=$1`, kind)
		return e
	}
	id, e := uuid.Parse(department)
	if e != nil || id.String() != department {
		return fault.Invalid
	}
	var existing string
	if e = s.db.Row(ctx, `SELECT id::text FROM departments WHERE id=$1 FOR KEY SHARE`, department).Scan(&existing); e != nil {
		return fault.Invalid
	}
	_, e = s.db.Exec(ctx, `INSERT INTO federation_default_departments(kind,department_id)VALUES($1,$2) ON CONFLICT(kind) DO UPDATE SET department_id=EXCLUDED.department_id`, kind, department)
	return e
}
