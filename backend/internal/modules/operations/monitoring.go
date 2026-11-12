package operations

import (
	"context"
	"encoding/json"
	"math"
	"next-terminal/backend/internal/modules/connectivity/agentwire"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"strings"
	"time"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db: db} }

type MonitorRule struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Metric      string     `json:"metric"`
	AgentID     string     `json:"agentId"`
	Threshold   float64    `json:"threshold"`
	Recovery    float64    `json:"recovery"`
	HoldSeconds int        `json:"holdSeconds"`
	Enabled     bool       `json:"enabled"`
	Version     int64      `json:"version"`
	CreatedBy   string     `json:"-"`
	PendingAt   *time.Time `json:"pendingAt"`
	Firing      bool       `json:"firing"`
	Value       *float64   `json:"value"`
	State       string     `json:"state"`
	CheckedAt   *time.Time `json:"checkedAt"`
}
type SystemEvent struct {
	ID             string     `json:"id"`
	RuleID         string     `json:"ruleId"`
	Name           string     `json:"name"`
	Metric         string     `json:"metric"`
	Kind           string     `json:"kind"`
	Value          *float64   `json:"value"`
	CreatedAt      time.Time  `json:"createdAt"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt"`
}

func (s *Service) MonitorRules(ctx context.Context) ([]MonitorRule, error) {
	rows, err := s.db.Rows(ctx, `SELECT id,name,metric,coalesce(agent_id::text,''),threshold,recovery,hold_seconds,enabled,version,created_by,pending_at,firing,last_value,sample_state,checked_at FROM monitor_rules WHERE deleted_at IS NULL ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonitorRule{}
	for rows.Next() {
		var r MonitorRule
		if err = rows.Scan(&r.ID, &r.Name, &r.Metric, &r.AgentID, &r.Threshold, &r.Recovery, &r.HoldSeconds, &r.Enabled, &r.Version, &r.CreatedBy, &r.PendingAt, &r.Firing, &r.Value, &r.State, &r.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) SaveMonitorRule(ctx context.Context, r MonitorRule, actor string) (string, error) {
	if !s.db.InTransaction(ctx) || strings.TrimSpace(r.Name) == "" || len(r.Name) > 100 || r.HoldSeconds < 0 || r.HoldSeconds > 3600 || math.IsNaN(r.Threshold) || math.IsInf(r.Threshold, 0) || math.IsNaN(r.Recovery) || math.IsInf(r.Recovery, 0) || r.Recovery < 0 || r.Threshold <= r.Recovery {
		return "", fault.Invalid
	}
	metric, known := monitorMetric(r.Metric)
	if !known || r.Threshold < metric.Min || r.Threshold > metric.Max {
		return "", fault.Invalid
	}
	if metric.Scope == "platform" {
		if r.AgentID != "" {
			return "", fault.Invalid
		}
	} else {
		var exists bool
		if r.AgentID == "" {
			return "", fault.Invalid
		}
		if err := s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM agent_gateway_configs WHERE id=$1 AND revoked_at IS NULL)`, r.AgentID).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return "", fault.Invalid
		}
	}
	if _, err := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(651601)`); err != nil {
		return "", err
	}
	if r.ID == "" {
		var count int
		if err := s.db.Row(ctx, `SELECT count(*) FROM monitor_rules WHERE deleted_at IS NULL`).Scan(&count); err != nil {
			return "", err
		}
		if count >= 500 {
			return "", fault.Conflict
		}
		err := s.db.Row(ctx, `INSERT INTO monitor_rules(name,metric,agent_id,threshold,recovery,hold_seconds,enabled,created_by) VALUES($1,$2,nullif($3,'')::uuid,$4,$5,$6,$7,$8) RETURNING id`, r.Name, r.Metric, r.AgentID, r.Threshold, r.Recovery, r.HoldSeconds, r.Enabled, actor).Scan(&r.ID)
		return r.ID, err
	}
	// Configuration changes close the old condition explicitly, never as a recovery.
	if _, err := s.db.Exec(ctx, `INSERT INTO system_events(rule_id,rule_name,metric,kind,value) SELECT id,name,metric,'cancelled',last_value FROM monitor_rules WHERE id=$1 AND version=$2 AND firing AND deleted_at IS NULL`, r.ID, r.Version); err != nil {
		return "", err
	}
	tag, err := s.db.Exec(ctx, `UPDATE monitor_rules SET name=$2,metric=$3,agent_id=nullif($4,'')::uuid,threshold=$5,recovery=$6,hold_seconds=$7,enabled=$8,version=version+1,pending_at=NULL,firing=false,last_value=NULL,sample_state='unknown',checked_at=NULL WHERE id=$1 AND version=$9 AND deleted_at IS NULL`, r.ID, r.Name, r.Metric, r.AgentID, r.Threshold, r.Recovery, r.HoldSeconds, r.Enabled, r.Version)
	if err == nil && tag.RowsAffected() != 1 {
		err = fault.Conflict
	}
	return r.ID, err
}

func (s *Service) DeleteMonitorRule(ctx context.Context, id string, version int64) error {
	if !s.db.InTransaction(ctx) {
		return fault.Invalid
	}
	if _, err := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(651601)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO system_events(rule_id,rule_name,metric,kind,value) SELECT id,name,metric,'cancelled',last_value FROM monitor_rules WHERE id=$1 AND version=$2 AND firing AND deleted_at IS NULL`, id, version); err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `UPDATE monitor_rules SET deleted_at=now(),enabled=false,firing=false,pending_at=NULL,version=version+1 WHERE id=$1 AND version=$2 AND deleted_at IS NULL`, id, version)
	if err == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return err
}

func (s *Service) SystemEvents(ctx context.Context, before time.Time, beforeID string, limit int) ([]SystemEvent, error) {
	rows, err := s.db.Rows(ctx, `SELECT id,rule_id,rule_name,metric,kind,value,created_at,acknowledged_at FROM system_events WHERE (created_at,id)<($1,$2::uuid) ORDER BY created_at DESC,id DESC LIMIT $3`, before, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SystemEvent{}
	for rows.Next() {
		var e SystemEvent
		if err = rows.Scan(&e.ID, &e.RuleID, &e.Name, &e.Metric, &e.Kind, &e.Value, &e.CreatedAt, &e.AcknowledgedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) AcknowledgeEvent(ctx context.Context, id, actor string) error {
	tag, err := s.db.Exec(ctx, `UPDATE system_events SET acknowledged_at=coalesce(acknowledged_at,now()),acknowledged_by=coalesce(acknowledged_by,$2::uuid) WHERE id=$1`, id, actor)
	if err == nil && tag.RowsAffected() != 1 {
		return fault.Invalid
	}
	return err
}

// Sample persisted receipt timestamps, not this node's in-memory connections.
// Missing/stale telemetry never becomes zero and cannot trigger false recovery.
func (s *Service) monitorSample(ctx context.Context, r MonitorRule, now time.Time) (*float64, error) {
	if query, ok := platformSamples[r.Metric]; ok {
		var value float64
		if err := s.db.Row(ctx, query, now).Scan(&value); err != nil {
			return nil, err
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return nil, nil
		}
		return &value, nil
	}
	if r.Metric == "configuration.delay" {
		view, err := system.New(s.db).ConfigurationView(ctx, 0)
		if err != nil {
			return nil, err
		}
		value := 0.0
		if view.State != "effective" {
			value = math.Max(0, now.Sub(view.CreatedAt).Seconds())
		}
		return &value, nil
	}
	var received, revoked *time.Time
	var created time.Time
	var raw []byte
	if err := s.db.Row(ctx, `SELECT metrics_at,metrics,created_at,revoked_at FROM agent_gateway_configs WHERE id=$1`, r.AgentID).Scan(&received, &raw, &created, &revoked); err != nil {
		return nil, err
	}
	if revoked != nil {
		return nil, nil
	}
	if r.Metric == "agent.silence" {
		last := created
		if received != nil {
			last = *received
		}
		value := math.Max(0, now.Sub(last).Seconds())
		return &value, nil
	}
	var metrics agentwire.Metrics
	if received == nil || len(raw) == 0 || json.Unmarshal(raw, &metrics) != nil {
		return nil, nil
	}
	interval := metrics.IntervalSeconds
	if interval < 5 || interval > 300 {
		interval = 5
	}
	if now.Sub(*received) > time.Duration(interval*3)*time.Second {
		return nil, nil
	}
	var value float64
	switch r.Metric {
	case "agent.cpu":
		if metrics.CPU == nil {
			return nil, nil
		}
		value = *metrics.CPU
	case "agent.memory":
		if metrics.MemoryTotal == 0 || metrics.MemoryUsed > metrics.MemoryTotal {
			return nil, nil
		}
		value = float64(metrics.MemoryUsed) / float64(metrics.MemoryTotal) * 100
	case "agent.disk":
		found := false
		for _, disk := range metrics.Disks {
			if disk.Total > 0 && disk.Used <= disk.Total {
				found = true
				value = math.Max(value, float64(disk.Used)/float64(disk.Total)*100)
			}
		}
		if !found {
			return nil, nil
		}
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return nil, nil
	}
	return &value, nil
}

type MonitorNotify func(context.Context, string, string, string, string) error

func (s *Service) EvaluateMonitors(ctx context.Context, notify MonitorNotify) error {
	return s.db.Within(ctx, func(c context.Context) error {
		var locked bool
		if err := s.db.Row(c, `SELECT pg_try_advisory_xact_lock(651601)`).Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return nil
		}
		rules, err := s.MonitorRules(c)
		if err != nil {
			return err
		}
		var now time.Time
		if err = s.db.Row(c, `SELECT now()`).Scan(&now); err != nil {
			return err
		}
		samples := map[string]*float64{}
		for _, r := range rules {
			if !r.Enabled {
				continue
			}
			key := r.Metric + ":" + r.AgentID
			value, sampled := samples[key]
			if !sampled {
				value, err = s.monitorSample(c, r, now)
				if err != nil {
					return err
				}
				samples[key] = value
			}
			state, kind := "unknown", ""
			pending := r.PendingAt
			firing := r.Firing
			if value == nil {
				pending = nil
			} else if firing {
				state = "firing"
				if *value <= r.Recovery {
					firing = false
					state = "normal"
					kind = "recovered"
					pending = nil
				}
			} else if *value >= r.Threshold {
				state = "pending"
				if pending == nil {
					pending = &now
				}
				if now.Sub(*pending) >= time.Duration(r.HoldSeconds)*time.Second {
					firing = true
					state = "firing"
					kind = "firing"
				}
			} else {
				pending = nil
				state = "normal"
			}
			if _, err = s.db.Exec(c, `UPDATE monitor_rules SET pending_at=$2,firing=$3,last_value=$4,sample_state=$5,checked_at=$6 WHERE id=$1`, r.ID, pending, firing, value, state, now); err != nil {
				return err
			}
			if kind != "" {
				var id string
				if err = s.db.Row(c, `INSERT INTO system_events(rule_id,rule_name,metric,kind,value) VALUES($1,$2,$3,$4,$5) RETURNING id`, r.ID, r.Name, r.Metric, kind, value).Scan(&id); err != nil {
					return err
				}
				if err = notify(c, id, kind, r.CreatedBy, r.Name); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
