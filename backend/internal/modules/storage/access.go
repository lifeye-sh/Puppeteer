package storage

import (
	"context"
	"github.com/jackc/pgx/v5"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"syscall"
)

type Policy struct {
	Upload   bool `json:"upload"`
	Download bool `json:"download"`
	Copy     bool `json:"clipboardCopy"`
	Paste    bool `json:"clipboardPaste"`
}
type Space struct {
	ID       string `json:"id"`
	Owner    string `json:"ownerId"`
	Name     string `json:"name"`
	Shared   bool   `json:"shared"`
	Enabled  bool   `json:"enabled"`
	Writable bool   `json:"writable"`
	Default  bool   `json:"isDefault"`
	Quota    int64  `json:"quotaBytes"`
	Used     int64  `json:"usedBytes"`
}
type actorKey struct{}
type Actor struct{ User, Channel, Session, IP string }

func WithActor(ctx context.Context, user, channel, session string, ips ...string) context.Context {
	ip := ""
	if len(ips) > 0 {
		ip = ips[0]
	}
	return context.WithValue(ctx, actorKey{}, Actor{user, channel, session, ip})
}

func (s *Service) authorizeAction(ctx context.Context, space, action string) error {
	actor, ok := ctx.Value(actorKey{}).(Actor)
	if !ok {
		return nil
	}
	subject, e := identity.LoadSubject(ctx, s.db, actor.User)
	if e != nil {
		return e
	}
	if !subject.Enabled {
		return syscall.EACCES
	}
	target, ip := space, actor.IP
	if actor.Session != "" {
		if e = s.db.Row(ctx, `SELECT asset_id::text,client_ip FROM sessions WHERE id=$1 AND actor_id=$2 AND state IN ('connecting','connected') AND NOT terminate_requested AND lease_expires_at>now()`, actor.Session, actor.User).Scan(&target, &ip); e != nil {
			return syscall.EACCES
		}
	}
	scope, e := resource.New(s.db).Scope(ctx, target)
	if e != nil {
		return e
	}
	authz := authorization.New(s.db)
	forced, e := authz.ForcedPolicies(ctx, actor.User, subject.DepartmentIDs, scope)
	if e != nil {
		return e
	}
	decision, e := authz.Evaluate(ctx, authorization.Facts{UserID: actor.User, DepartmentIDs: subject.DepartmentIDs, ResourceIDs: scope, ForcedIDs: forced, IP: ip, Action: action})
	if e != nil {
		return e
	}
	if !decision.Allowed {
		return syscall.EACCES
	}
	return nil
}
func (s *Service) Policy(ctx context.Context, user string) (Policy, error) {
	p := Policy{true, true, true, true}
	e := s.db.Row(ctx, "SELECT upload,download,clipboard_copy,clipboard_paste FROM user_channel_policy WHERE user_id=$1", user).Scan(&p.Upload, &p.Download, &p.Copy, &p.Paste)
	if e == pgx.ErrNoRows {
		return p, nil
	}
	return p, e
}
func (s *Service) SetPolicy(ctx context.Context, user string, p Policy) error {
	_, e := s.db.Exec(ctx, `INSERT INTO user_channel_policy(user_id,upload,download,clipboard_copy,clipboard_paste) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id) DO UPDATE SET upload=$2,download=$3,clipboard_copy=$4,clipboard_paste=$5`, user, p.Upload, p.Download, p.Copy, p.Paste)
	return e
}
func (s *Service) Access(ctx context.Context, user, space string, write bool) error {
	var writable bool
	e := s.db.Row(ctx, `SELECT sp.owner_id=$1 OR coalesce(a.writable,false) FROM storage_spaces sp LEFT JOIN storage_acl a ON a.space_id=sp.id AND a.user_id=$1 JOIN users u ON u.id=$1 WHERE sp.id=$2 AND sp.enabled AND u.status='enabled' AND (sp.owner_id=$1 OR (sp.shared AND a.user_id IS NOT NULL))`, user, space).Scan(&writable)
	if e == pgx.ErrNoRows {
		return syscall.EACCES
	}
	if e != nil {
		return e
	}
	if write && !writable {
		return syscall.EROFS
	}
	return nil
}
func (s *Service) authorize(ctx context.Context, space, operation string) error {
	a, ok := ctx.Value(actorKey{}).(Actor)
	if !ok {
		return nil
	} // trusted internal maintenance / migration
	write := operation == "write"
	if write {
		if e := s.db.RequireFeature(ctx, "files.write"); e != nil {
			return syscall.EACCES
		}
	}
	if a.Session != "" {
		var readonly bool
		if e := s.db.Row(ctx, `SELECT readonly FROM sessions WHERE id=$1 AND actor_id=$2 AND state IN ('connecting','connected') AND NOT terminate_requested AND lease_expires_at>now()`, a.Session, a.User).Scan(&readonly); e != nil {
			return syscall.EACCES
		}
		if write && readonly {
			return syscall.EROFS
		}
	}
	if e := s.Access(ctx, a.User, space, write); e != nil {
		return e
	}
	p, e := s.Policy(ctx, a.User)
	if e != nil {
		return e
	}
	if (write && !p.Upload) || (operation == "read" && !p.Download) {
		return syscall.EACCES
	}
	if write {
		return s.authorizeAction(ctx, space, "upload")
	}
	if operation == "read" {
		return s.authorizeAction(ctx, space, "download")
	}
	return nil
}
func (s *Service) event(ctx context.Context, space string, node int64, operation string, bytes int64, names ...string) error {
	a, ok := ctx.Value(actorKey{}).(Actor)
	if !ok {
		return nil
	}
	name := ""
	if len(names) > 0 {
		name = names[0]
	}
	_, e := s.db.Exec(ctx, `INSERT INTO storage_file_events(actor_id,space_id,node_id,operation,channel,session_id,bytes,node_name) VALUES($1,$2,$3,$4,$5,$6,$7,coalesce(nullif($8,''),(SELECT name FROM storage_nodes WHERE id=$3),''))`, a.User, space, node, operation, a.Channel, a.Session, bytes, name)
	return e
}
func (s *Service) Spaces(ctx context.Context, user string, admin bool) ([]Space, error) {
	if _, e := s.Ensure(ctx, user); e != nil {
		return nil, e
	}
	rows, e := s.db.Rows(ctx, `SELECT sp.id::text,sp.owner_id::text,sp.name,sp.shared,sp.enabled,(sp.owner_id=$1 OR coalesce(a.writable,false)),coalesce(p.space_id, $1::uuid)=sp.id,q.quota_bytes,q.used_bytes FROM storage_spaces sp JOIN user_storage q ON q.user_id=sp.id LEFT JOIN storage_acl a ON a.space_id=sp.id AND a.user_id=$1 LEFT JOIN storage_preferences p ON p.user_id=$1 WHERE $2 OR sp.owner_id=$1 OR (sp.shared AND a.user_id IS NOT NULL) ORDER BY sp.created_at,sp.id`, user, admin)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Space{}
	for rows.Next() {
		var v Space
		if e = rows.Scan(&v.ID, &v.Owner, &v.Name, &v.Shared, &v.Enabled, &v.Writable, &v.Default, &v.Quota, &v.Used); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Default(ctx context.Context, user string) (string, error) {
	if _, e := s.Ensure(ctx, user); e != nil {
		return "", e
	}
	id := user
	e := s.db.Row(ctx, "SELECT space_id::text FROM storage_preferences WHERE user_id=$1", user).Scan(&id)
	if e == pgx.ErrNoRows {
		id = user
	} else if e != nil {
		return "", e
	}
	if e = s.Access(ctx, user, id, false); e != nil {
		return "", e
	}
	return id, nil
}
func (s *Service) SetDefault(ctx context.Context, user, space string) error {
	if e := s.Access(ctx, user, space, false); e != nil {
		return e
	}
	_, e := s.db.Exec(ctx, "INSERT INTO storage_preferences VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET space_id=$2", user, space)
	return e
}
func (s *Service) CreateSpace(ctx context.Context, owner, name string, quota int64) (id string, e error) {
	if e = s.db.RequireFeature(ctx, "files.write"); e != nil {
		return "", e
	}
	if len(name) == 0 || len([]rune(name)) > 100 || quota < 0 || quota > 1<<40 {
		return "", syscall.EINVAL
	}
	e = s.db.Within(ctx, func(c context.Context) error {
		if e := s.db.Row(c, "INSERT INTO storage_spaces(owner_id,name,shared) VALUES($1,$2,true) RETURNING id::text", owner, name).Scan(&id); e != nil {
			return e
		}
		if _, e := s.Ensure(c, id); e != nil {
			return e
		}
		_, e := s.db.Exec(c, "UPDATE user_storage SET quota_bytes=$2 WHERE user_id=$1", id, quota)
		return e
	})
	return
}
func (s *Service) BindSession(ctx context.Context, user, session string) (string, error) {
	id, e := s.Default(ctx, user)
	if e != nil {
		return "", e
	}
	_, e = s.db.Exec(ctx, "INSERT INTO session_storage VALUES($1,$2) ON CONFLICT DO NOTHING", session, id)
	return id, e
}

func (s *Service) CanWrite(ctx context.Context, space string) error {
	return s.authorize(ctx, space, "write")
}
