package automation

import (
	"context"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct{ db *postgres.DB }

func New(db *postgres.DB) *Service { return &Service{db: db} }

type Snippet struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	Version     int64     `json:"version"`
	System      bool      `json:"system"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type SnippetPage struct {
	Items []Snippet `json:"items"`
	Total int64     `json:"total"`
}

func (s *Service) Snippets(ctx context.Context, actor, scope, search string, page int) (SnippetPage, error) {
	out := SnippetPage{Items: []Snippet{}}
	// Apply the same ownership predicate before both counting and pagination.
	predicate := `((owner_id=$1::uuid AND $2 IN ('mine','all')) OR (owner_id IS NULL AND $2 IN ('system','all'))) AND (strpos(lower(name),lower($3))>0 OR strpos(lower(description),lower($3))>0)`
	if e := s.db.Row(ctx, `SELECT count(*) FROM snippets WHERE `+predicate, actor, scope, search).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := s.db.Rows(ctx, `SELECT id,name,description,content,version,owner_id IS NULL,updated_at FROM snippets WHERE `+predicate+` ORDER BY updated_at DESC,id LIMIT 30 OFFSET $4`, actor, scope, search, (page-1)*30)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var v Snippet
		if e = rows.Scan(&v.ID, &v.Name, &v.Description, &v.Content, &v.Version, &v.System, &v.UpdatedAt); e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveSnippet(ctx context.Context, actor string, system bool, v Snippet) (string, error) {
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" || utf8.RuneCountInString(v.Name) > 120 || utf8.RuneCountInString(v.Description) > 1000 || len(v.Content) > 4096 || strings.TrimSpace(v.Content) == "" || !utf8.ValidString(v.Content) || strings.ContainsRune(v.Content, 0) {
		return "", fault.Invalid
	}
	// NULL owns a system snippet. Personal endpoints can never publish or mutate system snippets.
	var owner any = actor
	if system {
		owner = nil
	}
	if v.ID == "" {
		var id string
		e := s.db.Row(ctx, `INSERT INTO snippets(owner_id,name,description,content) VALUES($1,$2,$3,$4) RETURNING id`, owner, v.Name, v.Description, v.Content).Scan(&id)
		return id, e
	}
	tag, e := s.db.Exec(ctx, `UPDATE snippets SET name=$3,description=$4,content=$5,version=version+1,updated_at=now() WHERE id=$1 AND owner_id IS NOT DISTINCT FROM $2::uuid AND version=$6`, v.ID, owner, v.Name, v.Description, v.Content, v.Version)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() != 1 {
		return "", fault.Conflict
	}
	return v.ID, nil
}
func (s *Service) DeleteSnippet(ctx context.Context, actor string, system bool, id string, version int64) error {
	var owner any = actor
	if system {
		owner = nil
	}
	tag, e := s.db.Exec(ctx, `DELETE FROM snippets WHERE id=$1 AND owner_id IS NOT DISTINCT FROM $2::uuid AND version=$3`, id, owner, version)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}
