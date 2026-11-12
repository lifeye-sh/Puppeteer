package connectivity

import (
	"context"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
)

type Proxy struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Scheme       string `json:"scheme"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	CredentialID string `json:"credentialId"`
	Enabled      bool   `json:"enabled"`
	Version      int64  `json:"version"`
}

const proxyColumns = `id,name,scheme,host,port,COALESCE(credential_id::text,''),enabled,version`

func (s *Service) Proxies(ctx context.Context) ([]Proxy, error) {
	rows, e := s.db.Rows(ctx, `SELECT `+proxyColumns+` FROM network_proxies ORDER BY name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Proxy{}
	for rows.Next() {
		var v Proxy
		if e = rows.Scan(&v.ID, &v.Name, &v.Scheme, &v.Host, &v.Port, &v.CredentialID, &v.Enabled, &v.Version); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Proxy(ctx context.Context, id string) (v Proxy, e error) {
	if !resource.ValidID(id) {
		return v, fault.Invalid
	}
	e = s.db.Row(ctx, `SELECT `+proxyColumns+` FROM network_proxies WHERE id=$1`, id).Scan(&v.ID, &v.Name, &v.Scheme, &v.Host, &v.Port, &v.CredentialID, &v.Enabled, &v.Version)
	return
}
func (s *Service) SaveProxy(ctx context.Context, v Proxy) (string, error) {
	cred := v.CredentialID
	if cred == "" {
		cred = "00000000-0000-0000-0000-000000000000"
	}
	if e := resource.Validate(resource.Asset{Name: v.Name, Host: v.Host, Port: v.Port, Protocol: "ssh", CredentialID: cred, ConnectTimeout: 10}); e != nil {
		return "", e
	}
	switch v.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", fault.Invalid
	}
	if v.ID == "" {
		e := s.db.Row(ctx, `INSERT INTO network_proxies(name,scheme,host,port,credential_id,enabled) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6) RETURNING id`, v.Name, v.Scheme, v.Host, v.Port, v.CredentialID, v.Enabled).Scan(&v.ID)
		return v.ID, e
	}
	if !resource.ValidID(v.ID) {
		return "", fault.Invalid
	}
	tag, e := s.db.Exec(ctx, `UPDATE network_proxies SET name=$2,scheme=$3,host=$4,port=$5,credential_id=NULLIF($6,'')::uuid,enabled=$7,version=version+1 WHERE id=$1 AND version=$8`, v.ID, v.Name, v.Scheme, v.Host, v.Port, v.CredentialID, v.Enabled, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return v.ID, e
}
