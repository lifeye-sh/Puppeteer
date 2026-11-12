package session

import (
	"context"
	"encoding/json"
	"time"
)

type StatisticsTotals struct {
	Sessions int64 `json:"sessions"`
	Users    int64 `json:"users"`
	Assets   int64 `json:"assets"`
	Failed   int64 `json:"failed"`
	Seconds  int64 `json:"seconds"`
}
type StatisticsDay struct {
	Date string `json:"date"`
	StatisticsTotals
}
type StatisticsProtocol struct {
	Protocol string `json:"protocol"`
	Count    int64  `json:"count"`
}
type Statistics struct {
	From      string               `json:"from"`
	To        string               `json:"to"`
	Timezone  string               `json:"timezone"`
	Scope     string               `json:"scope"`
	Totals    StatisticsTotals     `json:"totals"`
	Days      []StatisticsDay      `json:"days"`
	Protocols []StatisticsProtocol `json:"protocols"`
}

// All aggregates use one PostgreSQL statement/snapshot. Purged audit rows are
// excluded, and a missing lease bounds abandoned sessions' operation time.
func (s *Service) Statistics(ctx context.Context, actor string, from, until time.Time, zone string) (Statistics, error) {
	v := Statistics{From: from.Format("2006-01-02"), To: until.AddDate(0, 0, -1).Format("2006-01-02"), Timezone: zone, Scope: "own"}
	if actor == "" {
		v.Scope = "all"
	}
	var totals, days, protocols []byte
	err := s.db.Row(ctx, `WITH scoped AS MATERIALIZED (
	 SELECT actor_id,asset_id,protocol,state,(created_at AT TIME ZONE $4)::date AS day,
	 CASE WHEN connected_at IS NULL THEN 0 ELSE greatest(0,floor(extract(epoch FROM
	 (least(coalesce(closed_at,lease_expires_at),now())-connected_at))))::bigint END AS seconds
	 FROM sessions WHERE audit_purged_at IS NULL AND ($1='' OR actor_id=NULLIF($1,'')::uuid)
	 AND created_at >= $2 AND created_at < $3
	), daily AS (
	 SELECT day,count(*) AS sessions,count(DISTINCT actor_id) AS users,count(DISTINCT asset_id) AS assets,
	 count(*) FILTER(WHERE state='failed') AS failed,coalesce(sum(seconds),0)::bigint AS seconds FROM scoped GROUP BY day
	), calendar AS (
	 SELECT (($2::timestamptz AT TIME ZONE $4)::date+n)::date AS day
	 FROM generate_series(0,($3::timestamptz AT TIME ZONE $4)::date-($2::timestamptz AT TIME ZONE $4)::date-1) AS n
	), protocols AS (SELECT protocol,count(*) AS count FROM scoped GROUP BY protocol)
	SELECT (SELECT json_build_object('sessions',count(*),'users',count(DISTINCT actor_id),'assets',count(DISTINCT asset_id),
	 'failed',count(*) FILTER(WHERE state='failed'),'seconds',coalesce(sum(seconds),0)::bigint) FROM scoped),
	 (SELECT coalesce(json_agg(json_build_object('date',to_char(c.day,'YYYY-MM-DD'),'sessions',coalesce(d.sessions,0),
	 'users',coalesce(d.users,0),'assets',coalesce(d.assets,0),'failed',coalesce(d.failed,0),'seconds',coalesce(d.seconds,0)) ORDER BY c.day),'[]'::json)
	 FROM calendar c LEFT JOIN daily d ON d.day=c.day),
	 (SELECT coalesce(json_agg(protocols ORDER BY protocol),'[]'::json) FROM protocols)`, actor, from, until, zone).Scan(&totals, &days, &protocols)
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(totals, &v.Totals); err != nil {
		return v, err
	}
	if err = json.Unmarshal(days, &v.Days); err != nil {
		return v, err
	}
	err = json.Unmarshal(protocols, &v.Protocols)
	return v, err
}
