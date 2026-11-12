package database

import (
	"context"
	"crypto/x509"
	"net"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"regexp"
	"strings"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }

type Asset struct {
	NativeWriteAvailable    bool   `json:"nativeWriteAvailable"`
	NativeWriteCredentialID string `json:"nativeWriteCredentialId"`
	Engine                  string `json:"engine"`
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	Alias                   string `json:"alias"`
	Host                    string `json:"host"`
	Port                    int    `json:"port"`
	Database                string `json:"database"`
	ReadonlyCredentialID    string `json:"readonlyCredentialId"`
	WriteCredentialID       string `json:"writeCredentialId"`
	TLSServerName           string `json:"tlsServerName"`
	TLSCA                   string `json:"tlsCa"`
	Description             string `json:"description"`
	ConnectTimeout          int    `json:"connectTimeout"`
	Enabled                 bool   `json:"enabled"`
	Version                 int64  `json:"version"`
}

var aliasPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var dnsPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

func Validate(v Asset) error {
	if v.Engine != "" && v.Engine != "mysql" && v.Engine != "postgresql" {
		return fault.Invalid
	}
	if v.Engine == "postgresql" && (v.WriteCredentialID != "" || v.NativeWriteCredentialID != "") {
		return fault.Invalid
	}
	if v.NativeWriteCredentialID != "" && (!resource.ValidID(v.NativeWriteCredentialID) || v.NativeWriteCredentialID == v.ReadonlyCredentialID || v.NativeWriteCredentialID == v.WriteCredentialID) {
		return fault.Invalid
	}
	if !resource.ValidName(v.Name) || !aliasPattern.MatchString(v.Alias) || (net.ParseIP(v.Host) == nil && !dnsPattern.MatchString(v.Host)) || v.Port < 1 || v.Port > 65535 || v.Database == "" || len(v.Database) > 64 || strings.ContainsAny(v.Database, "\x00\r\n") || !resource.ValidID(v.ReadonlyCredentialID) || (v.WriteCredentialID != "" && !resource.ValidID(v.WriteCredentialID)) || v.WriteCredentialID == v.ReadonlyCredentialID || v.ConnectTimeout < 1 || v.ConnectTimeout > 60 || len(v.Description) > 2000 {
		return fault.Invalid
	}
	if (net.ParseIP(v.TLSServerName) == nil && !dnsPattern.MatchString(v.TLSServerName)) || len(v.TLSCA) > 32768 {
		return fault.Invalid
	}
	if !x509.NewCertPool().AppendCertsFromPEM([]byte(v.TLSCA)) {
		return fault.Invalid
	}
	return nil
}

const assetFields = `r.id::text,r.display_name,d.alias,d.host,d.port,d.database_name,d.readonly_credential_id::text,coalesce(d.write_credential_id::text,''),d.tls_server_name,d.tls_ca,d.description,d.connect_timeout,r.enabled,r.version,d.engine,coalesce(d.native_write_credential_id::text,'')`

func (s *Service) List(ctx context.Context, all bool, targets []string) ([]Asset, error) {
	if targets == nil {
		targets = []string{}
	}
	rows, e := s.db.Rows(ctx, `SELECT `+assetFields+` FROM database_assets d JOIN resource_registry r ON r.id=d.id WHERE r.deleted_at IS NULL AND ($1 OR (r.enabled AND r.id=ANY($2::uuid[]))) ORDER BY r.display_name,r.id`, all, targets)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		var v Asset
		if e = rows.Scan(&v.ID, &v.Name, &v.Alias, &v.Host, &v.Port, &v.Database, &v.ReadonlyCredentialID, &v.WriteCredentialID, &v.TLSServerName, &v.TLSCA, &v.Description, &v.ConnectTimeout, &v.Enabled, &v.Version, &v.Engine, &v.NativeWriteCredentialID); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Save(ctx context.Context, v Asset) (string, error) {
	if !s.db.InTransaction(ctx) {
		var id string
		e := s.db.Within(ctx, func(c context.Context) error { var e error; id, e = s.Save(c, v); return e })
		return id, e
	}
	// Older clients omit engine; only new assets default to MySQL. An update
	// must never accidentally turn an existing PostgreSQL asset into MySQL.
	if v.Engine == "" {
		v.Engine = "mysql"
		if v.ID != "" {
			if e := s.db.Row(ctx, `SELECT engine FROM database_assets WHERE id=$1`, v.ID).Scan(&v.Engine); e != nil {
				return "", e
			}
		}
	}
	if e := Validate(v); e != nil {
		return "", e
	}
	if v.ID == "" {
		if e := s.db.Admit(ctx, "resources", "resource.assets"); e != nil {
			return "", e
		}
		if e := s.db.Row(ctx, `INSERT INTO resource_registry(kind,display_name,enabled) VALUES('database',$1,$2) RETURNING id`, v.Name, v.Enabled).Scan(&v.ID); e != nil {
			return "", e
		}
		_, e := s.db.Exec(ctx, `INSERT INTO database_assets(id,alias,host,port,database_name,readonly_credential_id,write_credential_id,tls_server_name,tls_ca,description,connect_timeout,engine,native_write_credential_id) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10,$11,$12,NULLIF($13,'')::uuid)`, v.ID, v.Alias, v.Host, v.Port, v.Database, v.ReadonlyCredentialID, v.WriteCredentialID, v.TLSServerName, v.TLSCA, v.Description, v.ConnectTimeout, v.Engine, v.NativeWriteCredentialID)
		return v.ID, e
	}
	var engine string
	if e := s.db.Row(ctx, `SELECT engine FROM database_assets WHERE id=$1 FOR UPDATE`, v.ID).Scan(&engine); e != nil {
		return "", e
	}
	if engine != v.Engine {
		return "", fault.Conflict
	}
	tag, e := s.db.Exec(ctx, `UPDATE resource_registry SET display_name=$2,enabled=$3,version=version+1 WHERE id=$1 AND kind='database' AND deleted_at IS NULL AND version=$4`, v.ID, v.Name, v.Enabled, v.Version)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() != 1 {
		return "", fault.Conflict
	}
	_, e = s.db.Exec(ctx, `UPDATE database_assets SET alias=$2,host=$3,port=$4,database_name=$5,readonly_credential_id=$6,write_credential_id=NULLIF($7,'')::uuid,tls_server_name=$8,tls_ca=$9,description=$10,connect_timeout=$11,native_write_credential_id=NULLIF($12,'')::uuid WHERE id=$1`, v.ID, v.Alias, v.Host, v.Port, v.Database, v.ReadonlyCredentialID, v.WriteCredentialID, v.TLSServerName, v.TLSCA, v.Description, v.ConnectTimeout, v.NativeWriteCredentialID)
	return v.ID, e
}
func (s *Service) Delete(ctx context.Context, id string, version int64) error {
	tag, e := s.db.Exec(ctx, `UPDATE resource_registry SET enabled=false,deleted_at=now(),version=version+1 WHERE id=$1 AND kind='database' AND version=$2 AND deleted_at IS NULL`, id, version)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}
