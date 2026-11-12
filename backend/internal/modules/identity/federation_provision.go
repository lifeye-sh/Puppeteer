package identity

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
)

// Preparing a local account is an explicit administrator action. It never
// asserts an external subject or grants administrative roles. Binding still
// requires the owner's verified issuer/subject callback and password proof.
func (s *Service) federationDepartmentForNewUser(ctx context.Context, in ManagedUser) ([]string, error) {
	if in.DefaultDepartmentSource == "" {
		if in.DefaultDepartmentID != "" {
			return nil, fault.Invalid
		}
		return in.DepartmentIDs, nil
	}
	if in.ID != "" || in.Type != "user" || len(in.RoleIDs) != 0 || len(in.DepartmentIDs) != 0 || in.DefaultDepartmentID == "" || (in.DefaultDepartmentSource != "oidc" && in.DefaultDepartmentSource != "wecom") {
		return nil, fault.Invalid
	}
	var department string
	if e := s.db.Row(ctx, `SELECT department_id::text FROM federation_default_departments WHERE kind=$1 FOR SHARE`, in.DefaultDepartmentSource).Scan(&department); e != nil {
		return nil, fault.Conflict
	}
	if department != in.DefaultDepartmentID {
		return nil, fault.Conflict
	}
	return []string{department}, nil
}
