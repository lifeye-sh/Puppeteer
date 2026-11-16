package authorization

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const MaxBatchGrants = 500

type GrantBatch struct {
	UserIDs         []string   `json:"userIds"`
	DepartmentIDs   []string   `json:"departmentIds"`
	ResourceIDs     []string   `json:"resourceIds"`
	StrategyID      string     `json:"strategyId"`
	CommandFilterID string     `json:"commandFilterId"`
	ValidFrom       time.Time  `json:"validFrom"`
	ExpiresAt       *time.Time `json:"expiresAt"`
}
type GrantBatchItem struct {
	ID           string `json:"id,omitempty"`
	UserID       string `json:"userId,omitempty"`
	DepartmentID string `json:"departmentId,omitempty"`
	ResourceID   string `json:"resourceId"`
	Status       string `json:"status"`
}
type GrantBatchResult struct {
	Total   int              `json:"total"`
	Created int              `json:"created"`
	Skipped int              `json:"skipped"`
	Items   []GrantBatchItem `json:"items"`
}
type GrantBatchInvalid struct{ Message string }

func (e *GrantBatchInvalid) Error() string { return e.Message }
func batchInvalid(message string) error    { return &GrantBatchInvalid{Message: message} }

func NormalizeGrantBatch(in GrantBatch) (GrantBatch, error) {
	for _, ids := range []*[]string{&in.UserIDs, &in.DepartmentIDs, &in.ResourceIDs} {
		if len(*ids) > 100 {
			return in, batchInvalid("每类最多选择 100 个对象")
		}
		seen := map[string]bool{}
		out := []string{}
		for _, id := range *ids {
			if !validUUID(id) {
				return in, batchInvalid("用户、部门或资源编号无效")
			}
			id = strings.ToLower(id)
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		sort.Strings(out)
		*ids = out
	}
	total := (len(in.UserIDs) + len(in.DepartmentIDs)) * len(in.ResourceIDs)
	if total == 0 {
		return in, batchInvalid("请至少选择一个用户或部门，以及一个资产或分组")
	}
	if total > MaxBatchGrants {
		return in, batchInvalid(fmt.Sprintf("本次包含 %d 条授权，单批最多 %d 条，请减少选择", total, MaxBatchGrants))
	}
	for _, id := range []string{in.StrategyID, in.CommandFilterID} {
		if id != "" && !validUUID(id) {
			return in, batchInvalid("动作策略或命令过滤编号无效")
		}
	}
	if in.ValidFrom.IsZero() {
		in.ValidFrom = time.Now().UTC()
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(in.ValidFrom) {
		return in, batchInvalid("到期时间必须晚于开始时间")
	}
	return in, nil
}

// The management transaction covers preflight, every insert, and audit together.
// Existing grants (including expired/approval grants) are never overwritten.
func (s *Service) SaveGrantBatch(ctx context.Context, input GrantBatch) (GrantBatchResult, error) {
	out := GrantBatchResult{Items: []GrantBatchItem{}}
	if !s.db.InTransaction(ctx) {
		return out, errors.New("batch grants require a transaction")
	}
	in, err := NormalizeGrantBatch(input)
	if err != nil {
		return out, err
	}
	for _, entry := range []struct {
		ids          []string
		query, label string
	}{
		{in.UserIDs, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL)`, "用户不存在或已删除"},
		{in.DepartmentIDs, `SELECT EXISTS(SELECT 1 FROM departments WHERE id=$1)`, "部门不存在"},
	} {
		for _, id := range entry.ids {
			var exists bool
			if err = s.db.Row(ctx, entry.query, id).Scan(&exists); err != nil {
				return out, err
			}
			if !exists {
				return out, batchInvalid(entry.label + "：" + id)
			}
		}
	}
	kinds := map[string]string{}
	for _, id := range in.ResourceIDs {
		var kind string
		err = s.db.Row(ctx, `SELECT kind FROM resource_registry WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, batchInvalid("资产或分组不存在或已删除：" + id)
		}
		if err != nil {
			return out, err
		}
		kinds[id] = kind
	}
	for _, entry := range []struct{ id, query, label string }{
		{in.StrategyID, `SELECT EXISTS(SELECT 1 FROM strategies WHERE id=$1)`, "动作策略不存在"},
		{in.CommandFilterID, `SELECT EXISTS(SELECT 1 FROM command_filters WHERE id=$1)`, "命令过滤不存在"},
	} {
		if entry.id == "" {
			continue
		}
		var exists bool
		if err = s.db.Row(ctx, entry.query, entry.id).Scan(&exists); err != nil {
			return out, err
		}
		if !exists {
			return out, batchInvalid(entry.label)
		}
	}
	subjects := []GrantBatchItem{}
	for _, id := range in.UserIDs {
		subjects = append(subjects, GrantBatchItem{UserID: id})
	}
	for _, id := range in.DepartmentIDs {
		subjects = append(subjects, GrantBatchItem{DepartmentID: id})
	}
	for _, subject := range subjects {
		for _, resource := range in.ResourceIDs {
			item := subject
			item.ResourceID = resource
			strategy, filter := in.StrategyID, in.CommandFilterID
			// A mixed selection must not turn SSH policies into SQL write permissions.
			if kinds[resource] == "database" {
				strategy, filter = "", ""
			}
			err = s.db.Row(ctx, `INSERT INTO grants(user_id,department_id,resource_id,valid_from,expires_at,strategy_id,command_filter_id)
   VALUES(NULLIF($1,'')::uuid,NULLIF($2,'')::uuid,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid)
   ON CONFLICT DO NOTHING RETURNING id::text`, item.UserID, item.DepartmentID, resource, in.ValidFrom, in.ExpiresAt, strategy, filter).Scan(&item.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				item.Status = "skipped"
				out.Skipped++
			} else if err != nil {
				return GrantBatchResult{}, err
			} else {
				item.Status = "created"
				out.Created++
			}
			out.Items = append(out.Items, item)
		}
	}
	out.Total = len(out.Items)
	return out, nil
}
