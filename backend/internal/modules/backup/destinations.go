package backup

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/jobs"
	"next-terminal/backend/internal/platform/secretbox"
)

func (s *Service) Destinations(ctx context.Context) ([]Destination, error) {
	rows, e := s.db.Rows(ctx, `SELECT id::text,name,kind,enabled,config,revision,octet_length(secret)>0 FROM backup_destinations ORDER BY name,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Destination{}
	for rows.Next() {
		var v Destination
		var raw []byte
		if e = rows.Scan(&v.ID, &v.Name, &v.Kind, &v.Enabled, &raw, &v.Revision, &v.HasSecret); e != nil {
			return nil, e
		}
		meta := v
		if e = json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		v.ID = meta.ID
		v.Name = meta.Name
		v.Kind = meta.Kind
		v.Enabled = meta.Enabled
		v.Revision = meta.Revision
		v.HasSecret = meta.HasSecret
		v.Secret = ""
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) SaveDestination(ctx context.Context, d Destination, key []byte) (id string, e error) {
	if e = ValidateDestination(d); e != nil {
		return "", fault.Invalid
	}
	e = s.db.Within(ctx, func(c context.Context) error {
		id = d.ID
		var encrypted []byte
		if id == "" {
			if d.Secret == "" {
				return fault.Invalid
			}
			if e := s.db.Row(c, "SELECT gen_random_uuid()::text").Scan(&id); e != nil {
				return e
			}
		} else {
			var oldRaw []byte
			var revision int64
			if e := s.db.Row(c, `SELECT config,secret,revision FROM backup_destinations WHERE id=$1 FOR UPDATE`, id).Scan(&oldRaw, &encrypted, &revision); e != nil {
				return e
			}
			if revision != d.Revision {
				return fault.Conflict
			}
			var old Destination
			if e := json.Unmarshal(oldRaw, &old); e != nil {
				return e
			}
			if d.Secret == "" && (old.Endpoint != d.Endpoint || old.Username != d.Username || old.Kind != d.Kind || old.HostKey != d.HostKey) {
				return fault.Invalid
			}
		}
		if d.Secret != "" {
			var e error
			encrypted, e = secretbox.Seal(key, []byte(d.Secret), "backup-destination:"+id)
			if e != nil {
				return e
			}
		}
		d.Secret = ""
		d.HasSecret = true
		d.ID = id
		raw, e := json.Marshal(d)
		if e != nil {
			return e
		}
		if d.Revision == 0 {
			_, e = s.db.Exec(c, `INSERT INTO backup_destinations(id,name,kind,enabled,config,secret) VALUES($1,$2,$3,$4,$5,$6)`, id, d.Name, d.Kind, d.Enabled, raw, encrypted)
			return e
		}
		tag, e := s.db.Exec(c, `UPDATE backup_destinations SET name=$2,kind=$3,enabled=$4,config=$5,secret=$6,revision=revision+1 WHERE id=$1 AND revision=$7`, id, d.Name, d.Kind, d.Enabled, raw, encrypted, d.Revision)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return fault.Conflict
		}
		return nil
	})
	return
}
func (s *Service) DestinationSecret(ctx context.Context, id string, key []byte) (Destination, error) {
	var d Destination
	var raw, encrypted []byte
	e := s.db.Row(ctx, `SELECT config,secret FROM backup_destinations WHERE id=$1`, id).Scan(&raw, &encrypted)
	if e != nil {
		return d, e
	}
	if e = json.Unmarshal(raw, &d); e != nil {
		return d, e
	}
	plain, e := secretbox.Open(key, encrypted, "backup-destination:"+id)
	if e != nil {
		return d, e
	}
	d.Secret = string(plain)
	clear(plain)
	return d, nil
}

type Copy struct {
	TaskID        string `json:"taskId"`
	DestinationID string `json:"destinationId"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	RemoteKey     string `json:"remoteKey"`
	State         string `json:"state"`
	Attempts      int    `json:"attempts"`
	Reason        string `json:"reason"`
}

func (s *Service) Copies(ctx context.Context) ([]Copy, error) {
	rows, e := s.db.Rows(ctx, `SELECT task_id::text,destination_id::text,config_snapshot->>'name',config_snapshot->>'kind',remote_key,state,attempts,reason FROM backup_copies ORDER BY task_id,destination_id LIMIT 300`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Copy{}
	for rows.Next() {
		var v Copy
		if e = rows.Scan(&v.TaskID, &v.DestinationID, &v.Name, &v.Kind, &v.RemoteKey, &v.State, &v.Attempts, &v.Reason); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) snapshotDestinations(ctx context.Context, id string) error {
	rows, e := s.db.Rows(ctx, `SELECT id::text,config,secret FROM backup_destinations WHERE enabled`)
	if e != nil {
		return e
	}
	type item struct {
		id          string
		raw, secret []byte
	}
	items := []item{}
	for rows.Next() {
		var v item
		if e = rows.Scan(&v.id, &v.raw, &v.secret); e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range items {
		key := id + "-" + identity.NewToken()[:16] + ".ntbackup.age"
		if _, e = s.db.Exec(ctx, `INSERT INTO backup_copies(task_id,destination_id,config_snapshot,secret,remote_key) VALUES($1,$2,$3,$4,$5)`, id, v.id, v.raw, v.secret, key); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) CopyAll(ctx context.Context, id, local, digest string, key []byte, j jobs.Job) error {
	rows, e := s.db.Rows(ctx, `SELECT destination_id::text,config_snapshot,secret,remote_key,state FROM backup_copies WHERE task_id=$1`, id)
	if e != nil {
		return e
	}
	type item struct {
		id, remote, state string
		raw, secret       []byte
	}
	items := []item{}
	for rows.Next() {
		var v item
		if e = rows.Scan(&v.id, &v.raw, &v.secret, &v.remote, &v.state); e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	failed := false
	for _, v := range items {
		if v.state == "verified" {
			continue
		}
		var d Destination
		if e = json.Unmarshal(v.raw, &d); e != nil {
			return e
		}
		plain, e := secretbox.Open(key, v.secret, "backup-destination:"+v.id)
		if e != nil {
			return e
		}
		d.Secret = string(plain)
		clear(plain)
		if e = s.db.Within(ctx, func(c context.Context) error {
			if e := jobs.New(s.db).Fence(c, j); e != nil {
				return e
			}
			_, e := s.db.Exec(c, `UPDATE backup_copies SET state='copying',attempts=attempts+1 WHERE task_id=$1 AND destination_id=$2`, id, v.id)
			return e
		}); e != nil {
			return e
		}
		_, copyErr := ResumeCopyRemote(ctx, d, local, v.remote, digest)
		d.Secret = ""
		state, reason := "verified", ""
		if copyErr != nil {
			state = "failed"
			reason = "remote_copy_or_verification_failed"
			failed = true
		}
		if e = s.db.Within(ctx, func(c context.Context) error {
			if e := jobs.New(s.db).Fence(c, j); e != nil {
				return e
			}
			_, e := s.db.Exec(c, `UPDATE backup_copies SET state=$3,reason=$4,verified_at=CASE WHEN $3='verified' THEN now() END WHERE task_id=$1 AND destination_id=$2`, id, v.id, state, reason)
			return e
		}); e != nil {
			return e
		}
	}
	if failed {
		return errors.New("one or more remote copies failed")
	}
	return nil
}
