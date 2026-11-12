package authorization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"next-terminal/backend/internal/platform/fault"
)

type GeoEntry struct {
	Prefix   string `json:"prefix"`
	Country  string `json:"country"`
	Province string `json:"province"`
	City     string `json:"city"`
}
type GeoVersion struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Digest  string `json:"digest"`
	Active  bool   `json:"active"`
	Entries int64  `json:"entries"`
}

func (s *Service) Geo(ctx context.Context, ip string) (v GeoEntry, e error) {
	if _, e = netip.ParseAddr(ip); e != nil {
		return v, fault.Invalid
	}
	e = s.db.Row(ctx, `SELECT prefix::text,country,province,city FROM geodata_entries g JOIN geodata_versions v ON v.id=g.version_id WHERE v.active AND $1::inet<<=g.prefix ORDER BY masklen(g.prefix) DESC LIMIT 1`, ip).Scan(&v.Prefix, &v.Country, &v.Province, &v.City)
	return
}
func (s *Service) GeoVersions(ctx context.Context) ([]GeoVersion, error) {
	rows, e := s.db.Rows(ctx, "SELECT v.id::text,v.name,v.digest,v.active,count(g.prefix) FROM geodata_versions v LEFT JOIN geodata_entries g ON g.version_id=v.id GROUP BY v.id ORDER BY v.created_at DESC")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []GeoVersion{}
	for rows.Next() {
		var v GeoVersion
		if e = rows.Scan(&v.ID, &v.Name, &v.Digest, &v.Active, &v.Entries); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ImportGeo is atomic with the caller's audit transaction. A bad dataset never
// replaces the current version. Input is data, not executable provider code.
func (s *Service) ImportGeo(ctx context.Context, name string, entries []GeoEntry) (string, error) {
	if name == "" || len(name) > 100 || len(entries) == 0 || len(entries) > 100000 {
		return "", fault.Invalid
	}
	seen := map[string]bool{}
	for i, v := range entries {
		p, e := netip.ParsePrefix(v.Prefix)
		if e != nil || v.Country == "" || len(v.Country)+len(v.Province)+len(v.City) > 500 {
			return "", fault.Invalid
		}
		entries[i].Prefix = p.Masked().String()
		if seen[entries[i].Prefix] {
			return "", fault.Invalid
		}
		seen[entries[i].Prefix] = true
	}
	b, e := json.Marshal(entries)
	if e != nil {
		return "", e
	}
	hash := sha256.Sum256(b)
	var id string
	if _, e = s.db.Exec(ctx, "SELECT pg_advisory_xact_lock(908003)"); e != nil {
		return "", e
	}
	if e = s.db.Row(ctx, "INSERT INTO geodata_versions(name,digest) VALUES($1,$2) RETURNING id::text", name, hex.EncodeToString(hash[:])).Scan(&id); e != nil {
		return "", e
	}
	// Insert in bounded batches instead of issuing one network round trip per prefix.
	for start := 0; start < len(entries); start += 1000 {
		chunk, e := json.Marshal(entries[start:min(start+1000, len(entries))])
		if e != nil {
			return "", e
		}
		if _, e = s.db.Exec(ctx, `INSERT INTO geodata_entries SELECT $1,prefix::cidr,country,province,city FROM jsonb_to_recordset($2::jsonb) AS x(prefix text,country text,province text,city text)`, id, chunk); e != nil {
			return "", e
		}
	}
	if _, e = s.db.Exec(ctx, "UPDATE geodata_versions SET active=false WHERE active"); e != nil {
		return "", e
	}
	if _, e = s.db.Exec(ctx, "UPDATE geodata_versions SET active=true WHERE id=$1", id); e != nil {
		return "", e
	}
	_, e = s.db.Exec(ctx, "UPDATE policy_revision SET revision=revision+1")
	return id, e
}
