package system

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/buildmeta"
	"next-terminal/backend/internal/platform/fault"
)

func ReadonlyPropertySchema() []Property {
	return []Property{
		{"version", "运行版本", "string", "readonly", buildmeta.Release, 1, 64, ""},
		{"security-level", "安全配置模式", "string", "readonly", "custom", 6, 6, ""},
		{"ssh-server-private-key-exists", "已准备 SSH 网关身份", "boolean", "readonly", false, 0, 0, ""},
		{"asset-group-tree", "资产分组（当前目录）", "string", "readonly", "[]", 0, 0, ""},
		{"website-group-tree", "Web 资产分组（当前目录）", "string", "readonly", "[]", 0, 0, ""},
	}
}

// Operational metadata, not editable revision values. Historical revisions also
// show current read-only metadata in a separate object, never as old snapshots.
func (s *Service) ReadonlyConfiguration(ctx context.Context) (map[string]any, error) {
	var exists bool
	e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM native_ssh_gateway_keys WHERE singleton=1) OR EXISTS(SELECT 1 FROM configuration_revisions r JOIN configuration_head h ON h.revision=r.id WHERE coalesce(r.snapshot->>'ssh-server-private-key','')<>'')`).Scan(&exists)
	if e != nil {
		return nil, e
	}
	groups, e := resource.New(s.db).Groups(ctx)
	if e != nil {
		return nil, e
	}
	tree, e := resourceGroupTree(groups)
	if e != nil {
		return nil, e
	}
	// Host/database/web resources share one authoritative group catalog.
	return map[string]any{"version": buildmeta.Release, "security-level": "custom", "ssh-server-private-key-exists": exists, "asset-group-tree": tree, "website-group-tree": tree}, nil
}

type groupProjection struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	ParentID string             `json:"parentId"`
	Children []*groupProjection `json:"children"`
}

func resourceGroupTree(groups []resource.Group) (string, error) {
	nodes := make(map[string]*groupProjection, len(groups))
	for _, g := range groups {
		nodes[g.ID] = &groupProjection{ID: g.ID, Name: g.Name, ParentID: g.ParentID, Children: []*groupProjection{}}
	}
	roots := []*groupProjection{}
	for _, g := range groups {
		n := nodes[g.ID]
		if g.ParentID == "" {
			roots = append(roots, n)
		} else {
			parent := nodes[g.ParentID]
			if parent == nil {
				return "", fault.Invalid
			}
			parent.Children = append(parent.Children, n)
		}
	}
	visited := map[string]bool{}
	var visit func(*groupProjection, int) error
	visit = func(n *groupProjection, depth int) error {
		if visited[n.ID] || depth > 256 {
			return fault.Invalid
		}
		visited[n.ID] = true
		for _, child := range n.Children {
			if e := visit(child, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	for _, n := range roots {
		if e := visit(n, 0); e != nil {
			return "", e
		}
	}
	if len(visited) != len(groups) {
		return "", fault.Invalid
	}
	raw, e := json.Marshal(roots)
	return string(raw), e
}

// Only legacy round-tripping may send unchanged read-only projections. The head
// lock also serializes the automatic no-op field with concurrent legacy writes.
func (s *Service) NormalizeLegacyReadonly(ctx context.Context, patch map[string]json.RawMessage) error {
	present := false
	for _, p := range ReadonlyPropertySchema() {
		if _, ok := patch[p.Key]; ok {
			present = true
		}
	}
	if !present {
		return nil
	}
	if !s.db.InTransaction(ctx) {
		return fault.Invalid
	}
	var id int64
	if e := s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`).Scan(&id); e != nil {
		return e
	}
	current, e := s.ReadonlyConfiguration(ctx)
	if e != nil {
		return e
	}
	for _, p := range ReadonlyPropertySchema() {
		if raw, ok := patch[p.Key]; ok {
			var value any
			if json.Unmarshal(raw, &value) != nil {
				return fault.Invalid
			}
			switch v := value.(type) {
			case string:
				if p.Type != "string" || v != current[p.Key] {
					return fault.Invalid
				}
			case bool:
				if p.Type != "boolean" || v != current[p.Key] {
					return fault.Invalid
				}
			default:
				return fault.Invalid
			}
			delete(patch, p.Key)
		}
	}
	if len(patch) == 0 {
		v, e := s.Configuration(ctx, id)
		if e != nil {
			return e
		}
		patch["system-name"], e = json.Marshal(v.Values.String("system-name"))
		return e
	}
	return nil
}
