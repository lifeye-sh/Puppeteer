package connectivity

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
)

type GatewayMember struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type GatewayGroup struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Enabled bool            `json:"enabled"`
	Version int64           `json:"version"`
	Members []GatewayMember `json:"members"`
}

func (s *Service) Groups(ctx context.Context) ([]GatewayGroup, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,name,enabled,version,members FROM gateway_groups ORDER BY name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []GatewayGroup{}
	for rows.Next() {
		var v GatewayGroup
		var raw []byte
		if e = rows.Scan(&v.ID, &v.Name, &v.Enabled, &v.Version, &raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &v.Members); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Group(ctx context.Context, id string) (v GatewayGroup, e error) {
	if !resource.ValidID(id) {
		return v, fault.Invalid
	}
	var raw []byte
	e = s.db.Row(ctx, `SELECT id,name,enabled,version,members FROM gateway_groups WHERE id=$1`, id).Scan(&v.ID, &v.Name, &v.Enabled, &v.Version, &raw)
	if e == nil {
		e = json.Unmarshal(raw, &v.Members)
	}
	return
}
func (s *Service) SaveGroup(ctx context.Context, v GatewayGroup) (string, error) {
	if !resource.ValidName(v.Name) || len(v.Members) == 0 || len(v.Members) > 32 {
		return "", fault.Invalid
	}
	seen := map[string]bool{}
	for _, m := range v.Members {
		if !resource.ValidID(m.ID) || seen[m.ID] {
			return "", fault.Invalid
		}
		seen[m.ID] = true
		switch m.Kind {
		case "agent":
			if e := s.ValidateDevice(ctx, m.ID); e != nil {
				return "", e
			}
		case "jump":
			if _, e := s.Jump(ctx, m.ID); e != nil {
				return "", e
			}
		default:
			return "", fault.Invalid
		}
	}
	raw, e := json.Marshal(v.Members)
	if e != nil {
		return "", e
	}
	if v.ID == "" {
		e = s.db.Row(ctx, `INSERT INTO gateway_groups(name,enabled,members) VALUES($1,$2,$3) RETURNING id`, v.Name, v.Enabled, raw).Scan(&v.ID)
		return v.ID, e
	}
	if !resource.ValidID(v.ID) {
		return "", fault.Invalid
	}
	tag, e := s.db.Exec(ctx, `UPDATE gateway_groups SET name=$2,enabled=$3,members=$4,version=version+1 WHERE id=$1 AND version=$5`, v.ID, v.Name, v.Enabled, raw, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return v.ID, e
}
