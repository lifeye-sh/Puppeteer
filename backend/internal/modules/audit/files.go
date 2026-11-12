package audit

import (
	"context"
	"time"
)

type FileEvent struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"sessionId"`
	Actor       string    `json:"actor"`
	Action      string    `json:"action"`
	Path        string    `json:"path"`
	Destination string    `json:"destination"`
	State       string    `json:"state"`
	Bytes       int64     `json:"bytes"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (s *Service) StartFile(ctx context.Context, session, actor, action, path, destination string) (string, error) {
	var id string
	e := s.db.Row(ctx, `INSERT INTO filesystem_logs(session_id,actor_id,action,path,destination) VALUES($1,$2,$3,$4,$5) RETURNING id`, session, actor, action, path, destination).Scan(&id)
	return id, e
}
func (s *Service) EndFile(ctx context.Context, id, state, message string, bytes int64) error {
	_, e := s.db.Exec(ctx, `UPDATE filesystem_logs SET state=$2,message=$3,bytes=$4,ended_at=now() WHERE id=$1`, id, state, message, bytes)
	return e
}
func (s *Service) FilePage(ctx context.Context, actor string, offset int) ([]FileEvent, int, error) {
	var total int
	if e := s.db.Row(ctx, `SELECT count(*) FROM filesystem_logs WHERE ($1='' OR actor_id::text=$1)`, actor).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT l.id,l.session_id,u.username,l.action,l.path,l.destination,l.state,l.bytes,l.message,l.created_at FROM filesystem_logs l JOIN users u ON u.id=l.actor_id WHERE ($1='' OR actor_id::text=$1) ORDER BY l.created_at DESC,l.id DESC OFFSET $2 LIMIT 20`, actor, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []FileEvent{}
	for rows.Next() {
		var v FileEvent
		if e = rows.Scan(&v.ID, &v.SessionID, &v.Actor, &v.Action, &v.Path, &v.Destination, &v.State, &v.Bytes, &v.Message, &v.CreatedAt); e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
func (s *Service) InterruptFiles(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE filesystem_logs SET state='interrupted',message='服务重启，操作结果需核对',ended_at=now() WHERE state='running'`)
	return e
}
func (s *Service) StartUpload(ctx context.Context, id, session, actor, path string) error {
	_, e := s.db.Exec(ctx, `INSERT INTO filesystem_logs(task_id,session_id,actor_id,action,path) VALUES($1,$2,$3,'upload',$4) ON CONFLICT(task_id) WHERE task_id IS NOT NULL DO NOTHING`, id, session, actor, path)
	return e
}
func (s *Service) SyncUploads(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `UPDATE filesystem_logs l SET state=t.state,message=t.message,bytes=CASE WHEN t.state='succeeded' THEN t.size ELSE t.received END,ended_at=now() FROM upload_tasks t WHERE l.task_id=t.id AND l.state='running' AND t.state IN ('succeeded','failed','interrupted','canceled')`)
	return e
}
