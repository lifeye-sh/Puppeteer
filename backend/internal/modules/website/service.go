package website

import (
	"context"
	"encoding/json"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/idna"
	"net"
	"net/http"
	"net/url"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"path"
	"regexp"
	"strings"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db} }

type Rewrite struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}
type Website struct {
	URL             string                       `json:"url,omitempty"`
	ID              string                       `json:"id"`
	Name            string                       `json:"name"`
	Enabled         bool                         `json:"enabled"`
	Version         int64                        `json:"version"`
	GroupID         string                       `json:"groupId"`
	Domain          string                       `json:"domain"`
	EntryPath       string                       `json:"entryPath"`
	Upstream        string                       `json:"upstream"`
	Mode            string                       `json:"mode"`
	Password        string                       `json:"password,omitempty"`
	ClearPassword   bool                         `json:"clearPassword,omitempty"`
	HasPassword     bool                         `json:"hasPassword"`
	Policy          authorization.PolicyDocument `json:"policy"`
	CertificateID   string                       `json:"certificateId"`
	ClientCAID      string                       `json:"clientCaId"`
	UpstreamCA      string                       `json:"upstreamCa"`
	AgentID         string                       `json:"agentId"`
	JumpID          string                       `json:"jumpId"`
	GatewayGroupID  string                       `json:"gatewayGroupId"`
	ProxyID         string                       `json:"proxyId"`
	RequestHeaders  map[string]string            `json:"requestHeaders"`
	ResponseHeaders map[string]string            `json:"responseHeaders"`
	Rewrites        []Rewrite                    `json:"rewrites"`
	Audit           bool                         `json:"auditEnabled"`
	TempTTL         int                          `json:"tempTtlMinutes"`
	TempMax         int                          `json:"tempMaxMinutes"`
	TempAutoRenew   bool                         `json:"tempAutoRenew"`
}
type Settings struct {
	DefaultCertificateID  string   `json:"defaultCertificateId"`
	ConsoleURL            string   `json:"consoleUrl"`
	APITrustedProxies     []string `json:"apiTrustedProxies"`
	WebsiteTrustedProxies []string `json:"websiteTrustedProxies"`
	Version               int64    `json:"version"`
}

const columns = `w.id::text,r.display_name,r.enabled,r.version,coalesce(w.group_id::text,''),w.domain,w.entry_path,w.upstream,w.mode,w.public_password_hash<>'',w.policy,coalesce(w.certificate_id::text,''),coalesce(w.client_ca_id::text,''),w.upstream_ca,coalesce(w.agent_id::text,''),coalesce(w.jump_id::text,''),coalesce(w.gateway_group_id::text,''),coalesce(w.proxy_id::text,''),w.request_headers,w.response_headers,w.rewrites,w.audit_enabled,w.temp_ttl_minutes,w.temp_max_minutes,w.temp_auto_renew`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (v Website, e error) {
	var policy, request, response, rewrites []byte
	e = row.Scan(&v.ID, &v.Name, &v.Enabled, &v.Version, &v.GroupID, &v.Domain, &v.EntryPath, &v.Upstream, &v.Mode, &v.HasPassword, &policy, &v.CertificateID, &v.ClientCAID, &v.UpstreamCA, &v.AgentID, &v.JumpID, &v.GatewayGroupID, &v.ProxyID, &request, &response, &rewrites, &v.Audit, &v.TempTTL, &v.TempMax, &v.TempAutoRenew)
	if e != nil {
		return
	}
	for _, p := range []struct {
		b []byte
		v any
	}{{policy, &v.Policy}, {request, &v.RequestHeaders}, {response, &v.ResponseHeaders}, {rewrites, &v.Rewrites}} {
		if e = json.Unmarshal(p.b, p.v); e != nil {
			return
		}
	}
	return
}
func (s *Service) List(ctx context.Context) ([]Website, error) {
	rows, e := s.db.Rows(ctx, "SELECT "+columns+" FROM websites w JOIN resource_registry r ON r.id=w.id WHERE r.deleted_at IS NULL ORDER BY r.display_name,w.id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Website{}
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Get(ctx context.Context, id string) (Website, error) {
	return scan(s.db.Row(ctx, "SELECT "+columns+" FROM websites w JOIN resource_registry r ON r.id=w.id WHERE w.id=$1 AND r.deleted_at IS NULL", id))
}
func (s *Service) Domain(ctx context.Context, domain string) (Website, error) {
	return scan(s.db.Row(ctx, "SELECT "+columns+" FROM websites w JOIN resource_registry r ON r.id=w.id WHERE w.domain=$1 AND r.deleted_at IS NULL AND r.enabled", domain))
}
func Domain(value string) (string, error) {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if len(value) > 253 || strings.ContainsAny(value, "/:@\\* \r\n") || net.ParseIP(value) != nil {
		return "", fault.Invalid
	}
	ascii, e := idna.Lookup.ToASCII(value)
	if e != nil || !strings.Contains(ascii, ".") {
		return "", fault.Invalid
	}
	re := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	for _, part := range strings.Split(ascii, ".") {
		if !re.MatchString(part) {
			return "", fault.Invalid
		}
	}
	return ascii, nil
}
func SafePath(value string) bool {
	u, e := url.Parse(value)
	return e == nil && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !u.IsAbs() && u.Host == "" && !strings.ContainsAny(value, "\\\r\n\x00") && !strings.ContainsAny(u.Path, "\\\r\n\x00") && !strings.HasPrefix(u.Path, "//")
}
func Validate(v *Website, consoleURL string) error {
	var e error
	v.Domain, e = Domain(v.Domain)
	if e != nil {
		return e
	}
	console, e := url.Parse(consoleURL)
	if e != nil || strings.EqualFold(console.Hostname(), v.Domain) {
		return fault.Invalid
	}
	if v.Name == "" || len(v.Name) > 100 || len(v.Password) > 256 || v.ClearPassword && v.Password != "" || len(v.UpstreamCA) > 65536 {
		return fault.Invalid
	}
	if v.EntryPath == "" {
		v.EntryPath = "/"
	}
	if !SafePath(v.EntryPath) || strings.ContainsAny(v.EntryPath, "?#") || path.Clean(v.EntryPath) != strings.TrimSuffix(v.EntryPath, "/") && v.EntryPath != "/" {
		return fault.Invalid
	}
	u, e := url.Parse(v.Upstream)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.EqualFold(u.Hostname(), v.Domain) || strings.EqualFold(u.Hostname(), console.Hostname()) {
		return fault.Invalid
	}
	if v.Mode != "private" && v.Mode != "public" && v.Mode != "temporary_ip" {
		return fault.Invalid
	}
	if v.TempTTL < 1 || v.TempTTL > 1440 || v.TempMax < v.TempTTL || v.TempMax > 10080 {
		return fault.Invalid
	}
	routes := 0
	for _, id := range []string{v.AgentID, v.JumpID, v.GatewayGroupID, v.ProxyID} {
		if id != "" {
			routes++
		}
	}
	if routes > 1 {
		return fault.Invalid
	}
	v.Policy.Name = "website"
	v.Policy.Global = true
	v.Policy.Enabled = true
	if v.Policy.Timezone == "" {
		v.Policy.Timezone = "Asia/Shanghai"
	}
	if v.Policy.Mode == "" {
		v.Policy.Mode = "blacklist"
	}
	if e = authorization.ValidatePolicy("access-policy", v.Policy); e != nil {
		return e
	}
	for _, headers := range []map[string]string{v.RequestHeaders, v.ResponseHeaders} {
		if len(headers) > 32 {
			return fault.Invalid
		}
		for k, value := range headers {
			key := http.CanonicalHeaderKey(k)
			if key == "" || !httpguts.ValidHeaderFieldName(k) || !httpguts.ValidHeaderFieldValue(value) || len(value) > 4096 {
				return fault.Invalid
			}
			switch strings.ToLower(k) {
			case "host", "cookie", "set-cookie", "authorization", "proxy-authorization", "connection", "upgrade", "transfer-encoding", "content-length", "forwarded", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto", "origin", "x-real-ip", "x-auth-token", "x-internal-request-id":
				return fault.Invalid
			}
		}
	}
	if len(v.Rewrites) > 20 {
		return fault.Invalid
	}
	for _, r := range v.Rewrites {
		if r.Find == "" || len(r.Find) > 1024 || len(r.Replace) > 4096 {
			return fault.Invalid
		}
	}
	return nil
}

// Save is transactional with the resource registry and audit owned by the application.
func (s *Service) Save(ctx context.Context, v Website, passwordHash string) (string, error) {
	if !s.db.InTransaction(ctx) {
		var id string
		e := s.db.Within(ctx, func(c context.Context) error { var e error; id, e = s.Save(c, v, passwordHash); return e })
		return id, e
	}
	policy, e := json.Marshal(v.Policy)
	if e != nil {
		return "", e
	}
	if v.RequestHeaders == nil {
		v.RequestHeaders = map[string]string{}
	}
	if v.ResponseHeaders == nil {
		v.ResponseHeaders = map[string]string{}
	}
	if v.Rewrites == nil {
		v.Rewrites = []Rewrite{}
	}
	req, _ := json.Marshal(v.RequestHeaders)
	res, _ := json.Marshal(v.ResponseHeaders)
	rewrites, _ := json.Marshal(v.Rewrites)
	if v.ID == "" {
		if e := s.db.Admit(ctx, "resources", "resource.assets"); e != nil {
			return "", e
		}
		if e = s.db.Row(ctx, "INSERT INTO resource_registry(kind,display_name,enabled) VALUES('website',$1,$2) RETURNING id::text", v.Name, v.Enabled).Scan(&v.ID); e != nil {
			return "", e
		}
		if _, e = s.db.Exec(ctx, "INSERT INTO websites(id,domain,upstream) VALUES($1,$2,$3)", v.ID, v.Domain, v.Upstream); e != nil {
			return "", e
		}
	} else {
		tag, e := s.db.Exec(ctx, "UPDATE resource_registry SET display_name=$2,enabled=$3,version=version+1 WHERE id=$1 AND version=$4 AND deleted_at IS NULL AND kind='website'", v.ID, v.Name, v.Enabled, v.Version)
		if e != nil {
			return "", e
		}
		if tag.RowsAffected() != 1 {
			return "", fault.Conflict
		}
	}
	_, e = s.db.Exec(ctx, `UPDATE websites SET group_id=nullif($2,'')::uuid,domain=$3,entry_path=$4,upstream=$5,mode=$6,policy=$7,certificate_id=nullif($8,'')::uuid,client_ca_id=nullif($9,'')::uuid,upstream_ca=$10,agent_id=nullif($11,'')::uuid,jump_id=nullif($12,'')::uuid,gateway_group_id=nullif($13,'')::uuid,proxy_id=nullif($14,'')::uuid,request_headers=$15,response_headers=$16,rewrites=$17,audit_enabled=$18,temp_ttl_minutes=$19,temp_max_minutes=$20,temp_auto_renew=$21,public_password_hash=CASE WHEN $22<>'' OR $23 THEN $22 ELSE public_password_hash END WHERE id=$1`, v.ID, v.GroupID, v.Domain, v.EntryPath, v.Upstream, v.Mode, policy, v.CertificateID, v.ClientCAID, v.UpstreamCA, v.AgentID, v.JumpID, v.GatewayGroupID, v.ProxyID, req, res, rewrites, v.Audit, v.TempTTL, v.TempMax, v.TempAutoRenew, passwordHash, v.ClearPassword)
	return v.ID, e
}
func (s *Service) Settings(ctx context.Context) (v Settings, e error) {
	e = s.db.Row(ctx, `SELECT coalesce(default_certificate_id::text,''),console_url,api_trusted_proxies,website_trusted_proxies,version FROM website_settings`).Scan(&v.DefaultCertificateID, &v.ConsoleURL, &v.APITrustedProxies, &v.WebsiteTrustedProxies, &v.Version)
	return
}
func (s *Service) SaveSettings(ctx context.Context, v Settings) error {
	u, e := url.Parse(v.ConsoleURL)
	if e != nil || u.Hostname() == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return fault.Invalid
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || strings.HasSuffix(u.Hostname(), ".localhost"))) {
		return fault.Invalid
	}
	for _, ips := range [][]string{v.APITrustedProxies, v.WebsiteTrustedProxies} {
		for _, ip := range ips {
			if _, _, e := net.ParseCIDR(ip); e != nil {
				return fault.Invalid
			}
		}
	}
	if v.APITrustedProxies == nil {
		v.APITrustedProxies = []string{}
	}
	if v.WebsiteTrustedProxies == nil {
		v.WebsiteTrustedProxies = []string{}
	}
	tag, e := s.db.Exec(ctx, `UPDATE website_settings SET default_certificate_id=nullif($1,'')::uuid,console_url=$2,api_trusted_proxies=$3,website_trusted_proxies=$4,version=version+1 WHERE version=$5`, v.DefaultCertificateID, strings.TrimSuffix(v.ConsoleURL, "/"), v.APITrustedProxies, v.WebsiteTrustedProxies, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return e
}
