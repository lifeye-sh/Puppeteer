package identity

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"time"
)

type ManagedUser struct {
	DefaultDepartmentSource string    `json:"defaultDepartmentSource,omitempty"`
	DefaultDepartmentID     string    `json:"defaultDepartmentId,omitempty"`
	ID                      string    `json:"id"`
	Username                string    `json:"username"`
	Nickname                string    `json:"nickname"`
	Type                    string    `json:"type"`
	Status                  string    `json:"status"`
	Mail                    string    `json:"mail"`
	Remark                  string    `json:"remark"`
	Version                 int64     `json:"version"`
	CreatedAt               time.Time `json:"createdAt"`
	DepartmentIDs           []string  `json:"departmentIds"`
	RoleIDs                 []string  `json:"roleIds"`
	Password                string    `json:"password,omitempty"`
	EnabledTotp             bool      `json:"enabledTotp"`
}

func (s *Service) FindByName(ctx context.Context, name string) (u User, e error) {
	e = s.db.Row(ctx, `SELECT id,username,nickname,type FROM users WHERE username=$1 AND deleted_at IS NULL`, name).Scan(&u.ID, &u.Username, &u.Nickname, &u.Type)
	return
}
func (s *Service) LockManagement(ctx context.Context) error {
	_, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(78201602)`)
	return e
}
func (s *Service) Users(ctx context.Context, search string, offset, limit int) ([]ManagedUser, int, error) {
	var total int
	e := s.db.Row(ctx, `SELECT count(*) FROM users WHERE deleted_at IS NULL AND (username ILIKE $1 OR nickname ILIKE $1)`, "%"+search+"%").Scan(&total)
	if e != nil {
		return nil, 0, e
	}
	rows, e := s.db.Rows(ctx, `SELECT u.id,u.username,u.nickname,u.type,u.status,u.mail,u.remark,u.version,u.created_at,ARRAY(SELECT department_id::text FROM user_departments WHERE user_id=u.id ORDER BY department_id),EXISTS(SELECT 1 FROM mfa_factors WHERE user_id=u.id AND secret_cipher IS NOT NULL) FROM users u WHERE deleted_at IS NULL AND (username ILIKE $1 OR nickname ILIKE $1) ORDER BY created_at,id LIMIT $2 OFFSET $3`, "%"+search+"%", limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []ManagedUser{}
	for rows.Next() {
		var u ManagedUser
		if e = rows.Scan(&u.ID, &u.Username, &u.Nickname, &u.Type, &u.Status, &u.Mail, &u.Remark, &u.Version, &u.CreatedAt, &u.DepartmentIDs, &u.EnabledTotp); e != nil {
			return nil, 0, e
		}
		u.RoleIDs = []string{}
		out = append(out, u)
	}
	return out, total, rows.Err()
}
func (s *Service) SaveUser(ctx context.Context, in ManagedUser, hash string) (string, error) {
	if !s.db.InTransaction(ctx) {
		var id string
		e := s.db.Within(ctx, func(c context.Context) error { var e error; id, e = s.SaveUser(c, in, hash); return e })
		return id, e
	}
	if e := s.LockManagement(ctx); e != nil {
		return "", e
	}
	departments, e := s.federationDepartmentForNewUser(ctx, in)
	if e != nil {
		return "", e
	}
	in.DepartmentIDs = departments
	in.Username = strings.ToLower(in.Username)
	if in.ID == "" {
		if e := s.db.Admit(ctx, "users", "identity.users"); e != nil {
			return "", e
		}
		e := s.db.Row(ctx, `INSERT INTO users(username,nickname,type,status,mail,remark,password_hash) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, in.Username, in.Nickname, in.Type, in.Status, in.Mail, in.Remark, hash).Scan(&in.ID)
		if e != nil {
			return "", e
		}
	} else {
		var oldType, oldStatus, oldName string
		var version int64
		e := s.db.Row(ctx, `SELECT type,status,username,version FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, in.ID).Scan(&oldType, &oldStatus, &oldName, &version)
		if e != nil {
			return "", e
		}
		if version != in.Version || in.Username != oldName {
			return "", fault.Conflict
		}
		if oldType == "admin" && oldStatus == "enabled" && (in.Type != "admin" || in.Status != "enabled") {
			if e = s.CheckOtherAdmin(ctx, in.ID); e != nil {
				return "", e
			}
		}
		_, e = s.db.Exec(ctx, `UPDATE users SET nickname=$2,type=$3,status=$4,mail=$5,remark=$6,version=version+1,password_hash=CASE WHEN $7='' THEN password_hash ELSE $7 END WHERE id=$1`, in.ID, in.Nickname, in.Type, in.Status, in.Mail, in.Remark, hash)
		if e != nil {
			return "", e
		}
		if e = s.RevokeAll(ctx, in.ID); e != nil {
			return "", e
		}
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM user_departments WHERE user_id=$1`, in.ID); e != nil {
		return "", e
	}
	for _, id := range in.DepartmentIDs {
		if _, e := s.db.Exec(ctx, `INSERT INTO user_departments(user_id,department_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, in.ID, id); e != nil {
			return "", e
		}
	}
	return in.ID, nil
}
func (s *Service) CheckOtherAdmin(ctx context.Context, id string) error {
	var n int
	e := s.db.Row(ctx, `SELECT count(*) FROM users WHERE id<>$1 AND type='admin' AND status='enabled' AND deleted_at IS NULL`, id).Scan(&n)
	if e != nil {
		return e
	}
	if n == 0 {
		return fault.Conflict
	}
	return nil
}
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	if e := s.LockManagement(ctx); e != nil {
		return e
	}
	var kind, status string
	if e := s.db.Row(ctx, `SELECT type,status FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&kind, &status); e != nil {
		return e
	}
	if kind == "admin" && status == "enabled" {
		if e := s.CheckOtherAdmin(ctx, id); e != nil {
			return e
		}
	}
	if _, e := s.db.Exec(ctx, `UPDATE users SET deleted_at=now(),status='disabled',version=version+1 WHERE id=$1`, id); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM user_departments WHERE user_id=$1`, id); e != nil {
		return e
	}
	return s.RevokeAll(ctx, id)
}
func (s *Service) RevokeAll(ctx context.Context, id string) error {
	for _, q := range []string{`UPDATE oauth_token_families SET revoked_at=now() WHERE user_id=$1`, `DELETE FROM oauth_codes WHERE user_id=$1`, `DELETE FROM oauth_pending_authorizations WHERE user_id=$1`, `UPDATE contact_challenges SET consumed_at=now() WHERE user_id=$1 AND consumed_at IS NULL`} {
		if _, e := s.db.Exec(ctx, q, id); e != nil {
			return e
		}
	}

	if _, e := s.db.Exec(ctx, `UPDATE users SET external_login_valid_after=now() WHERE id=$1`, id); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `DELETE FROM oidc_authorizations WHERE user_id=$1`, id); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `UPDATE passkey_credentials SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); e != nil {
		return e
	}
	if _, e := s.db.Exec(ctx, `UPDATE passkey_ceremonies SET consumed_at=now() WHERE user_id=$1 AND consumed_at IS NULL`, id); e != nil {
		return e
	}
	for _, table := range []string{"personal_api_keys", "personal_ssh_keys"} {
		if _, e := s.db.Exec(ctx, `UPDATE `+table+` SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); e != nil {
			return e
		}
	}
	_, e := s.db.Exec(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id)
	return e
}

type Department struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
	Weight   int    `json:"weight"`
	Version  int64  `json:"version"`
}

func (s *Service) Departments(ctx context.Context) ([]Department, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,name,COALESCE(parent_id::text,''),weight,version FROM departments ORDER BY weight,name,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Department{}
	for rows.Next() {
		var d Department
		if e = rows.Scan(&d.ID, &d.Name, &d.ParentID, &d.Weight, &d.Version); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Service) SaveDepartment(ctx context.Context, d Department) (string, error) {
	if e := s.LockManagement(ctx); e != nil {
		return "", e
	}
	var parent any
	if d.ParentID != "" {
		parent = d.ParentID
	}
	if d.ID == "" {
		e := s.db.Row(ctx, `INSERT INTO departments(name,parent_id,weight) VALUES($1,$2,$3) RETURNING id`, d.Name, parent, d.Weight).Scan(&d.ID)
		return d.ID, e
	}
	var cycle bool
	e := s.db.Row(ctx, `WITH RECURSIVE descendants AS (SELECT id FROM departments WHERE id=$1 UNION SELECT d.id FROM departments d JOIN descendants x ON d.parent_id=x.id) SELECT EXISTS(SELECT 1 FROM descendants WHERE id=$2::uuid)`, d.ID, parent).Scan(&cycle)
	if e != nil {
		return "", e
	}
	if cycle {
		return "", fault.Invalid
	}
	tag, e := s.db.Exec(ctx, `UPDATE departments SET name=$2,parent_id=$3,weight=$4,version=version+1 WHERE id=$1 AND version=$5`, d.ID, d.Name, parent, d.Weight, d.Version)
	if e == nil && tag.RowsAffected() != 1 {
		e = fault.Conflict
	}
	return d.ID, e
}
func (s *Service) DeleteDepartment(ctx context.Context, id string) error {
	if e := s.LockManagement(ctx); e != nil {
		return e
	}
	tag, e := s.db.Exec(ctx, `DELETE FROM departments WHERE id=$1`, id)
	var pgError *pgconn.PgError
	if errors.As(e, &pgError) && (pgError.Code == "23503" || pgError.Code == "23001") {
		return fault.Conflict
	}
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}

type LoginLock struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Failures    int        `json:"failures"`
	LockedUntil *time.Time `json:"lockedUntil"`
}

func (s *Service) Locks(ctx context.Context) ([]LoginLock, error) {
	rows, e := s.db.Rows(ctx, `SELECT username,'account',failures,locked_until FROM login_locks WHERE locked_until>now()
 UNION ALL SELECT 'ip:'||host(address),'ip',failures,locked_until FROM login_ip_locks WHERE locked_until>now() ORDER BY 1`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []LoginLock{}
	for rows.Next() {
		var v LoginLock
		if e = rows.Scan(&v.ID, &v.Kind, &v.Failures, &v.LockedUntil); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Unlock(ctx context.Context, name string) error {
	if strings.HasPrefix(name, "ip:") {
		return s.UnlockIP(ctx, strings.TrimPrefix(name, "ip:"))
	}
	_, e := s.db.Exec(ctx, `DELETE FROM login_locks WHERE username=$1`, name)
	return e
}
func (s *Service) CheckLock(ctx context.Context, name string, ip ...string) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(tx context.Context) error { return s.CheckLock(tx, name, ip...) })
	}
	configuration, e := system.New(s.db).SecurityConfiguration(ctx)
	if e != nil {
		return e
	}
	enabled, ok := configuration.Values["login-lock-enabled"].(bool)
	if !ok {
		return fault.Invalid
	}
	if !enabled {
		return nil
	}
	if len(ip) > 0 {
		if e = s.lockIP(ctx, ip[0]); e != nil {
			return e
		}
		if e = s.CheckIPLock(ctx, ip[0]); e != nil {
			return e
		}
	}
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,43))`, name); e != nil {
		return e
	}
	var locked bool
	e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM login_locks WHERE username=$1 AND locked_until>now())`, name).Scan(&locked)
	if e != nil {
		return e
	}
	if locked {
		return fault.Locked
	}
	return nil
}
func (s *Service) FailedPassword(ctx context.Context, name string, ip ...string) error {
	if !s.db.InTransaction(ctx) {
		return s.db.Within(ctx, func(tx context.Context) error { return s.FailedPassword(tx, name, ip...) })
	}
	if len(ip) > 0 {
		if e := s.failedIP(ctx, ip[0]); e != nil {
			return e
		}
	}
	// Reuse CheckLock's per-account lock even when called outside the HTTP login path.
	if _, e := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,43))`, name); e != nil {
		return e
	}
	configuration, e := system.New(s.db).SecurityConfiguration(ctx)
	if e != nil {
		return e
	}
	enabled, ok := configuration.Values["login-lock-enabled"].(bool)
	if !ok {
		return fault.Invalid
	}
	if !enabled {
		return nil
	}
	window, limit := configuration.Values.Int("login-lock-failed-duration"), configuration.Values.Int("login-lock-failed-times")
	duration, present := configuration.Values["login-lock-account-duration"].(int)
	if !present || window < 1 || window > 1440 || limit < 1 || limit > 100 || duration < 0 || duration > 10080 {
		return fault.Invalid
	}
	// Active locks keep their original deadline. An elapsed lock starts a fresh counter.
	_, e = s.db.Exec(ctx, `INSERT INTO login_locks(username,failures,locked_until,last_failed_at,configuration_revision)
 VALUES($1,1,CASE WHEN $3::integer=1 AND $4::integer>0 THEN now()+make_interval(mins=>$4) END,now(),$5)
 ON CONFLICT(username) DO UPDATE SET
 failures=CASE WHEN login_locks.locked_until<=now() OR login_locks.last_failed_at<=now()-make_interval(mins=>$2) THEN 1 ELSE least(login_locks.failures::bigint+1,2147483647)::integer END,
 locked_until=NULL,last_failed_at=now(),configuration_revision=$5
 WHERE login_locks.locked_until IS NULL OR login_locks.locked_until<=now()`, name, window, limit, duration, configuration.ID)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `UPDATE login_locks SET locked_until=now()+make_interval(mins=>$2) WHERE username=$1 AND locked_until IS NULL AND failures>=$3 AND $2::integer>0`, name, duration, limit)
	return e
}
