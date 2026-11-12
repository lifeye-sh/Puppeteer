package resource

import (
	"context"
	"net"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }

type Asset struct {
	CertFingerprint string   `json:"certFingerprint"`
	ProxyID         string   `json:"proxyId"`
	GatewayGroupID  string   `json:"gatewayGroupId"`
	JumpID          string   `json:"jumpId"`
	AgentID         string   `json:"agentId"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Protocol        string   `json:"protocol"`
	Alias           string   `json:"alias"`
	Host            string   `json:"host"`
	Port            int      `json:"port"`
	GroupID         string   `json:"groupId"`
	CredentialID    string   `json:"credentialId,omitempty"`
	Description     string   `json:"description"`
	Enabled         bool     `json:"enabled"`
	Version         int64    `json:"version"`
	ConnectTimeout  int      `json:"connectTimeout"`
	Tags            []string `json:"tags"`
}
type Group struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
	Version  int64  `json:"version"`
}
type Filter struct {
	Search, GroupID, Tag, ID string
	Offset, Limit            int
	All                      bool
	EnabledOnly              bool
	Targets                  []string
}

var aliasPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var hostnamePattern = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

func ValidID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
func ValidName(s string) bool { return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= 100 }
func Validate(v Asset) error {
	if (v.ProxyID != "" && (!ValidID(v.ProxyID) || v.AgentID != "" || v.JumpID != "" || v.GatewayGroupID != "")) || (v.GatewayGroupID != "" && (!ValidID(v.GatewayGroupID) || v.AgentID != "" || v.JumpID != "")) || (v.JumpID != "" && (!ValidID(v.JumpID) || v.AgentID != "")) || (v.AgentID != "" && !ValidID(v.AgentID)) || !ValidName(v.Name) || (v.Protocol != "ssh" && v.Protocol != "telnet" && v.Protocol != "rdp" && v.Protocol != "vnc") || v.Port < 1 || v.Port > 65535 || v.ConnectTimeout < 1 || v.ConnectTimeout > 300 || len(v.Description) > 2000 || !ValidID(v.CredentialID) || (v.GroupID != "" && !ValidID(v.GroupID)) || (v.Alias != "" && !aliasPattern.MatchString(v.Alias)) || len(v.Host) > 253 || len(v.Tags) > 20 {
		return fault.Invalid
	}
	if len(v.CertFingerprint) > 512 || strings.ContainsAny(v.CertFingerprint, "\r\n\x00") {
		return fault.Invalid
	}
	if v.Protocol == "rdp" && strings.TrimSpace(v.CertFingerprint) == "" {
		return fault.Invalid
	}
	if net.ParseIP(v.Host) == nil && !hostnamePattern.MatchString(v.Host) {
		return fault.Invalid
	}
	seen := map[string]bool{}
	for _, t := range v.Tags {
		if strings.TrimSpace(t) != t || t == "" || utf8.RuneCountInString(t) > 40 || seen[t] {
			return fault.Invalid
		}
		seen[t] = true
	}
	return nil
}

// Query both list/count with the same authorization predicate, before pagination.
const filterSQL = `WITH RECURSIVE allowed_groups AS (
 SELECT id FROM resource_groups WHERE id=ANY($1::uuid[])
 UNION ALL SELECT g.id FROM resource_groups g JOIN allowed_groups p ON g.parent_id=p.id
) SELECT %s FROM assets a JOIN resource_registry r ON r.id=a.id
WHERE r.deleted_at IS NULL AND ($2 OR (r.enabled AND (a.id=ANY($1::uuid[]) OR a.group_id IN(SELECT id FROM allowed_groups))))
AND (r.display_name ILIKE $3 OR a.host ILIKE $3 OR a.alias ILIKE $3)
AND ($4='' OR a.group_id=NULLIF($4,'')::uuid) AND ($5='' OR EXISTS(SELECT 1 FROM resource_tags t WHERE t.resource_id=a.id AND t.tag=$5))
AND ($6='' OR a.id=NULLIF($6,'')::uuid)`
const assetCols = `a.id,r.display_name,a.protocol,a.alias,a.host,a.port,COALESCE(a.group_id::text,''),a.credential_id,r.enabled,r.version,a.description,a.connect_timeout,COALESCE(a.agent_id::text,''),COALESCE(a.jump_id::text,''),COALESCE(a.gateway_group_id::text,''),COALESCE(a.proxy_id::text,''),ARRAY(SELECT tag FROM resource_tags WHERE resource_id=a.id ORDER BY tag),a.cert_fingerprint`

func (s *Service) List(ctx context.Context, f Filter) ([]Asset, int, error) {
	if f.Targets == nil {
		f.Targets = []string{}
	}
	args := []any{f.Targets, f.All, "%" + f.Search + "%", f.GroupID, f.Tag, f.ID}
	var total int
	base := filterSQL
	if f.EnabledOnly {
		base += ` AND r.enabled`
	}
	q := strings.Replace(base, "%s", "count(*)", 1)
	if e := s.db.Row(ctx, q, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	q = strings.Replace(base, "%s", assetCols, 1) + ` ORDER BY r.display_name,a.id LIMIT $7 OFFSET $8`
	args = append(args, f.Limit, f.Offset)
	rows, e := s.db.Rows(ctx, q, args...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		var v Asset
		if e = rows.Scan(&v.ID, &v.Name, &v.Protocol, &v.Alias, &v.Host, &v.Port, &v.GroupID, &v.CredentialID, &v.Enabled, &v.Version, &v.Description, &v.ConnectTimeout, &v.AgentID, &v.JumpID, &v.GatewayGroupID, &v.ProxyID, &v.Tags, &v.CertFingerprint); e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
func (s *Service) Save(ctx context.Context, v Asset) (string, error) {
	if !s.db.InTransaction(ctx) {
		var id string
		e := s.db.Within(ctx, func(c context.Context) error { var e error; id, e = s.Save(c, v); return e })
		return id, e
	}
	if e := Validate(v); e != nil {
		return "", e
	}
	var collision bool
	if e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM assets a JOIN resource_registry r ON r.id=a.id WHERE a.id<>COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000') AND r.deleted_at IS NULL AND (a.alias=$2 OR ($3<>'' AND r.display_name=$3)))`, v.ID, v.Name, v.Alias).Scan(&collision); e != nil {
		return "", e
	}
	if collision {
		return "", fault.Conflict
	}
	if v.ID == "" {
		if e := s.db.Admit(ctx, "resources", "resource.assets"); e != nil {
			return "", e
		}
		if e := s.db.Row(ctx, `INSERT INTO resource_registry(kind,display_name,enabled) VALUES('asset',$1,$2) RETURNING id`, v.Name, v.Enabled).Scan(&v.ID); e != nil {
			return "", e
		}
		if _, e := s.db.Exec(ctx, `INSERT INTO assets(id,protocol,alias,host,port,group_id,credential_id,description,connect_timeout,agent_id,jump_id,gateway_group_id,proxy_id,cert_fingerprint) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,NULLIF($10,'')::uuid,NULLIF($11,'')::uuid,NULLIF($12,'')::uuid,NULLIF($13,'')::uuid,$14)`, v.ID, v.Protocol, v.Alias, v.Host, v.Port, v.GroupID, v.CredentialID, v.Description, v.ConnectTimeout, v.AgentID, v.JumpID, v.GatewayGroupID, v.ProxyID, v.CertFingerprint); e != nil {
			return "", e
		}
	} else {
		tag, e := s.db.Exec(ctx, `UPDATE resource_registry SET display_name=$2,enabled=$3,version=version+1 WHERE id=$1 AND kind='asset' AND version=$4 AND deleted_at IS NULL`, v.ID, v.Name, v.Enabled, v.Version)
		if e != nil {
			return "", e
		}
		if tag.RowsAffected() != 1 {
			return "", fault.Conflict
		}
		if _, e = s.db.Exec(ctx, `UPDATE assets SET alias=$2,host=$3,port=$4,group_id=NULLIF($5,'')::uuid,credential_id=$6,description=$7,connect_timeout=$8,agent_id=NULLIF($9,'')::uuid,jump_id=NULLIF($10,'')::uuid,gateway_group_id=NULLIF($11,'')::uuid,proxy_id=NULLIF($12,'')::uuid,protocol=$13,cert_fingerprint=$14 WHERE id=$1`, v.ID, v.Alias, v.Host, v.Port, v.GroupID, v.CredentialID, v.Description, v.ConnectTimeout, v.AgentID, v.JumpID, v.GatewayGroupID, v.ProxyID, v.Protocol, v.CertFingerprint); e != nil {
			return "", e
		}
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM resource_tags WHERE resource_id=$1`, v.ID); e != nil {
		return "", e
	}
	for _, t := range v.Tags {
		if _, e := s.db.Exec(ctx, `INSERT INTO resource_tags(resource_id,tag) VALUES($1,$2)`, v.ID, t); e != nil {
			return "", e
		}
	}
	return v.ID, nil
}
func (s *Service) Groups(ctx context.Context) ([]Group, error) {
	rows, e := s.db.Rows(ctx, `SELECT g.id,r.display_name,COALESCE(g.parent_id::text,''),r.version FROM resource_groups g JOIN resource_registry r ON r.id=g.id WHERE r.deleted_at IS NULL ORDER BY r.display_name,g.id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if e = rows.Scan(&g.ID, &g.Name, &g.ParentID, &g.Version); e != nil {
			return nil, e
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Service) SaveGroup(ctx context.Context, g Group) (string, error) {
	if !ValidName(g.Name) || (g.ParentID != "" && !ValidID(g.ParentID)) {
		return "", fault.Invalid
	}
	if g.ID == "" {
		if e := s.db.Row(ctx, `INSERT INTO resource_registry(kind,display_name) VALUES('group',$1) RETURNING id`, g.Name).Scan(&g.ID); e != nil {
			return "", e
		}
		_, e := s.db.Exec(ctx, `INSERT INTO resource_groups(id,parent_id) VALUES($1,NULLIF($2,'')::uuid)`, g.ID, g.ParentID)
		return g.ID, e
	}
	var cycle bool
	if e := s.db.Row(ctx, `WITH RECURSIVE children AS(SELECT id FROM resource_groups WHERE id=$1 UNION ALL SELECT g.id FROM resource_groups g JOIN children c ON g.parent_id=c.id) SELECT EXISTS(SELECT 1 FROM children WHERE id=NULLIF($2,'')::uuid)`, g.ID, g.ParentID).Scan(&cycle); e != nil {
		return "", e
	}
	if cycle {
		return "", fault.Invalid
	}
	tag, e := s.db.Exec(ctx, `UPDATE resource_registry SET display_name=$2,version=version+1 WHERE id=$1 AND kind='group' AND version=$3 AND deleted_at IS NULL`, g.ID, g.Name, g.Version)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() != 1 {
		return "", fault.Conflict
	}
	_, e = s.db.Exec(ctx, `UPDATE resource_groups SET parent_id=NULLIF($2,'')::uuid WHERE id=$1`, g.ID, g.ParentID)
	return g.ID, e
}
func (s *Service) Delete(ctx context.Context, id, kind string) error {
	table := "assets"
	if kind == "group" {
		table = "resource_groups"
	}
	tag, e := s.db.Exec(ctx, `UPDATE resource_registry SET deleted_at=now(),enabled=false,version=version+1 WHERE id=$1 AND kind=$2 AND deleted_at IS NULL`, id, kind)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	if _, e = s.db.Exec(ctx, "DELETE FROM "+table+" WHERE id=$1", id); e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `DELETE FROM resource_tags WHERE resource_id=$1`, id)
	return e
}
func (s *Service) Exists(ctx context.Context, id string) (bool, error) {
	var v bool
	e := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM resource_registry WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&v)
	return v, e
}
func (s *Service) CredentialReferences(ctx context.Context, id string) ([]string, error) {
	rows, e := s.db.Rows(ctx, `SELECT r.display_name FROM assets a JOIN resource_registry r ON r.id=a.id WHERE a.credential_id=$1 AND r.deleted_at IS NULL ORDER BY r.display_name`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
