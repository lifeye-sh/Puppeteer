package authorization

import (
	"context"
	"encoding/json"
	"net/netip"
	"next-terminal/backend/internal/platform/fault"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
)

var policyTables = map[string]string{"access-policy": "policy_groups", "strategy": "strategies", "command-filter": "command_filters", "ip-set": "ip_sets"}
var ActionNames = []string{"connect", "upload", "download", "createFile", "createDir", "edit", "delete", "rename", "chmod", "copy", "paste", "execute", "observe", "share"}

type Period struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
}
type Rule struct {
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	Priority  int        `json:"priority"`
	Action    string     `json:"action"`
	IPs       []string   `json:"ips"`
	IPSetIDs  []string   `json:"ipSetIds"`
	Countries []string   `json:"countries"`
	Provinces []string   `json:"provinces"`
	Cities    []string   `json:"cities"`
	Periods   []Period   `json:"periods"`
	ExpiresAt *time.Time `json:"expiresAt"`
}
type CommandRule struct {
	Pattern string `json:"pattern"`
	Action  string `json:"action"`
}
type PolicyDocument struct {
	ID            string          `json:"id"`
	Version       int64           `json:"version"`
	Name          string          `json:"name"`
	Enabled       bool            `json:"enabled"`
	Global        bool            `json:"global"`
	UserIDs       []string        `json:"userIds"`
	DepartmentIDs []string        `json:"departmentIds"`
	ResourceIDs   []string        `json:"resourceIds"`
	Mode          string          `json:"mode"`
	Timezone      string          `json:"timezone"`
	Rules         []Rule          `json:"rules"`
	Actions       map[string]bool `json:"actions"`
	Patterns      []CommandRule   `json:"patterns"`
	Entries       []string        `json:"entries"`
}
type Facts struct {
	UserID                                string
	DepartmentIDs, ResourceIDs, ForcedIDs []string
	IP, Action, Command                   string
	Now                                   time.Time
}
type Decision struct {
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason"`
	PolicyID string `json:"policyId"`
	Revision int64  `json:"revision"`
	Filtered bool   `json:"filtered"`
}

func validUUID(v string) bool {
	ok, _ := regexp.MatchString(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`, v)
	return ok
}
func addressRange(v string) (netip.Addr, netip.Addr, bool) {
	if p, e := netip.ParsePrefix(v); e == nil {
		p = p.Masked()
		start := p.Addr().Unmap()
		b := p.Addr().As16()
		bits := p.Bits()
		if p.Addr().Is4() {
			bits += 96
		}
		for bit := bits; bit < 128; bit++ {
			b[bit/8] |= 1 << uint(7-bit%8)
		}
		return start, netip.AddrFrom16(b).Unmap(), true
	}
	if p, e := netip.ParseAddr(v); e == nil {
		return p.Unmap(), p.Unmap(), true
	}
	parts := strings.Split(v, "-")
	if len(parts) != 2 {
		return netip.Addr{}, netip.Addr{}, false
	}
	a, e := netip.ParseAddr(strings.TrimSpace(parts[0]))
	b, f := netip.ParseAddr(strings.TrimSpace(parts[1]))
	a = a.Unmap()
	b = b.Unmap()
	return a, b, e == nil && f == nil && a.BitLen() == b.BitLen() && a.Compare(b) <= 0
}
func IPMatches(ip string, entries []string) bool {
	a, e := netip.ParseAddr(ip)
	if e != nil {
		return false
	}
	a = a.Unmap()
	for _, v := range entries {
		lo, hi, ok := addressRange(v)
		if ok && a.BitLen() == lo.BitLen() && a.Compare(lo) >= 0 && a.Compare(hi) <= 0 {
			return true
		}
	}
	return false
}
func minute(v string) (int, bool) {
	t, e := time.Parse("15:04", v)
	return t.Hour()*60 + t.Minute(), e == nil
}
func ValidatePolicy(kind string, p PolicyDocument) error {
	if policyTables[kind] == "" || strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 100 || len(p.UserIDs)+len(p.DepartmentIDs)+len(p.ResourceIDs) > 1000 || len(p.Rules) > 100 || len(p.Patterns) > 100 || len(p.Entries) > 10000 {
		return fault.Invalid
	}
	for _, ids := range [][]string{p.UserIDs, p.DepartmentIDs, p.ResourceIDs} {
		for _, id := range ids {
			if !validUUID(id) {
				return fault.Invalid
			}
		}
	}
	if kind == "access-policy" || kind == "command-filter" {
		if p.Mode != "whitelist" && p.Mode != "blacklist" {
			return fault.Invalid
		}
	}
	if kind == "access-policy" {
		if _, e := time.LoadLocation(p.Timezone); e != nil {
			return fault.Invalid
		}
	}
	for k := range p.Actions {
		if !slices.Contains(ActionNames, k) {
			return fault.Invalid
		}
	}
	if kind == "ip-set" && len(p.Entries) == 0 {
		return fault.Invalid
	}
	for _, v := range p.Entries {
		if _, _, ok := addressRange(v); !ok {
			return fault.Invalid
		}
	}
	for _, r := range p.Rules {
		if r.Action != "allow" && r.Action != "reject" || len(r.IPs) > 1000 || len(r.IPSetIDs) > 50 || len(r.Periods) > 50 || len(r.Countries)+len(r.Provinces)+len(r.Cities) > 1000 {
			return fault.Invalid
		}
		for _, v := range r.IPs {
			if _, _, ok := addressRange(v); !ok {
				return fault.Invalid
			}
		}
		for _, v := range r.IPSetIDs {
			if !validUUID(v) {
				return fault.Invalid
			}
		}
		for _, v := range r.Periods {
			if _, ok := minute(v.Start); !ok {
				return fault.Invalid
			}
			if _, ok := minute(v.End); !ok {
				return fault.Invalid
			}
			if len(v.Days) == 0 {
				return fault.Invalid
			}
			for _, d := range v.Days {
				if d < 0 || d > 6 {
					return fault.Invalid
				}
			}
		}
	}
	for _, r := range p.Patterns {
		if len(r.Pattern) == 0 || len(r.Pattern) > 1024 || (r.Action != "allow" && r.Action != "reject") {
			return fault.Invalid
		}
		if _, e := regexp.Compile(r.Pattern); e != nil {
			return fault.Invalid
		}
	}
	return nil
}
func (s *Service) Policies(ctx context.Context, kind string) ([]PolicyDocument, error) {
	table := policyTables[kind]
	if table == "" {
		return nil, fault.Invalid
	}
	rows, e := s.db.Rows(ctx, "SELECT id::text,version,document FROM "+table+" ORDER BY updated_at,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []PolicyDocument{}
	for rows.Next() {
		var id string
		var version int64
		var b []byte
		if e = rows.Scan(&id, &version, &b); e != nil {
			return nil, e
		}
		var p PolicyDocument
		if e = json.Unmarshal(b, &p); e != nil {
			return nil, e
		}
		p.ID = id
		p.Version = version
		out = append(out, p)
	}
	return out, rows.Err()
}

// SavePolicy is used inside the application's audit transaction.
func (s *Service) SavePolicy(ctx context.Context, kind string, p PolicyDocument) (string, error) {
	if e := ValidatePolicy(kind, p); e != nil {
		return "", e
	}
	table := policyTables[kind]
	id, version := p.ID, p.Version
	p.ID = ""
	p.Version = 0
	b, e := json.Marshal(p)
	if e != nil {
		return "", e
	}
	if id == "" {
		e = s.db.Row(ctx, "INSERT INTO "+table+"(document) VALUES($1) RETURNING id::text", b).Scan(&id)
	} else {
		var n int64
		tag, err := s.db.Exec(ctx, "UPDATE "+table+" SET document=$2,version=version+1,updated_at=now() WHERE id=$1 AND version=$3", id, b, version)
		e = err
		if e == nil {
			n = tag.RowsAffected()
			if n != 1 {
				return "", fault.Conflict
			}
		}
	}
	if e != nil {
		return "", e
	}
	_, e = s.db.Exec(ctx, "UPDATE policy_revision SET revision=revision+1")
	return id, e
}
func (s *Service) DeletePolicy(ctx context.Context, kind, id string, version int64) error {
	table := policyTables[kind]
	if table == "" {
		return fault.Invalid
	}
	tag, e := s.db.Exec(ctx, "DELETE FROM "+table+" WHERE id=$1 AND version=$2", id, version)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	_, e = s.db.Exec(ctx, "UPDATE policy_revision SET revision=revision+1")
	return e
}
func intersects(a, b []string) bool {
	for _, v := range a {
		if slices.Contains(b, v) {
			return true
		}
	}
	return false
}
func applies(p PolicyDocument, f Facts) bool {
	if !p.Enabled {
		return false
	}
	if slices.Contains(f.ForcedIDs, p.ID) {
		return true
	}
	return (p.Global || slices.Contains(p.UserIDs, f.UserID) || intersects(p.DepartmentIDs, f.DepartmentIDs)) && (len(p.ResourceIDs) == 0 || intersects(p.ResourceIDs, f.ResourceIDs))
}
func matchesTime(periods []Period, now time.Time) bool {
	if len(periods) == 0 {
		return true
	}
	m := now.Hour()*60 + now.Minute()
	day := int(now.Weekday())
	for _, p := range periods {
		start, _ := minute(p.Start)
		end, _ := minute(p.End)
		if start <= end && slices.Contains(p.Days, day) && m >= start && m < end {
			return true
		}
		if start > end && ((slices.Contains(p.Days, day) && m >= start) || (slices.Contains(p.Days, (day+6)%7) && m < end)) {
			return true
		}
	}
	return false
}
func (s *Service) Evaluate(ctx context.Context, f Facts) (Decision, error) {
	all := map[string][]PolicyDocument{}
	for _, kind := range []string{"access-policy", "strategy", "command-filter"} {
		policies, e := s.Policies(ctx, kind)
		if e != nil {
			return Decision{}, e
		}
		all[kind] = policies
	}
	return s.evaluate(ctx, f, all)
}
func (s *Service) EvaluateAccess(ctx context.Context, f Facts, p PolicyDocument) (Decision, error) {
	p.Global = true
	p.Enabled = true
	p.ResourceIDs = nil
	return s.evaluate(ctx, f, map[string][]PolicyDocument{"access-policy": {p}})
}
func (s *Service) evaluate(ctx context.Context, f Facts, all map[string][]PolicyDocument) (Decision, error) {
	d := Decision{Allowed: true, Reason: "allowed"}
	if f.Now.IsZero() {
		f.Now = time.Now()
	}
	if e := s.db.Row(ctx, "SELECT revision FROM policy_revision").Scan(&d.Revision); e != nil {
		return d, e
	}
	deny := func(id, reason string) (Decision, error) {
		d.Allowed = false
		d.PolicyID = id
		d.Reason = reason
		return d, nil
	}
	for _, kind := range []string{"access-policy", "strategy", "command-filter"} {
		policies := all[kind]
		for _, p := range policies {
			if slices.Contains(f.ForcedIDs, p.ID) && !p.Enabled {
				return deny(p.ID, "bound_policy_disabled")
			}
			if !applies(p, f) {
				continue
			}
			if kind == "strategy" {
				if allowed, present := p.Actions[f.Action]; present && !allowed {
					return deny(p.ID, "action_denied")
				}
				continue
			}
			if kind == "command-filter" {
				d.Filtered = true
				if f.Action != "execute" {
					continue
				}
				allowed := p.Mode == "blacklist"
				for _, r := range p.Patterns {
					re, e := regexp.Compile(r.Pattern)
					if e != nil {
						return d, e
					}
					if re.MatchString(f.Command) {
						if r.Action == "reject" {
							return deny(p.ID, "command_denied")
						}
						allowed = true
					}
				}
				if !allowed {
					return deny(p.ID, "command_not_allowed")
				}
				continue
			}
			loc, e := time.LoadLocation(p.Timezone)
			if e != nil {
				return d, e
			}
			allowed := p.Mode == "blacklist"
			sort.SliceStable(p.Rules, func(i, j int) bool { return p.Rules[i].Priority < p.Rules[j].Priority })
			for _, r := range p.Rules {
				if !r.Enabled || (r.ExpiresAt != nil && !f.Now.Before(*r.ExpiresAt)) {
					continue
				}
				ips := append([]string{}, r.IPs...)
				for _, id := range r.IPSetIDs {
					var data []byte
					if e = s.db.Row(ctx, "SELECT document FROM ip_sets WHERE id=$1", id).Scan(&data); e != nil {
						return deny(p.ID, "ip_set_unavailable")
					}
					var set PolicyDocument
					if e = json.Unmarshal(data, &set); e != nil {
						return d, e
					}
					if !set.Enabled {
						return deny(p.ID, "ip_set_unavailable")
					}
					ips = append(ips, set.Entries...)
				}
				if len(ips) > 0 && !IPMatches(f.IP, ips) {
					continue
				}
				if !matchesTime(r.Periods, f.Now.In(loc)) {
					continue
				}
				if len(r.Countries)+len(r.Provinces)+len(r.Cities) > 0 {
					geo, e := s.Geo(ctx, f.IP)
					if e != nil {
						return deny(p.ID, "geodata_unavailable")
					}
					if len(r.Countries) > 0 && !slices.Contains(r.Countries, geo.Country) || len(r.Provinces) > 0 && !slices.Contains(r.Provinces, geo.Province) || len(r.Cities) > 0 && !slices.Contains(r.Cities, geo.City) {
						continue
					}
				}
				if r.Action == "reject" {
					return deny(p.ID, "access_rule_denied")
				}
				allowed = true
			}
			if !allowed {
				return deny(p.ID, "access_not_allowlisted")
			}
		}
	}
	return d, nil
}
