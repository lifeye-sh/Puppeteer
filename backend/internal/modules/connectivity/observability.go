package connectivity

import (
	"context"
	"encoding/json"
	"next-terminal/backend/internal/modules/connectivity/agentwire"
	"time"
)

type Observation struct {
	UpdateRequest     string             `json:"updateRequest"`
	UpdateCapable     bool               `json:"updateCapable"`
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	Version           string             `json:"version"`
	Online            bool               `json:"online"`
	Stale             bool               `json:"stale"`
	Streams           int                `json:"streams"`
	ReceivedAt        *time.Time         `json:"receivedAt"`
	Metrics           *agentwire.Metrics `json:"metrics"`
	RotationRequested bool               `json:"rotationRequested"`
	CredentialVersion int64              `json:"credentialVersion"`
}

func (s *Service) Observations(ctx context.Context) ([]Observation, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,name,version,metrics_at,metrics,rotation_requested,credential_version,capabilities ? 'managed-update.v1',update_request FROM agent_gateway_configs WHERE revoked_at IS NULL ORDER BY name LIMIT 1000`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		var v Observation
		var raw []byte
		if e = rows.Scan(&v.ID, &v.Name, &v.Version, &v.ReceivedAt, &raw, &v.RotationRequested, &v.CredentialVersion, &v.UpdateCapable, &v.UpdateRequest); e != nil {
			return nil, e
		}
		if raw != nil {
			if e = json.Unmarshal(raw, &v.Metrics); e != nil {
				return nil, e
			}
		}
		v.Online = s.AgentOnline(v.ID)
		threshold := 15 * time.Second
		if v.Metrics != nil && v.Metrics.IntervalSeconds >= 5 && v.Metrics.IntervalSeconds <= 300 {
			threshold = time.Duration(v.Metrics.IntervalSeconds*3) * time.Second
		}
		v.Stale = !v.Online || v.ReceivedAt == nil || time.Since(*v.ReceivedAt) > threshold
		s.agents.mu.Lock()
		if ch := s.agents.active[v.ID]; ch != nil {
			v.Streams = len(ch.slots)
		}
		s.agents.mu.Unlock()
		out = append(out, v)
	}
	return out, rows.Err()
}
