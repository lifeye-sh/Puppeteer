package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"next-terminal/backend/internal/platform/secretbox"
	"strings"
	"time"
)

type Service struct {
	db  *postgres.DB
	key []byte
}

func New(db *postgres.DB, key []byte) *Service { return &Service{db, key} }

type Config struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	ImplicitTLS bool   `json:"implicitTls"`
	Username    string `json:"username"`
	From        string `json:"from"`
	CA          string `json:"ca"`
	APIBase     string `json:"apiBase"`
	CorpID      string `json:"corpId"`
	AgentID     int64  `json:"agentId"`
}
type Channel struct {
	SettingsManaged bool   `json:"settingsManaged"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Enabled         bool   `json:"enabled"`
	Config          Config `json:"config"`
	Secret          string `json:"secret,omitempty"`
	ClearSecret     bool   `json:"clearSecret,omitempty"`
	HasSecret       bool   `json:"hasSecret"`
	Version         int64  `json:"version"`
}
type Binding struct {
	UserID    string `json:"userId"`
	ChannelID string `json:"channelId"`
	Address   string `json:"address"`
	Enabled   bool   `json:"enabled"`
}
type Preference struct {
	Enabled    bool   `json:"enabled"`
	QuietStart string `json:"quietStart"`
	QuietEnd   string `json:"quietEnd"`
	Timezone   string `json:"timezone"`
}
type Rule struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	Events       []string `json:"events"`
	ChannelIDs   []string `json:"channelIds"`
	Mode         string   `json:"recipientMode"`
	RecipientIDs []string `json:"recipientIds"`
	Version      int64    `json:"version"`
}

func (s *Service) Channels(ctx context.Context) ([]Channel, error) {
	rows, e := s.db.Rows(ctx, "SELECT id::text,name,type,enabled,config,(secret IS NOT NULL OR configuration_secret_id IS NOT NULL),version,settings_managed FROM notification_channels ORDER BY name,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		var v Channel
		var b []byte
		if e = rows.Scan(&v.ID, &v.Name, &v.Type, &v.Enabled, &b, &v.HasSecret, &v.Version, &v.SettingsManaged); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v.Config); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveChannel(ctx context.Context, v Channel) (string, error) {
	if v.ID != "" {
		var managed bool
		if e := s.db.Row(ctx, `SELECT settings_managed FROM notification_channels WHERE id=$1`, v.ID).Scan(&managed); e != nil {
			return "", e
		}
		if managed {
			return "", fault.Conflict
		}
	}
	if v.ClearSecret && v.Secret != "" {
		return "", fault.Invalid
	}
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 100 || len(v.Secret) > 8192 || len(v.Config.CA) > 65536 {
		return "", fault.Invalid
	}
	if v.Type == "smtp" {
		if v.Config.Host == "" || v.Config.Port < 1 || v.Config.Port > 65535 || strings.ContainsAny(v.Config.Host, "/\r\n") {
			return "", fault.Invalid
		}
		if _, e := mail.ParseAddress(v.Config.From); e != nil || strings.ContainsAny(v.Config.From, "\r\n") {
			return "", fault.Invalid
		}
	} else if v.Type == "sms-webhook" {
		u, e := url.Parse(v.Config.APIBase)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(v.Config.APIBase, "\r\n") {
			return "", fault.Invalid
		}
	} else if v.Type == "wecom" {
		u, e := url.Parse(v.Config.APIBase)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || v.Config.CorpID == "" || v.Config.AgentID < 1 {
			return "", fault.Invalid
		}
	} else {
		return "", fault.Invalid
	}
	b, e := json.Marshal(v.Config)
	if e != nil {
		return "", e
	}
	if v.Enabled && (v.Type == "wecom" || v.Type == "sms-webhook" || v.Config.Username != "") && v.Secret == "" {
		var exists bool
		if v.ID == "" || v.ClearSecret {
			return "", fault.Invalid
		}
		if e = s.db.Row(ctx, "SELECT secret IS NOT NULL FROM notification_channels WHERE id=$1", v.ID).Scan(&exists); e != nil {
			return "", e
		}
		if !exists {
			return "", fault.Invalid
		}
	}
	if v.ID == "" {
		if e = s.db.Row(ctx, "INSERT INTO notification_channels(name,type,enabled,config) VALUES($1,$2,$3,$4) RETURNING id::text", v.Name, v.Type, v.Enabled, b).Scan(&v.ID); e != nil {
			return "", e
		}
	} else {
		tag, e := s.db.Exec(ctx, "UPDATE notification_channels SET name=$2,type=$3,enabled=$4,config=$5,version=version+1 WHERE id=$1 AND version=$6", v.ID, v.Name, v.Type, v.Enabled, b, v.Version)
		if e != nil {
			return "", e
		}
		if tag.RowsAffected() != 1 {
			return "", fault.Conflict
		}
	}
	if v.ClearSecret {
		if _, e = s.db.Exec(ctx, "UPDATE notification_channels SET secret=NULL WHERE id=$1", v.ID); e != nil {
			return "", e
		}
	}
	if v.Secret != "" {
		sealed, e := secretbox.Seal(s.key, []byte(v.Secret), "notification:"+v.ID)
		if e != nil {
			return "", e
		}
		if _, e = s.db.Exec(ctx, "UPDATE notification_channels SET secret=$2 WHERE id=$1", v.ID, sealed); e != nil {
			return "", e
		}
	}
	return v.ID, nil
}
func (s *Service) Bindings(ctx context.Context, user string, admin bool) ([]Binding, error) {
	rows, e := s.db.Rows(ctx, "SELECT user_id::text,channel_id::text,address,enabled FROM notification_bindings WHERE user_id=$1 OR $2 ORDER BY user_id,channel_id", user, admin)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Binding{}
	for rows.Next() {
		var v Binding
		if e = rows.Scan(&v.UserID, &v.ChannelID, &v.Address, &v.Enabled); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Bind(ctx context.Context, v Binding) error {
	var kind string
	if e := s.db.Row(ctx, "SELECT type FROM notification_channels WHERE id=$1", v.ChannelID).Scan(&kind); e != nil {
		return e
	}
	if len(v.Address) == 0 || len(v.Address) > 254 || strings.ContainsAny(v.Address, "\r\n|,;") {
		return fault.Invalid
	}
	if kind == "smtp" {
		a, e := mail.ParseAddress(v.Address)
		if e != nil || a.Address != v.Address {
			return fault.Invalid
		}
	}
	_, e := s.db.Exec(ctx, "INSERT INTO notification_bindings VALUES($1,$2,$3,$4) ON CONFLICT(user_id,channel_id) DO UPDATE SET address=$3,enabled=$4", v.UserID, v.ChannelID, v.Address, v.Enabled)
	return e
}
func (s *Service) Preference(ctx context.Context, user string) (Preference, error) {
	p := Preference{Enabled: true, Timezone: "Asia/Shanghai"}
	rows, e := s.db.Rows(ctx, "SELECT enabled,quiet_start,quiet_end,timezone FROM notification_preferences WHERE user_id=$1", user)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	if rows.Next() {
		e = rows.Scan(&p.Enabled, &p.QuietStart, &p.QuietEnd, &p.Timezone)
	}
	return p, e
}
func (s *Service) SetPreference(ctx context.Context, user string, p Preference) error {
	if _, e := time.LoadLocation(p.Timezone); e != nil {
		return fault.Invalid
	}
	if (p.QuietStart == "") != (p.QuietEnd == "") {
		return fault.Invalid
	}
	if p.QuietStart != "" {
		if _, e := time.Parse("15:04", p.QuietStart); e != nil {
			return fault.Invalid
		}
		if _, e := time.Parse("15:04", p.QuietEnd); e != nil {
			return fault.Invalid
		}
	}
	_, e := s.db.Exec(ctx, "INSERT INTO notification_preferences VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id) DO UPDATE SET enabled=$2,quiet_start=$3,quiet_end=$4,timezone=$5", user, p.Enabled, p.QuietStart, p.QuietEnd, p.Timezone)
	return e
}
func (s *Service) Rules(ctx context.Context) ([]Rule, error) {
	rows, e := s.db.Rows(ctx, "SELECT id::text,name,enabled,events,channel_ids,recipient_mode,recipient_ids,version FROM notification_rules ORDER BY name,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var v Rule
		if e = rows.Scan(&v.ID, &v.Name, &v.Enabled, &v.Events, &v.ChannelIDs, &v.Mode, &v.RecipientIDs, &v.Version); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveRule(ctx context.Context, v Rule) (string, error) {
	if v.Name == "" || len(v.Name) > 100 || len(v.Events) == 0 || len(v.Events) > 12 || len(v.ChannelIDs) == 0 || len(v.ChannelIDs) > 20 || len(v.RecipientIDs) > 100 || (v.Mode != "requester" && v.Mode != "reviewers" && v.Mode != "specified") || (v.Mode == "specified" && len(v.RecipientIDs) == 0) {
		return "", fault.Invalid
	}
	for _, event := range v.Events {
		if event == "monitor.firing" || event == "monitor.recovered" {
			if v.Mode != "specified" {
				return "", fault.Invalid
			}
			continue
		}
		if event != "access.submitted" && event != "access.approved" && event != "access.rejected" && event != "access.cancelled" && event != "access.expired" && event != "database.submitted" && event != "database.approved" && event != "database.rejected" && event != "database.cancelled" && event != "database.finished" {
			return "", fault.Invalid
		}
	}
	if v.RecipientIDs == nil {
		v.RecipientIDs = []string{}
	}
	if v.ID == "" {
		e := s.db.Row(ctx, "INSERT INTO notification_rules(name,enabled,events,channel_ids,recipient_mode,recipient_ids) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text", v.Name, v.Enabled, v.Events, v.ChannelIDs, v.Mode, v.RecipientIDs).Scan(&v.ID)
		return v.ID, e
	}
	tag, e := s.db.Exec(ctx, "UPDATE notification_rules SET name=$2,enabled=$3,events=$4,channel_ids=$5,recipient_mode=$6,recipient_ids=$7,version=version+1 WHERE id=$1 AND version=$8", v.ID, v.Name, v.Enabled, v.Events, v.ChannelIDs, v.Mode, v.RecipientIDs, v.Version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return v.ID, e
}
func (s *Service) Enqueue(ctx context.Context, key, event, request, user string, reviewers []string) error {
	if reviewers == nil {
		reviewers = []string{}
	}
	payload, _ := json.Marshal(map[string]string{"title": "Puppeteer 访问审批", "text": fmt.Sprintf("申请 %s：%s。请登录控制台查看详情。", request, event)})
	_, e := s.db.Exec(ctx, "INSERT INTO notification_events(event_key,event_type,request_id,actor_id,reviewer_ids,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(event_key) DO NOTHING", key, event, request, user, reviewers, payload)
	return e
}
func (s *Service) Test(ctx context.Context, channel, user string) (string, error) {
	if e := s.db.RequireFeature(ctx, "notification.send"); e != nil {
		return "", e
	}
	var id string
	e := s.db.Row(ctx, "INSERT INTO notification_deliveries(channel_id,recipient_id,test) VALUES($1,$2,true) RETURNING id::text", channel, user).Scan(&id)
	return id, e
}
